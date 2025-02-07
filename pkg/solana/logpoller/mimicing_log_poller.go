package logpoller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	solanaIDL "github.com/smartcontractkit/chainlink-ccip/chains/solana"

	"github.com/smartcontractkit/chainlink-solana/pkg/solana/client"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/codec"
)

func newMimicingLogPoller(lggr logger.SugaredLogger, orm ORM, cl RPCClient) *Service {
	sourceContractStr := os.Getenv("SOURCE_CONTRACT_ADDRESS")
	if sourceContractStr == "" {
		panic(fmt.Errorf("env variable SOURCE_CONTRACT_ADDRESS not set"))
	}

	sourceContract, err := solana.PublicKeyFromBase58(sourceContractStr)
	if err != nil {
		panic(fmt.Errorf("invalid address %s: %w", sourceContractStr, err))
	}

	wrappedCl := newWrappedClient(cl, sourceContract)
	service := newService(lggr, orm, wrappedCl)
	go func(lp *Service) {
		for {
			err := tryRegisterCCIPMessageSentFilter(context.Background(), lp)
			if err == nil {
				lp.lggr.Infow("hardcoded filter registered successfully")
				return
			}

			lp.lggr.Errorw("failed to register hardcoded filter", "err", err)
			time.Sleep(1 * time.Second)
		}
	}(service)
	return service
}

func tryRegisterCCIPMessageSentFilter(ctx context.Context, lp *Service) error {
	ctx, cancel := lp.eng.Ctx(ctx)
	defer cancel()
	addr, err := solana.PublicKeyFromBase58(targetContract)
	if err != nil {
		return fmt.Errorf("invalid address %s: %w", targetContract, err)
	}
	var idl codec.IDL
	err = json.Unmarshal([]byte(solanaIDL.FetchCCIPRouterIDL()), &idl)
	if err != nil {
		return fmt.Errorf("invalid idl: %w", err)
	}

	ccipMsgSentIndex := slices.IndexFunc(idl.Events, func(event codec.IdlEvent) bool {
		return event.Name == "CCIPMessageSent"
	})
	slot, err := lp.client.SlotHeightWithCommitment(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return fmt.Errorf("failed to get latest slot: %w", err)
	}

	startingBlock := slot - 72000 // 8 hours delay
	version := client.MaxSupportTransactionVersion
	block, err := lp.client.GetBlockWithOpts(ctx, startingBlock, &rpc.GetBlockOpts{MaxSupportedTransactionVersion: &version})
	if err != nil {
		return fmt.Errorf("failed to get starting block %d: %w", startingBlock, err)
	}

	lp.lggr.Infow("registering CCIPMessageSentFilter", "startingBlock", startingBlock, "blockTime", block.BlockTime.Time())

	if ccipMsgSentIndex == -1 {
		return fmt.Errorf("failed to find CCIPMessageSent event")
	}
	err = lp.RegisterFilter(ctx, Filter{
		Name:          "hardcoded-filter",
		Address:       PublicKey(addr),
		EventName:     "CCIPMessageSent",
		EventSig:      NewEventSignatureFromName("CCIPMessageSent"),
		StartingBlock: int64(startingBlock), //nolint:gosec
		EventIdl: EventIdl{
			Event: idl.Events[ccipMsgSentIndex],
			Types: idl.Types,
		},
		SubkeyPaths: SubKeyPaths{[]string{"SequenceNumber"}, []string{"Message", "Sender"}, []string{"Message", "Receiver"}},
		Retention:   time.Hour * 24 * 8,
	})
	if err != nil {
		return fmt.Errorf("could not register filter: %w", err)
	}

	return nil
}

// mimicContractClient - acts as if targetContract was actually deployed on chain and was producing transactions
// that were produced by sourceContract.
type mimicContractClient struct {
	RPCClient
	sourceContract    string
	sourceContractPub solana.PublicKey
}

func newWrappedClient(rpc RPCClient, sourceContract solana.PublicKey) *mimicContractClient {
	return &mimicContractClient{
		RPCClient:         rpc,
		sourceContract:    sourceContract.String(),
		sourceContractPub: sourceContract,
	}
}

const targetContract = "C8WSPj3yyus1YN3yNB6YA5zStYtbjQWtpmKadmvyUXq8"

var logs = []string{"Program C8WSPj3yyus1YN3yNB6YA5zStYtbjQWtpmKadmvyUXq8 invoke [1]", "Program log: Instruction: CcipSend", "Program 11111111111111111111111111111111 invoke [2]", "Program 11111111111111111111111111111111 success", "Program FeeVB9Q77QvyaENRL1i77BjW6cTkaWwNLjNbZg9JHqpw invoke [2]", "Program log: Instruction: GetFee", "Program FeeVB9Q77QvyaENRL1i77BjW6cTkaWwNLjNbZg9JHqpw consumed 23263 of 144094 compute units", "Program return: FeeVB9Q77QvyaENRL1i77BjW6cTkaWwNLjNbZg9JHqpw BpuIV/6rgYT7aH9jRhjANdrEOdwa6ztVmKDwAAAAAAFepiIAAAAAAAl3AwAAAAAAAAAAABUAAAAYHc8QQA0DAAAAAAAAAAAAAAAAAABADQMAAAAAAAAAAAAAAAAAAA==", "Program FeeVB9Q77QvyaENRL1i77BjW6cTkaWwNLjNbZg9JHqpw success", "Program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA invoke [2]", "Program log: Instruction: TransferChecked", "Program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA consumed 6346 of 117255 compute units", "Program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA success", "Program data: F01Jt3u5czkVAAAAAAAAAAEAAAAAAAAApLiJ2pbEis44BGzfl7xek8Ij19Ka4TaiIqF4HOlCTV0PAAAAAAAAABUAAAAAAAAAAQAAAAAAAAABAAAAAAAAAOZGQx39qtvzTlVMsFNekQO+afganyDcB4613MPZXq5mAwAAAAQFBiAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAABUAAAAYHc8QQA0DAAAAAAAAAAAAAAAAAAAGm4hX/quBhPtof2NGGMA12sQ53BrrO1WYoPAAAAAAAQAAAABepiIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAl3AwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "Program C8WSPj3yyus1YN3yNB6YA5zStYtbjQWtpmKadmvyUXq8 consumed 92361 of 200000 compute units", "Program C8WSPj3yyus1YN3yNB6YA5zStYtbjQWtpmKadmvyUXq8 success"}

func (c *mimicContractClient) GetBlockWithOpts(ctx context.Context, slot uint64, opts *rpc.GetBlockOpts) (*rpc.GetBlockResult, error) {
	blockResult, err := c.RPCClient.GetBlockWithOpts(ctx, slot, opts)
	if err != nil {
		return nil, err
	}

	// replace logs
	start := -1
	for i, tx := range blockResult.Transactions {
		idx := slices.IndexFunc(tx.Meta.LogMessages, func(s string) bool {
			return strings.Contains(s, c.sourceContract)
		})
		if idx != -1 {
			start++
			tx.Meta.LogMessages = logs
			blockResult.Transactions[start] = blockResult.Transactions[i]
		}
	}

	blockResult.Transactions = blockResult.Transactions[:start+1]

	return blockResult, nil
}

func (c *mimicContractClient) GetSignaturesForAddressWithOpts(ctx context.Context, pubKey solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error) {
	return c.RPCClient.GetSignaturesForAddressWithOpts(ctx, c.sourceContractPub, opts)
}
