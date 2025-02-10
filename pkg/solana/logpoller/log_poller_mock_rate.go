package logpoller

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func newMockedRateLogPoller(lggr logger.SugaredLogger, orm ORM, cl RPCClient) *Service {
	eventsPerSecStr := os.Getenv("EVENTS_PER_SEC")
	eventsPerSec, err := strconv.ParseFloat(eventsPerSecStr, 64)
	if err != nil {
		panic(fmt.Errorf("invalid %s: %w", eventsPerSecStr, err))
	}

	wrappedCl := newMockedRateClient(cl, eventsPerSec)
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

// mockedRateClient - acts as if targetContract was actually deployed on chain and was producing transactions
// at specified rate
type mockedRateClient struct {
	RPCClient
	eventsPerBlock                float64
	numberOfBlocksInGetSignatures float64
}

func newMockedRateClient(rpc RPCClient, eventsPerSecond float64) *mockedRateClient {
	eventsPerBlock := eventsPerSecond / 2.5 // assuming 400ms block time
	if eventsPerBlock < 1 {
		panic("eventsPerSecond must be greater than or equal to 4")
	}
	const maxNumberOfSignaturesPerResult = 1000
	numberOfBlocksInGetSignatures := maxNumberOfSignaturesPerResult / eventsPerBlock
	if numberOfBlocksInGetSignatures < 1 {
		panic("number of events per second is too large => numberOfBlocksInGetSignatures is less than 1")
	}
	return &mockedRateClient{
		RPCClient:                     rpc,
		eventsPerBlock:                eventsPerBlock,
		numberOfBlocksInGetSignatures: numberOfBlocksInGetSignatures,
	}
}

func (c *mockedRateClient) GetBlockWithOpts(ctx context.Context, slot uint64, opts *rpc.GetBlockOpts) (*rpc.GetBlockResult, error) {
	blockResult, err := c.RPCClient.GetBlockWithOpts(ctx, slot, opts)
	if err != nil {
		return nil, err
	}

	// replace logs
	numberOfEvents := 0
	for _, tx := range blockResult.Transactions {
		tx.Meta.LogMessages = logs
		numberOfEvents++
		if numberOfEvents >= int(c.eventsPerBlock) {
			break
		}
	}

	return blockResult, nil
}

func (c *mockedRateClient) GetSignaturesForAddressWithOpts(ctx context.Context, pubKey solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error) {
	if opts.MinContextSlot == nil {
		return nil, errors.New("minimum context slot is required for mocked rate client")
	}

	endingSlot := *opts.MinContextSlot
	if !opts.Before.IsZero() {
		var err error
		endingSlot, err = readIntFromSignature(opts.Before)
		if err != nil {
			return nil, fmt.Errorf("failed to read ending slot from signature: %w", err)
		}
	}

	const fractionOfEmptySlots = 0.1
	startingSlot := endingSlot - uint64(c.numberOfBlocksInGetSignatures*(1+fractionOfEmptySlots))
	// TODO: endingSlot is expected to be always finalized, but might want to add commitment for additional safety
	blocks, err := c.GetBlocks(ctx, startingSlot, &endingSlot)
	if err != nil {
		return nil, fmt.Errorf("failed to get blocks to block GetSignauters: %w", err)
	}

	results := make([]*rpc.TransactionSignature, 0, int64(c.numberOfBlocksInGetSignatures))
	for i := len(blocks) - 1; i >= 0; i-- {
		if len(results) >= int(c.numberOfBlocksInGetSignatures*c.eventsPerBlock) {
			break
		}

		for range int(c.eventsPerBlock) {
			results = append(results, &rpc.TransactionSignature{
				Slot:      blocks[i],
				Signature: intToSignature(blocks[i]),
			})
		}
	}
	return results, nil
}

func intToSignature(v uint64) solana.Signature {
	res, err := binary.Append(nil, binary.BigEndian, v)
	if err != nil {
		panic(fmt.Errorf("failed to convert integer to signature: %w", err))
	}
	var sig solana.Signature
	copy(sig[:], res)
	return sig
}

func readIntFromSignature(sig solana.Signature) (uint64, error) {
	var result uint64
	_, err := binary.Decode(sig[:], binary.BigEndian, &result)
	return result, err
}
