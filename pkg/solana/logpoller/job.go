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
	blockNumber uint64
	blockHash   solana.Hash
	trxIdx      int
	trxSig      solana.Signature
}

// processEventJob is a job that processes a single event. The parser should be a pure function
// such that no network requests are made and no side effects are produced.
type processEventJob struct {
	parser Parser
	event  ProgramEvent
}

func (j *processEventJob) String() string {
	return fmt.Sprintf("processEventJob for event: %s", j.event.FunctionName)
}

func (j *processEventJob) Run(_ context.Context) error {
	return j.parser.ProcessEvent(j.event)
}

// getTransactionsFromBlockJob is a job that fetches transaction signatures from a block and loads
// the job queue with getTransactionLogsJobs for each transaction found in the block.
type getTransactionsFromBlockJob struct {
	slotNumber uint64
	client     RPCClient
	parser     Parser
	chJobs     chan Job
}

func (j *getTransactionsFromBlockJob) String() string {
	return fmt.Sprintf("getTransactionsFromBlockJob for block: %d", j.slotNumber)
}

func (j *getTransactionsFromBlockJob) Run(ctx context.Context) error {
	var excludeRewards bool

	block, err := j.client.GetBlockWithOpts(
		ctx,
		j.slotNumber,
		// You can specify more options here:
		&rpc.GetBlockOpts{
			Encoding:   solana.EncodingBase64,
			Commitment: rpc.CommitmentFinalized,
			// get the full transaction details
			TransactionDetails: rpc.TransactionDetailsFull,
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
		// You can specify more options here:
		&rpc.GetBlockOpts{
			Encoding:   solana.EncodingBase64,
			Commitment: rpc.CommitmentFinalized,
			// get the signatures only
			TransactionDetails: rpc.TransactionDetailsSignatures,
			// exclude rewards
			Rewards: &excludeRewards,
		},
	)
	if err != nil {
		return err
	}

	detail := eventDetail{
		blockNumber: *block.BlockHeight,
		blockHash:   block.Blockhash,
	}

	for idx, trx := range block.Transactions {
		detail.trxIdx = idx
		if len(blockSigsOnly.Signatures)-1 <= idx {
			detail.trxSig = blockSigsOnly.Signatures[idx]
		}

		messagesToEvents(trx.Meta.LogMessages, j.parser, detail, j.chJobs)
	}

	return nil
}

func messagesToEvents(messages []string, parser Parser, detail eventDetail, chJobs chan Job) {
	var logIdx uint
	for _, outputs := range parse(messages) {
		for _, event := range outputs.Events {
			logIdx++

			event.BlockNumber = detail.blockNumber
			event.BlockHash = detail.blockHash.String()
			event.TransactionHash = detail.trxSig.String()
			event.TransactionIndex = detail.trxIdx
			event.TransactionLogIndex = logIdx

			chJobs <- &processEventJob{
				parser: parser,
				event:  event,
			}
		}
	}
}
