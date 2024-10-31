package logpoller

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

type Parser interface {
	// ProcessEvent should take a ProgramEvent and parse it based on log signature
	// and expected encoding. Only return errors that cannot be handled and
	// should exit further transaction processing on the running thread.
	//
	// ProcessEvent should be thread safe.
	ProcessEvent(ProgramEvent) error
}

type RPCClient interface {
	GetLatestBlockhash(ctx context.Context, commitment rpc.CommitmentType) (out *rpc.GetLatestBlockhashResult, err error)
	GetBlocks(ctx context.Context, startSlot uint64, endSlot *uint64, commitment rpc.CommitmentType) (out rpc.BlocksResult, err error)
	GetBlockWithOpts(context.Context, uint64, *rpc.GetBlockOpts) (*rpc.GetBlockResult, error)
	GetSignaturesForAddressWithOpts(context.Context, solana.PublicKey, *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error)
	GetTransaction(context.Context, solana.Signature, *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error)
}

const (
	DefaultNextSlotPollingInterval = 1_000 * time.Millisecond
)

type EncodedLogCollector struct {
	// service state management
	services.Service
	engine *services.Engine

	// dependencies and configuration
	client       RPCClient
	parser       Parser
	lggr         logger.Logger
	rpcTimeLimit time.Duration

	// internal state
	chSlot  chan uint64
	chBlock chan uint64
	chJobs  chan Job
	workers *WorkerGroup

	mu                sync.RWMutex
	loadingBlocks     atomic.Bool
	highestSlot       uint64
	highestSlotLoaded uint64
	lastSentSlot      atomic.Uint64
}

func NewEncodedLogCollector(
	client RPCClient,
	parser Parser,
	lggr logger.Logger,
) *EncodedLogCollector {
	c := &EncodedLogCollector{
		client:       client,
		parser:       parser,
		chSlot:       make(chan uint64, 1),
		chBlock:      make(chan uint64, 1),
		chJobs:       make(chan Job, 1),
		lggr:         lggr,
		rpcTimeLimit: 1 * time.Second,
	}

	c.Service, c.engine = services.Config{
		Name: "EncodedLogCollector",
		NewSubServices: func(lggr logger.Logger) []services.Service {
			c.workers = NewWorkerGroup(DefaultWorkerCount, lggr)

			return []services.Service{c.workers}
		},
		Start: c.start,
		Close: c.close,
	}.NewServiceEngine(lggr)

	return c
}

func (c *EncodedLogCollector) BackfillForAddress(ctx context.Context, address string, fromSlot uint64) error {
	pubKey, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		return err
	}

	sigs, err := c.client.GetSignaturesForAddressWithOpts(ctx, pubKey, &rpc.GetSignaturesForAddressOpts{
		Commitment:     rpc.CommitmentFinalized,
		MinContextSlot: &fromSlot,
	})
	if err != nil {
		return err
	}

	slots := make(map[uint64]struct{})

	for _, sig := range sigs {
		_, ok := slots[sig.Slot]
		if !ok {
			if err := c.workers.Do(ctx, &getTransactionsFromBlockJob{
				slotNumber: sig.Slot,
				client:     c.client,
				parser:     c.parser,
				chJobs:     c.chJobs,
			}); err != nil {
				return err
			}

			slots[sig.Slot] = struct{}{}
		}
	}

	return nil
}

func (c *EncodedLogCollector) start(ctx context.Context) error {
	c.engine.Go(c.runSlotPolling)
	c.engine.Go(c.runSlotProcessing)
	c.engine.Go(c.runBlockProcessing)
	c.engine.Go(c.runJobProcessing)

	return nil
}

func (c *EncodedLogCollector) close() error {
	return nil
}

func (c *EncodedLogCollector) runSlotPolling(ctx context.Context) {
	for {
		timer := time.NewTimer(DefaultNextSlotPollingInterval)

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			ctxB, cancel := context.WithTimeout(ctx, c.rpcTimeLimit)

			// not to be run as a job, but as a blocking call
			result, err := c.client.GetLatestBlockhash(ctxB, rpc.CommitmentFinalized)
			if err != nil {
				c.lggr.Info("failed to get latest blockhash", "err", err)
				cancel()

				continue
			}

			cancel()

			// if the slot is not higher than the highest slot, skip it
			if c.lastSentSlot.Load() >= result.Context.Slot {
				continue
			}

			c.lastSentSlot.Store(result.Context.Slot)
			c.chSlot <- result.Context.Slot
		}

		timer.Stop()
	}
}

func (c *EncodedLogCollector) runSlotProcessing(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case slot := <-c.chSlot:
			c.mu.Lock()

			if slot > c.highestSlot {
				c.highestSlot = slot

				if !c.loadingBlocks.Load() {
					c.loadingBlocks.Store(true)

					// run routine to load blocks in slot range
					go func(start, end uint64) {
						defer c.loadingBlocks.Store(false)

						if err := c.loadSlotBlocksRange(ctx, start, end); err != nil {
							// TODO: probably log something here
							// a retry will happen anyway on the next round of slots
							// so the error is handled by doing nothing
							c.lggr.Info("failed to load slot blocks range", "start", start, "end", end, "err", err)

							return
						}

						c.mu.Lock()
						c.highestSlotLoaded = end
						c.mu.Unlock()
					}(c.highestSlotLoaded+1, c.highestSlot)
				}
			}

			c.mu.Unlock()
		}
	}
}

func (c *EncodedLogCollector) runBlockProcessing(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case block := <-c.chBlock:
			if err := c.workers.Do(ctx, &getTransactionsFromBlockJob{
				slotNumber: block,
				client:     c.client,
				parser:     c.parser,
				chJobs:     c.chJobs,
			}); err != nil {
				c.lggr.Infof("failed to add job to queue: %s", err)
			}
		}
	}
}

func (c *EncodedLogCollector) runJobProcessing(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-c.chJobs:
			if err := c.workers.Do(ctx, job); err != nil {
				c.lggr.Infof("failed to add job to queue: %s", err)
			}
		}
	}
}

func (c *EncodedLogCollector) loadSlotBlocksRange(ctx context.Context, start, end uint64) error {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeLimit)
	defer cancel()

	if start >= end {
		return errors.New("the start block must come before the end block")
	}

	var (
		result rpc.BlocksResult
		err    error
	)

	if result, err = c.client.GetBlocks(ctx, start, &end, rpc.CommitmentFinalized); err != nil {
		return err
	}

	for _, block := range result {
		c.chBlock <- block
	}

	return nil
}
