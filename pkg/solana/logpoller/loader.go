package logpoller

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

type Block struct {
	SlotNumber uint64
	BlockHash  solana.Hash
	Events     []ProgramEvent
}

type ProgramEventProcessor interface {
	// Process should take a ProgramEvent and parseProgramLogs it based on log signature
	// and expected encoding. Only return errors that cannot be handled and
	// should exit further transaction processing on the running thread.
	//
	// Process should be thread safe.
	Process(Block) error
}

type RPCClient interface {
	GetLatestBlockhash(ctx context.Context, commitment rpc.CommitmentType) (out *rpc.GetLatestBlockhashResult, err error)
	GetBlocks(ctx context.Context, startSlot uint64, endSlot *uint64, commitment rpc.CommitmentType) (out rpc.BlocksResult, err error)
	GetBlockWithOpts(context.Context, uint64, *rpc.GetBlockOpts) (*rpc.GetBlockResult, error)
	GetSignaturesForAddressWithOpts(context.Context, solana.PublicKey, *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error)
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
	lggr         logger.Logger
	rpcTimeLimit time.Duration

	// internal state
	chSlot  chan uint64
	chBlock chan uint64
	chJobs  chan Job
	workers *WorkerGroup

	lastSentSlot atomic.Uint64
}

func NewEncodedLogCollector(
	client RPCClient,
	lggr logger.Logger,
) *EncodedLogCollector {
	c := &EncodedLogCollector{
		client:       client,
		chSlot:       make(chan uint64),
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

func (c *EncodedLogCollector) getSlotsToFetch(ctx context.Context, addresses []PublicKey, fromSlot, toSlot uint64) ([]uint64, error) {
	// identify slots to fetch
	slotsForAddressJobs := make([]*getSlotsForAddressJob, len(addresses))
	slotsToFetch := make(map[uint64]struct{}, toSlot-fromSlot)
	var slotsToFetchMu sync.Mutex
	storeSlot := func(slot uint64) {
		slotsToFetchMu.Lock()
		slotsToFetch[slot] = struct{}{}
		slotsToFetchMu.Unlock()
	}
	for i, address := range addresses {
		slotsForAddressJobs[i] = newGetSlotsForAddress(c.client, c.workers, storeSlot, address, fromSlot, toSlot)
		err := c.workers.Do(ctx, slotsForAddressJobs[i])
		if err != nil {
			return nil, fmt.Errorf("could not shedule job to fetch slots for address: %w", err)
		}
	}

	for _, job := range slotsForAddressJobs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-job.Done():
		}
	}

	// it should be safe to access slotsToFetch without lock as all the jobs signalled that they are done
	result := make([]uint64, 0, len(slotsToFetch))
	for slot := range slotsToFetch {
		result = append(result, slot)
	}

	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func (c *EncodedLogCollector) scheduleBlocksFetching(ctx context.Context, slots []uint64) (<-chan Block, error) {
	blocks := make(chan Block)
	getBlocksJobs := make([]*getBlockJob, len(slots))
	for i, slot := range slots {
		getBlocksJobs[i] = newGetBlockJob(c.client, blocks, slot)
		err := c.workers.Do(ctx, getBlocksJobs[i])
		if err != nil {
			return nil, fmt.Errorf("could not schedule job to fetch blocks for slot: %w", err)
		}
	}

	go func() {
		for _, job := range getBlocksJobs {
			select {
			case <-ctx.Done():
				return
			case <-job.Done():
				continue
			}
		}
		close(blocks)
	}()

	return blocks, nil
}

func (c *EncodedLogCollector) BackfillForAddresses(ctx context.Context, addresses []PublicKey, fromSlot, toSlot uint64) (orderedBlocks <-chan Block, cleanUp func(), err error) {
	slotsToFetch, err := c.getSlotsToFetch(ctx, addresses, fromSlot, toSlot)
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to identify slots to fetch: %w", err)
	}

	c.lggr.Debugw("Got all slots that need fetching for backfill operations", "addresses", PublicKeysToString(addresses), "fromSlot", fromSlot, "toSlot", toSlot, "slotsToFetch", slotsToFetch)

	unorderedBlocks, err := c.scheduleBlocksFetching(ctx, slotsToFetch)
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to schedule blocks to fetch: %w", err)
	}
	blocksSorter, sortedBlocks := newBlocksSorter(unorderedBlocks, c.lggr, slotsToFetch)
	err = blocksSorter.Start(ctx)
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to start blocks sorter: %w", err)
	}

	cleanUp = func() {
		err := blocksSorter.Close()
		if err != nil {
			blocksSorter.lggr.Errorw("Failed to close blocks sorter", "err", err)
		}
	}

	return sortedBlocks, cleanUp, nil
}

func (c *EncodedLogCollector) start(_ context.Context) error {
	//c.engine.Go(c.runSlotPolling)
	//c.engine.Go(c.runJobProcessing)

	return nil
}

func (c *EncodedLogCollector) close() error {
	return nil
}

func (c *EncodedLogCollector) runSlotPolling(ctx context.Context) {
	for {
		// TODO: fetch slots using getBlocksWithLimit RPC Method
		timer := time.NewTimer(DefaultNextSlotPollingInterval)

		select {
		case <-ctx.Done():
			timer.Stop()

			return
		case <-timer.C:
			ctxB, cancel := context.WithTimeout(ctx, c.rpcTimeLimit)

			// not to be run as a job, but as a blocking call
			result, err := c.client.GetLatestBlockhash(ctxB, rpc.CommitmentFinalized)
			if err != nil {
				c.lggr.Error("failed to get latest blockhash", "err", err)
				cancel()

				continue
			}

			cancel()

			slot := result.Context.Slot
			// if the slot is not higher than the highest slot, skip it
			if c.lastSentSlot.Load() >= slot {
				continue
			}

			select {
			case c.chSlot <- slot:
				c.lggr.Debugw("Fetched new slot and sent it for processing", "slot", slot)
				c.lastSentSlot.Store(slot)
			default:
			}
		}

		timer.Stop()
	}
}

//func (c *EncodedLogCollector) runSlotProcessing(ctx context.Context) {
//	start := uint64(0)
//	for {
//		select {
//		case <-ctx.Done():
//			return
//		case end := <-c.chSlot:
//			if start >= end {
//				continue
//			}
//
//			if start == 0 {
//				start = end // TODO: should be loaded from db or passed into EncodedLogCollector as arg
//			}
//			// load blocks in slot range
//			if err := c.loadSlotBlocksRange(ctx, start, end); err != nil {
//				// a retry will happen anyway on the next round of slots
//				// so the error is handled by doing nothing
//				c.lggr.Errorw("failed to load slot blocks range", "start", start, "end", end, "err", err)
//				continue
//			}
//
//			start = end + 1
//		}
//	}
//}

//func (c *EncodedLogCollector) runBlockProcessing(ctx context.Context) {
//	for {
//		select {
//		case <-ctx.Done():
//			return
//		case slot := <-c.chBlock:
//			if err := c.workers.Do(ctx, &getBlockJob{
//				slotNumber: slot,
//				client:     c.client,
//				parser:     c.ordered,
//				chJobs:     c.chJobs,
//			}); err != nil {
//				c.lggr.Errorf("failed to add job to queue: %s", err)
//			}
//		}
//	}
//}

func (c *EncodedLogCollector) runJobProcessing(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-c.chJobs:
			if err := c.workers.Do(ctx, job); err != nil {
				c.lggr.Errorf("failed to add job to queue: %s", err)
			}
		}
	}
}

//func (c *EncodedLogCollector) loadSlotBlocksRange(ctx context.Context, start, end uint64) error {
//	if start >= end {
//		return errors.New("the start block must come before the end block")
//	}
//
//	var (
//		result rpc.BlocksResult
//		err    error
//	)
//
//	rpcCtx, cancel := context.WithTimeout(ctx, c.rpcTimeLimit)
//	defer cancel()
//
//	if result, err = c.client.GetBlocks(rpcCtx, start, &end, rpc.CommitmentFinalized); err != nil {
//		return err
//	}
//
//	c.lggr.Debugw("loaded blocks for slots range", "start", start, "end", end, "blocks", len(result))
//
//	// as a safety mechanism, order the blocks ascending (oldest to newest) in the extreme case
//	// that the RPC changes and results get jumbled.
//	slices.SortFunc(result, func(a, b uint64) int {
//		if a < b {
//			return -1
//		} else if a > b {
//			return 1
//		}
//
//		return 0
//	})
//
//	for _, block := range result {
//		c.ordered.ExpectBlock(block)
//
//		select {
//		case <-ctx.Done():
//			return nil
//		case c.chBlock <- block:
//		}
//	}
//
//	return nil
//}

type unorderedParser struct {
	parser ProgramEventProcessor
}

func newUnorderedParser(parser ProgramEventProcessor) *unorderedParser {
	return &unorderedParser{parser: parser}
}

func (p *unorderedParser) ExpectBlock(_ uint64)      {}
func (p *unorderedParser) ExpectTxs(_ uint64, _ int) {}
func (p *unorderedParser) Process(block Block) error {
	return p.parser.Process(block)
}
