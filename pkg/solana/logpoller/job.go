package logpoller

import (
	"context"
	"fmt"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// Job is a function that should be run by the worker group. The context provided
// allows the Job to cancel if the worker group is closed. All other life-cycle
// management should be wrapped within the Job.
type Job interface {
	String() string
	Run(context.Context) error
}

type retryableJob struct {
	name  string
	count uint8
	when  time.Time
	job   Job
}

func (j retryableJob) String() string {
	return j.job.String()
}

func (j retryableJob) Run(ctx context.Context) error {
	return j.job.Run(ctx)
}

type eventDetail struct {
	slotNumber  uint64
	blockHeight uint64
	blockHash   solana.Hash
	trxIdx      int
	trxSig      solana.Signature
}

type wrappedParser interface {
	ProgramEventProcessor
	ExpectBlock(uint64)
}

// getTransactionsFromBlockJob is a job that fetches transaction signatures from a block and loads
// the job queue with getTransactionLogsJobs for each transaction found in the block.
type getTransactionsFromBlockJob struct {
	slotNumber uint64
	client     RPCClient
	parser     wrappedParser
	chJobs     chan Job
}

func (j *getTransactionsFromBlockJob) String() string {
	return fmt.Sprintf("getTransactionsFromBlockJob for block: %d", j.slotNumber)
}

func (j *getTransactionsFromBlockJob) Run(ctx context.Context) error {
	var excludeRewards bool

	version := uint64(0) // pull all tx types (legacy + v0)
	block, err := j.client.GetBlockWithOpts(
		ctx,
		j.slotNumber,
		&rpc.GetBlockOpts{
			Encoding:   solana.EncodingBase64,
			Commitment: rpc.CommitmentFinalized,
			// get the full transaction details
			TransactionDetails:             rpc.TransactionDetailsFull,
			MaxSupportedTransactionVersion: &version,
			// exclude rewards
			Rewards: &excludeRewards,
		},
	)
	if err != nil {
		return err
	}

	blockSigsOnly, err := j.client.GetBlockWithOpts(
		ctx,
		j.slotNumber,
		&rpc.GetBlockOpts{
			Encoding:   solana.EncodingBase64,
			Commitment: rpc.CommitmentFinalized,
			// get the signatures only
			TransactionDetails:             rpc.TransactionDetailsSignatures,
			MaxSupportedTransactionVersion: &version,

			// exclude rewards
			Rewards: &excludeRewards,
		},
	)
	if err != nil {
		return err
	}

	detail := eventDetail{
		slotNumber: j.slotNumber,
		blockHash:  block.Blockhash,
	}

	if block.BlockHeight != nil {
		detail.blockHeight = *block.BlockHeight
	}

	if len(block.Transactions) != len(blockSigsOnly.Signatures) {
		return fmt.Errorf("block %d has %d transactions but %d signatures", j.slotNumber, len(block.Transactions), len(blockSigsOnly.Signatures))
	}

	events := make([]ProgramEvent, 0, len(block.Transactions))
	for idx, trx := range block.Transactions {
		detail.trxIdx = idx
		if len(blockSigsOnly.Signatures)-1 <= idx {
			detail.trxSig = blockSigsOnly.Signatures[idx]
		}

		txEvents := j.messagesToEvents(trx.Meta.LogMessages, detail)
		events = append(events, txEvents...)
	}

	return j.parser.Process(Block{
		SlotNumber: j.slotNumber,
		BlockHash:  block.Blockhash,
		Events:     events,
	})
}

func (j *getTransactionsFromBlockJob) messagesToEvents(messages []string, detail eventDetail) []ProgramEvent {
	// TODO: changes to parsing might cause changes in logIdx generate, we might want to find a more stable way of doing it.
	var logIdx uint
	// TODO: only parse logs produced by CL contracts, otherwise if we enable custom user calls, it will be possible to forge a msg.
	events := make([]ProgramEvent, 0, len(messages))
	for _, outputs := range parseProgramLogs(messages) {
		for i, event := range outputs.Events {
			event.SlotNumber = detail.slotNumber
			event.BlockHeight = detail.blockHeight
			event.BlockHash = detail.blockHash
			event.TransactionHash = detail.trxSig
			event.TransactionIndex = detail.trxIdx
			event.TransactionLogIndex = logIdx

			logIdx++
			outputs.Events[i] = event
		}

		events = append(events, outputs.Events...)
	}

	return events
}
