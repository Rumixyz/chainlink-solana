package txm

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	solanaGo "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/google/uuid"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	commontypes "github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/utils"
	bigmath "github.com/smartcontractkit/chainlink-common/pkg/utils/big_math"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/mathutil"

	"github.com/smartcontractkit/chainlink-solana/pkg/solana/client"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/config"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/fees"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/internal"
)

const (
	MaxQueueLen                    = 1000
	MaxRetryTimeMs                 = 250              // max tx retry time (exponential retry will taper to retry every 0.25s)
	MaxSigsToConfirm               = 256              // max number of signatures in GetSignatureStatus call
	EstimateComputeUnitLimitBuffer = 10               // percent buffer added on top of estimated compute unit limits to account for any variance
	TxReapInterval                 = 10 * time.Second // interval of time between reaping transactions that have met the retention threshold
	MaxComputeUnitLimit            = 1_400_000        // max compute unit limit a transaction can have
)

var _ services.Service = (*Txm)(nil)

type SimpleKeystore interface {
	Sign(ctx context.Context, account string, data []byte) (signature []byte, err error)
	Accounts(ctx context.Context) (accounts []string, err error)
}

var _ loop.Keystore = (SimpleKeystore)(nil)

// Txm manages transactions for the solana blockchain.
// simple implementation with no persistently stored txs
type Txm struct {
	services.StateMachine
	lggr   logger.Logger
	chSend chan PendingTx
	chSim  chan PendingTx
	chStop services.StopChan
	done   sync.WaitGroup
	cfg    config.Config
	txs    PendingTxContext
	ks     SimpleKeystore
	client internal.Loader[client.ReaderWriter]
	fee    fees.Estimator
	// sendTx is an override for sending transactions rather than using a single client
	// Enabling MultiNode uses this function to send transactions to all RPCs
	sendTx func(ctx context.Context, tx *solanaGo.Transaction) (solanaGo.Signature, error)
}

type TxConfig struct {
	Timeout time.Duration // transaction broadcast timeout

	// compute unit price config
	FeeBumpPeriod        time.Duration // how often to bump fee
	BaseComputeUnitPrice uint64        // starting price
	ComputeUnitPriceMin  uint64        // min price
	ComputeUnitPriceMax  uint64        // max price

	EstimateComputeUnitLimit bool   // enable compute limit estimations using simulation
	ComputeUnitLimit         uint32 // compute unit limit
}

// NewTxm creates a txm. Uses simulation so should only be used to send txes to trusted contracts i.e. OCR.
func NewTxm(chainID string, client internal.Loader[client.ReaderWriter],
	sendTx func(ctx context.Context, tx *solanaGo.Transaction) (solanaGo.Signature, error),
	cfg config.Config, ks SimpleKeystore, lggr logger.Logger) *Txm {
	if sendTx == nil {
		// default sendTx using a single RPC
		sendTx = func(ctx context.Context, tx *solanaGo.Transaction) (solanaGo.Signature, error) {
			c, err := client.Get()
			if err != nil {
				return solanaGo.Signature{}, err
			}
			return c.SendTx(ctx, tx)
		}
	}

	return &Txm{
		lggr:   logger.Named(lggr, "Txm"),
		chSend: make(chan PendingTx, MaxQueueLen), // queue can support 1000 pending txs
		chSim:  make(chan PendingTx, MaxQueueLen), // queue can support 1000 pending txs
		chStop: make(chan struct{}),
		cfg:    cfg,
		txs:    newPendingTxContextWithProm(chainID),
		ks:     ks,
		client: client,
		sendTx: sendTx,
	}
}

// Start subscribes to queuing channel and processes them.
func (txm *Txm) Start(ctx context.Context) error {
	return txm.StartOnce("Txm", func() error {
		// determine estimator type
		var estimator fees.Estimator
		var err error
		switch strings.ToLower(txm.cfg.FeeEstimatorMode()) {
		case "fixed":
			estimator, err = fees.NewFixedPriceEstimator(txm.cfg)
		case "blockhistory":
			estimator, err = fees.NewBlockHistoryEstimator(txm.client, txm.cfg, txm.lggr)
		default:
			err = fmt.Errorf("unknown solana fee estimator type: %s", txm.cfg.FeeEstimatorMode())
		}
		if err != nil {
			return err
		}
		txm.fee = estimator
		if err := txm.fee.Start(ctx); err != nil {
			return err
		}

		txm.done.Add(3) // waitgroup: tx retry, confirmer, simulator
		go txm.run()
		go txm.confirm()
		go txm.simulate()
		// Start reaping loop only if TxRetentionTimeout > 0
		// Otherwise, transactions are dropped immediately after finalization so the loop is not required
		if txm.cfg.TxRetentionTimeout() > 0 {
			txm.done.Add(1) // waitgroup: reaper
			go txm.reap()
		}

		return nil
	})
}

func (txm *Txm) run() {
	defer txm.done.Done()
	ctx, cancel := txm.chStop.NewCtx()
	defer cancel()

	for {
		select {
		case msg := <-txm.chSend:
			// process tx (pass tx copy)
			tx, id, sig, err := txm.sendWithRetry(ctx, msg)
			if err != nil {
				txm.lggr.Errorw("failed to send transaction", "error", err)
				txm.client.Reset() // clear client if tx fails immediately (potentially bad RPC)
				continue           // skip remainining
			}

			// send tx + signature to simulation queue
			msg.Tx = tx
			msg.signatures = append(msg.signatures, sig)
			msg.UUID = id
			select {
			case txm.chSim <- msg:
			default:
				txm.lggr.Warnw("failed to enqueue tx for simulation", "queueFull", len(txm.chSend) == MaxQueueLen, "tx", msg)
			}

			txm.lggr.Debugw("transaction sent", "signature", sig.String(), "id", id)
		case <-txm.chStop:
			return
		}
	}
}

// sendWithRetry attempts to send a transaction with exponential backoff retry logic.
// It prepares the transaction, builds and signs it, sends the initial transaction, and starts a retry routine with fee bumping if needed.
// The function returns the signed transaction, its ID, and the initial signature for use in simulation.
func (txm *Txm) sendWithRetry(ctx context.Context, msg PendingTx) (solanaGo.Transaction, string, solanaGo.Signature, error) {
	// Prepare transaction assigning blockhash and lastValidBlockHeight (for expiration tracking).
	// If required, it also performs balanceCheck and sets compute unit limit.
	if err := txm.prepareTransaction(ctx, &msg); err != nil {
		return solanaGo.Transaction{}, "", solanaGo.Signature{}, err
	}

	// Build and sign initial transaction setting compute unit price
	initTx, err := txm.buildTx(ctx, msg, 0)
	if err != nil {
		return solanaGo.Transaction{}, "", solanaGo.Signature{}, err
	}

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, msg.cfg.Timeout)

	// Send initial transaction
	sig, err := txm.sendInitialTx(ctx, initTx, msg, cancel)
	if err != nil {
		return solanaGo.Transaction{}, "", solanaGo.Signature{}, err
	}

	// Initialize signature list with initialTx signature. This list will be used to add new signatures and track retry attempts.
	sigs := &signatureList{}
	sigs.Allocate()
	if initSetErr := sigs.Set(0, sig); initSetErr != nil {
		return solanaGo.Transaction{}, "", solanaGo.Signature{}, fmt.Errorf("failed to save initial signature in signature list: %w", initSetErr)
	}

	// Start retry routine
	// pass in copy of msg (to build new tx with bumped fee) and broadcasted tx == initTx (to retry tx without bumping)
	txm.done.Add(1)
	go func() {
		defer txm.done.Done()
		txm.retryTx(ctx, msg, initTx, sigs)
	}()

	// Return signed tx, id, signature for use in simulation
	return initTx, msg.UUID, sig, nil
}

// prepareTransaction sets blockhash and lastValidBlockHeight which will be used to track expiration.
// If required, it also performs balanceCheck and sets compute unit limit.
func (txm *Txm) prepareTransaction(ctx context.Context, msg *PendingTx) error {
	client, err := txm.client.Get()
	if err != nil {
		return fmt.Errorf("failed to get client in sendWithRetry: %w", err)
	}

	// Assign blockhash
	blockhash, err := client.LatestBlockhash(ctx)
	if err != nil {
		return fmt.Errorf("failed to get blockhash: %w", err)
	}
	msg.Tx.Message.RecentBlockhash = blockhash.Value.Blockhash
	msg.lastValidBlockHeight = blockhash.Value.LastValidBlockHeight

	// Validate balance if required
	if msg.BalanceCheck {
		if err = solanaValidateBalance(ctx, client, msg.From, msg.Amount, msg.Tx.Message.ToBase64()); err != nil {
			return fmt.Errorf("failed to validate balance: %w", err)
		}
	}

	// Set compute unit limit
	if msg.cfg.ComputeUnitLimit != 0 {
		if err := fees.SetComputeUnitLimit(&msg.Tx, fees.ComputeUnitLimit(msg.cfg.ComputeUnitLimit)); err != nil {
			return fmt.Errorf("failed to add compute unit limit instruction: %w", err)
		}
	}

	return nil
}

func solanaValidateBalance(ctx context.Context, reader client.Reader, from solana.PublicKey, amount uint64, msg string) error {
	balance, err := reader.Balance(ctx, from)
	if err != nil {
		return err
	}

	fee, err := reader.GetFeeForMessage(ctx, msg)
	if err != nil {
		return err
	}

	if balance < (amount + fee) {
		return fmt.Errorf("balance %d is too low for this transaction to be executed: amount %d + fee %d", balance, amount, fee)
	}
	return nil
}

// buildTx builds and signs the transaction with the appropriate compute unit price.
func (txm *Txm) buildTx(ctx context.Context, msg PendingTx, retryCount int) (solanaGo.Transaction, error) {
	// work with a copy
	newTx := msg.Tx

	// Set compute unit price (fee)
	fee := fees.ComputeUnitPrice(
		fees.CalculateFee(
			msg.cfg.BaseComputeUnitPrice,
			msg.cfg.ComputeUnitPriceMax,
			msg.cfg.ComputeUnitPriceMin,
			uint(retryCount), //nolint:gosec // reasonable number of bumps should never cause overflow
		))
	if err := fees.SetComputeUnitPrice(&newTx, fee); err != nil {
		return solanaGo.Transaction{}, err
	}

	// Sign transaction
	// NOTE: fee payer account is index 0 account. https://github.com/gagliardetto/solana-go/blob/main/transaction.go#L252
	txMsg, err := newTx.Message.MarshalBinary()
	if err != nil {
		return solanaGo.Transaction{}, fmt.Errorf("error in MarshalBinary: %w", err)
	}
	sigBytes, err := txm.ks.Sign(ctx, msg.Tx.Message.AccountKeys[0].String(), txMsg)
	if err != nil {
		return solanaGo.Transaction{}, fmt.Errorf("error in Sign: %w", err)
	}
	var finalSig [64]byte
	copy(finalSig[:], sigBytes)
	newTx.Signatures = append(newTx.Signatures, finalSig)

	return newTx, nil
}

// sendInitialTx sends the initial tx and handles any errors that may occur. It also stores the transaction signature and cancellation function.
func (txm *Txm) sendInitialTx(ctx context.Context, initTx solanaGo.Transaction, msg PendingTx, cancel context.CancelFunc) (solanaGo.Signature, error) {
	// Send initial transaction
	sig, err := txm.sendTx(ctx, &initTx)
	if err != nil {
		// do not retry and exit early if fails
		cancel()
		txm.txs.OnError(sig, txm.cfg.TxRetentionTimeout(), TxFailReject) //nolint // no need to check error since only incrementing metric here
		return solanaGo.Signature{}, fmt.Errorf("tx failed initial transmit: %w", err)
	}

	// Store tx signature and cancel function
	if err := txm.txs.New(msg, sig, cancel); err != nil {
		cancel() // cancel context when exiting early
		return solanaGo.Signature{}, fmt.Errorf("failed to save tx signature (%s) to inflight txs: %w", sig, err)
	}

	txm.lggr.Debugw("tx initial broadcast", "id", msg.UUID, "fee", msg.cfg.BaseComputeUnitPrice, "signature", sig)
	return sig, nil
}

// retryTx contains the logic for retrying the transaction, including exponential backoff and fee bumping.
// Retries until context cancelled by timeout or called externally.
// It uses handleRetry helper function to handle each retry attempt.
func (txm *Txm) retryTx(ctx context.Context, msg PendingTx, currentTx solanaGo.Transaction, sigs *signatureList) {
	deltaT := 1 // initial delay in ms
	tick := time.After(0)
	bumpCount := 0
	bumpTime := time.Now()
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			// stop sending tx after retry tx ctx times out (does not stop confirmation polling for tx)
			wg.Wait()
			txm.lggr.Debugw("stopped tx retry", "id", msg.UUID, "signatures", sigs.List(), "err", context.Cause(ctx))
			return
		case <-tick:
			// Determine if we should bump the fee
			shouldBump := txm.shouldBumpFee(msg.cfg.FeeBumpPeriod, bumpTime)
			if shouldBump {
				bumpCount++
				bumpTime = time.Now()
				// Build new transaction with bumped fee and replace current tx
				var err error
				currentTx, err = txm.buildTx(ctx, msg, bumpCount)
				if err != nil {
					// Exit if unable to build transaction for retrying
					txm.lggr.Errorw("failed to build bumped retry tx", "error", err, "id", msg.UUID)
					return
				}
				// allocates space for new signature that will be introduced in handleRetry if needs bumping.
				index := sigs.Allocate()
				if index != bumpCount {
					txm.lggr.Errorw("invariant violation: index does not match bumpCount", "index", index, "bumpCount", bumpCount)
					return
				}
			}

			// Start a goroutine to handle the retry attempt
			// takes currentTx and rebroadcast. If needs bumping it will new signature to already allocated space in signatureList.
			wg.Add(1)
			go func(bump bool, count int, retryTx solanaGo.Transaction) {
				defer wg.Done()
				txm.handleRetry(ctx, msg, bump, count, retryTx, sigs)
			}(shouldBump, bumpCount, currentTx)
		}

		// Update the exponential backoff delay
		deltaT = txm.updateBackoffDelay(deltaT)
		tick = time.After(time.Duration(deltaT) * time.Millisecond)
	}
}

// shouldBumpFee determines whether the fee should be bumped based on the fee bump period.
func (txm *Txm) shouldBumpFee(feeBumpPeriod time.Duration, lastBumpTime time.Time) bool {
	return feeBumpPeriod != 0 && time.Since(lastBumpTime) > feeBumpPeriod
}

// updateBackoffDelay updates the exponential backoff delay up to a maximum limit.
func (txm *Txm) updateBackoffDelay(currentDelay int) int {
	newDelay := currentDelay * 2
	if newDelay > MaxRetryTimeMs {
		return MaxRetryTimeMs
	}
	return newDelay
}

// handleRetry handles the logic for each retry attempt, including sending the transaction, updating signatures, and logging.
func (txm *Txm) handleRetry(ctx context.Context, msg PendingTx, bump bool, count int, retryTx solanaGo.Transaction, sigs *signatureList) {
	// send retry transaction
	retrySig, err := txm.sendTx(ctx, &retryTx)
	if err != nil {
		// this could occur if endpoint goes down or if ctx cancelled
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			txm.lggr.Debugw("ctx error on send retry transaction", "error", err, "signatures", sigs.List(), "id", msg.UUID)
		} else {
			txm.lggr.Warnw("failed to send retry transaction", "error", err, "signatures", sigs.List(), "id", msg.UUID)
		}
		return
	}

	// if bump is true, update signature list and set new signature in space already allocated.
	if bump {
		if err := txm.txs.AddSignature(msg.UUID, retrySig); err != nil {
			txm.lggr.Warnw("error in adding retry transaction", "error", err, "id", msg.UUID)
			return
		}
		if err := sigs.Set(count, retrySig); err != nil {
			// this should never happen
			txm.lggr.Errorw("INVARIANT VIOLATION: failed to set signature", "error", err, "id", msg.UUID)
			return
		}
		txm.lggr.Debugw("tx rebroadcast with bumped fee", "id", msg.UUID, "retryCount", count, "fee", msg.cfg.BaseComputeUnitPrice, "signatures", sigs.List())
	}

	// prevent locking on waitgroup when ctx is closed
	wait := make(chan struct{})
	go func() {
		defer close(wait)
		sigs.Wait(count) // wait until bump tx has set the tx signature to compare rebroadcast signatures
	}()
	select {
	case <-ctx.Done():
		return
	case <-wait:
	}

	// this should never happen (should match the signature saved to sigs)
	if fetchedSig, err := sigs.Get(count); err != nil || retrySig != fetchedSig {
		txm.lggr.Errorw("original signature does not match retry signature", "expectedSignatures", sigs.List(), "receivedSignature", retrySig, "error", err)
	}
}

// goroutine that polls to confirm implementation
// cancels the exponential retry once confirmed
func (txm *Txm) confirm() {
	defer txm.done.Done()
	ctx, cancel := txm.chStop.NewCtx()
	defer cancel()

	tick := time.After(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			client, err := txm.client.Get()
			// Get list of transaction signatures to confirm
			// If no signatures to confirm, we can break loop.
			sigs := txm.txs.ListAll()
			if len(sigs) == 0 {
				break
			}

			if err != nil {
				txm.lggr.Errorw("failed to get client in txm.confirm", "error", err)
				return
			}
			if txm.cfg.TxExpirationRebroadcast() {
				txm.rebroadcastExpiredTxs(ctx, client)
			}
			txm.processConfirmations(ctx, client, sigs)
		}
		tick = time.After(utils.WithJitter(txm.cfg.ConfirmPollPeriod()))
	}
}

func (txm *Txm) processConfirmations(ctx context.Context, client client.ReaderWriter, sigs []solanaGo.Signature) {
	// batch sigs no more than MaxSigsToConfirm each
	sigsBatch, err := utils.BatchSplit(sigs, MaxSigsToConfirm)
	if err != nil { // this should never happen
		txm.lggr.Fatalw("failed to batch signatures", "error", err)
		return
	}

	var wg sync.WaitGroup
	for i := 0; i < len(sigsBatch); i++ {
		// fetch signature statuses
		statuses, err := client.SignatureStatuses(ctx, sigsBatch[i])
		if err != nil {
			txm.lggr.Errorw("failed to get signature statuses in txm.confirm", "error", err)
			break // exit for loop
		}

		wg.Add(1)
		// nonblocking: process batches as soon as they come in
		go func(index int) {
			defer wg.Done()
			txm.processSignatureStatuses(sigsBatch[i], statuses)
		}(i)
	}
	wg.Wait() // wait for processing to finish
}

func (txm *Txm) rebroadcastExpiredTxs(ctx context.Context, client client.ReaderWriter) {
	// Get current slot height to check if txes have expired when compared against their lastValidBlockHeight
	currHeight, err := client.SlotHeight(ctx)
	if err != nil {
		txm.lggr.Errorw("failed to get current slot height", "error", err)
		return
	}

	// Rebroadcast all expired txes
	for _, tx := range txm.txs.ListAllExpiredBroadcastedTxs(currHeight) {
		txm.lggr.Infow("transaction expired, rebroadcasting", "id", tx.UUID, "signature", tx.signatures)
		if len(tx.signatures) == 0 { // prevent panic, shouldn't happen.
			txm.lggr.Errorw("no signatures found for expired transaction", "id", tx.UUID)
			continue
		}
		_, err := txm.txs.Remove(tx.signatures[0]) // only picking signature[0] because remove func removes all remaining signatures.
		if err != nil {
			txm.lggr.Errorw("failed to remove expired transaction", "id", tx.UUID, "error", err)
			continue
		}

		rebroadcastTx := &PendingTx{
			Tx:           tx.Tx,
			UUID:         tx.UUID, // same id to handle case where set by caller. Previous ID has already been removed.
			BalanceCheck: tx.BalanceCheck,
			Amount:       tx.Amount,
			From:         tx.From,
		}

		// Re-enqueue the transaction for rebroadcasting
		err = txm.Enqueue(ctx, rebroadcastTx)
		if err != nil {
			txm.lggr.Errorw("failed to enqueue rebroadcast transaction", "id", tx.UUID, "error", err)
			continue
		}

		txm.lggr.Infow("rebroadcast transaction enqueued", "id", tx.UUID)
	}
}

func (txm *Txm) processSignatureStatuses(sigs []solanaGo.Signature, res []*rpc.SignatureStatusesResult) {
	// Sort signatures and results process successful first
	sortedSigs, sortedRes, err := SortSignaturesAndResults(sigs, res)
	if err != nil {
		txm.lggr.Errorw("sorting error", "error", err)
		return
	}

	for i := 0; i < len(sortedRes); i++ {
		sig, status := sortedSigs[i], sortedRes[i]
		// if status is nil (sig not found), continue polling
		// sig not found could mean invalid tx or not picked up yet
		if status == nil {
			txm.handleNotFoundSignatureStatus(sig)
			continue
		}

		// if signature has an error, end polling
		if status.Err != nil {
			txm.handleErrorSignatureStatus(sig, status)
			continue
		}

		switch status.ConfirmationStatus {
		case rpc.ConfirmationStatusProcessed:
			// if signature is processed, keep polling for confirmed or finalized status
			txm.handleProcessedSignatureStatus(sig)
			continue
		case rpc.ConfirmationStatusConfirmed:
			// if signature is confirmed, keep polling for finalized status
			txm.handleConfirmedSignatureStatus(sig)
			continue
		case rpc.ConfirmationStatusFinalized:
			// if signature is finalized, end polling
			txm.handleFinalizedSignatureStatus(sig)
			continue
		default:
			txm.lggr.Warnw("unknown confirmation status", "signature", sig, "status", status.ConfirmationStatus)
			continue
		}
	}
}

func (txm *Txm) handleNotFoundSignatureStatus(sig solanaGo.Signature) {
	txm.lggr.Debugw("tx state: not found", "signature", sig)

	// check confirm timeout exceeded
	if txm.txs.Expired(sig, txm.cfg.TxConfirmTimeout()) {
		id, err := txm.txs.OnError(sig, txm.cfg.TxRetentionTimeout(), TxFailDrop)
		if err != nil {
			txm.lggr.Infow("failed to mark transaction as errored", "id", id, "signature", sig, "timeoutSeconds", txm.cfg.TxConfirmTimeout(), "error", err)
		} else {
			txm.lggr.Infow("failed to find transaction within confirm timeout", "id", id, "signature", sig, "timeoutSeconds", txm.cfg.TxConfirmTimeout())
		}
	}
}

func (txm *Txm) handleErrorSignatureStatus(sig solanaGo.Signature, status *rpc.SignatureStatusesResult) {
	id, err := txm.txs.OnError(sig, txm.cfg.TxRetentionTimeout(), TxFailRevert)
	if err != nil {
		txm.lggr.Infow("failed to mark transaction as errored", "id", id, "signature", sig, "error", err)
	} else {
		txm.lggr.Debugw("tx state: failed", "id", id, "signature", sig, "error", status.Err, "status", status.ConfirmationStatus)
	}
}

func (txm *Txm) handleProcessedSignatureStatus(sig solanaGo.Signature) {
	// update transaction state in local memory
	id, err := txm.txs.OnProcessed(sig)
	if err != nil && !errors.Is(err, ErrAlreadyInExpectedState) {
		txm.lggr.Errorw("failed to mark transaction as processed", "signature", sig, "error", err)
	} else if err == nil {
		txm.lggr.Debugw("marking transaction as processed", "id", id, "signature", sig)
	}
	// check confirm timeout exceeded if TxConfirmTimeout set
	if txm.cfg.TxConfirmTimeout() != 0*time.Second && txm.txs.Expired(sig, txm.cfg.TxConfirmTimeout()) {
		id, err := txm.txs.OnError(sig, txm.cfg.TxRetentionTimeout(), TxFailDrop)
		if err != nil {
			txm.lggr.Infow("failed to mark transaction as errored", "id", id, "signature", sig, "timeoutSeconds", txm.cfg.TxConfirmTimeout(), "error", err)
		} else {
			txm.lggr.Debugw("tx failed to move beyond 'processed' within confirm timeout", "id", id, "signature", sig, "timeoutSeconds", txm.cfg.TxConfirmTimeout())
		}
	}
}

func (txm *Txm) handleConfirmedSignatureStatus(sig solanaGo.Signature) {
	id, err := txm.txs.OnConfirmed(sig)
	if err != nil && !errors.Is(err, ErrAlreadyInExpectedState) {
		txm.lggr.Errorw("failed to mark transaction as confirmed", "id", id, "signature", sig, "error", err)
	} else if err == nil {
		txm.lggr.Debugw("marking transaction as confirmed", "id", id, "signature", sig)
	}
}

func (txm *Txm) handleFinalizedSignatureStatus(sig solanaGo.Signature) {
	id, err := txm.txs.OnFinalized(sig, txm.cfg.TxRetentionTimeout())
	if err != nil {
		txm.lggr.Errorw("failed to mark transaction as finalized", "id", id, "signature", sig, "error", err)
	} else {
		txm.lggr.Debugw("marking transaction as finalized", "id", id, "signature", sig)
	}
}

// goroutine that simulates tx (use a bounded number of goroutines to pick from queue?)
// simulate can cancel the send retry function early in the tx management process
// additionally, it can provide reasons for why a tx failed in the logs
func (txm *Txm) simulate() {
	defer txm.done.Done()
	ctx, cancel := txm.chStop.NewCtx()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-txm.chSim:
			res, err := txm.simulateTx(ctx, &msg.Tx)
			if err != nil {
				// this error can occur if endpoint goes down or if invalid signature (invalid signature should occur further upstream in sendWithRetry)
				// allow retry to continue in case temporary endpoint failure (if still invalid, confirmation or timeout will cleanup)
				txm.lggr.Debugw("failed to simulate tx", "id", msg.UUID, "signatures", msg.signatures, "error", err)
				continue
			}

			// continue if simulation does not return error continue
			if res.Err == nil {
				continue
			}

			// Transaction has to have a signature if simulation succeeded but added check for belt and braces approach
			if len(msg.signatures) > 0 {
				txm.processSimulationError(msg.UUID, msg.signatures[0], res)
			}
		}
	}
}

// reap is a goroutine that periodically checks whether finalized and errored transactions have reached
// their retention threshold and purges them from the in-memory storage if they have
func (txm *Txm) reap() {
	defer txm.done.Done()
	ctx, cancel := txm.chStop.NewCtx()
	defer cancel()

	tick := time.After(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			reapCount := txm.txs.TrimFinalizedErroredTxs()
			if reapCount > 0 {
				txm.lggr.Debugf("Reaped %d finalized or errored transactions", reapCount)
			}
		}
		tick = time.After(utils.WithJitter(TxReapInterval))
	}
}

// Enqueue enqueues a msg destined for the solana chain.
func (txm *Txm) Enqueue(ctx context.Context, msg *PendingTx, txCfgs ...SetTxConfig) error {
	if err := txm.Ready(); err != nil {
		return fmt.Errorf("error in soltxm.Enqueue: %w", err)
	}

	// validate msg and tx are not empty
	if msg == nil || isEmptyTransactionAccountKeys(msg.Tx) {
		return errors.New("error in soltxm.Enqueue: tx or account keys are empty")
	}

	// validate expected key exists by trying to sign with it
	// fee payer account is index 0 account
	// https://github.com/gagliardetto/solana-go/blob/main/transaction.go#L252
	_, err := txm.ks.Sign(ctx, msg.Tx.Message.AccountKeys[0].String(), nil)
	if err != nil {
		return fmt.Errorf("error in soltxm.Enqueue.GetKey: %w", err)
	}

	// apply changes to default config
	cfg := txm.defaultTxConfig()
	for _, v := range txCfgs {
		v(&cfg)
	}

	// Use transaction ID provided by caller if set
	id := uuid.New().String()
	if txID != nil && *txID != "" {
		id = *txID
	}

	// Perform compute unit limit estimation after storing transaction
	// If error found during simulation, transaction should be in storage to mark accordingly
	if cfg.EstimateComputeUnitLimit {
		computeUnitLimit, err := txm.EstimateComputeUnitLimit(ctx, &msg.Tx)
		if err != nil {
			return fmt.Errorf("transaction failed simulation: %w", err)
		}
		// If estimation returns 0 compute unit limit without error, fallback to original config
		if computeUnitLimit != 0 {
			cfg.ComputeUnitLimit = computeUnitLimit
		}
	}

	msg.cfg = cfg
	// If ID was not set by caller, create one.
	if msg.UUID == "" {
		msg.UUID = uuid.New().String()
	}

	select {
	case txm.chSend <- *msg:
	default:
		txm.lggr.Errorw("failed to enqeue tx", "queueFull", len(txm.chSend) == MaxQueueLen, "tx", msg)
		return fmt.Errorf("failed to enqueue transaction for %s", msg.AccountID)
	}
	return nil
}

// GetTransactionStatus translates internal TXM transaction statuses to chainlink common statuses
func (txm *Txm) GetTransactionStatus(ctx context.Context, transactionID string) (commontypes.TransactionStatus, error) {
	state, err := txm.txs.GetTxState(transactionID)
	if err != nil {
		return commontypes.Unknown, fmt.Errorf("failed to find transaction with id %s: %w", transactionID, err)
	}

	switch state {
	case Broadcasted:
		return commontypes.Pending, nil
	case Processed, Confirmed:
		return commontypes.Unconfirmed, nil
	case Finalized:
		return commontypes.Finalized, nil
	case Errored:
		return commontypes.Failed, nil
	case FatallyErrored:
		return commontypes.Fatal, nil
	default:
		return commontypes.Unknown, fmt.Errorf("found unknown transaction state: %s", state.String())
	}
}

// EstimateComputeUnitLimit estimates the compute unit limit needed for a transaction.
// It simulates the provided transaction to determine the used compute and applies a buffer to it.
func (txm *Txm) EstimateComputeUnitLimit(ctx context.Context, tx *solanaGo.Transaction, id string) (uint32, error) {
	txCopy := *tx

	// Set max compute unit limit when simulating a transaction to avoid getting an error for exceeding the default 200k compute unit limit
	if computeUnitLimitErr := fees.SetComputeUnitLimit(&txCopy, fees.ComputeUnitLimit(MaxComputeUnitLimit)); computeUnitLimitErr != nil {
		txm.lggr.Errorw("failed to set compute unit limit when simulating tx", "error", computeUnitLimitErr)
		return 0, computeUnitLimitErr
	}

	// Sign and set signature in tx copy for simulation
	txMsg, marshalErr := txCopy.Message.MarshalBinary()
	if marshalErr != nil {
		return 0, fmt.Errorf("failed to marshal tx message: %w", marshalErr)
	}
	sigBytes, signErr := txm.ks.Sign(ctx, txCopy.Message.AccountKeys[0].String(), txMsg)
	if signErr != nil {
		return 0, fmt.Errorf("failed to sign transaction: %w", signErr)
	}
	var sig [64]byte
	copy(sig[:], sigBytes)
	txCopy.Signatures = append(txCopy.Signatures, sig)

	res, err := txm.simulateTx(ctx, &txCopy)
	if err != nil {
		return 0, err
	}

	// Return error if response err is non-nil to avoid broadcasting a tx destined to fail
	if res.Err != nil {
		sig := solanaGo.Signature{}
		if len(txCopy.Signatures) > 0 {
			sig = txCopy.Signatures[0]
		}
		// Process error to determine the corresponding state and type.
		// Certain errors can be considered not to be failures during simulation to allow the process to continue
		if txState, errType := txm.processError(sig, res.Err, true); errType != NoFailure {
			err := txm.txs.OnPrebroadcastError(id, txm.cfg.TxRetentionTimeout(), txState, errType)
			if err != nil {
				return 0, fmt.Errorf("failed to process error %v for tx ID %s: %w", res.Err, id, err)
			}
		}
		return 0, fmt.Errorf("simulated tx returned error: %v", res.Err)
	}

	if res.UnitsConsumed == nil || *res.UnitsConsumed == 0 {
		txm.lggr.Debug("failed to get units consumed for tx")
		// Do not return error to allow falling back to default compute unit limit
		return 0, nil
	}

	unitsConsumed := *res.UnitsConsumed
	// Add buffer to the used compute estimate
	computeUnitLimit := bigmath.AddPercentage(new(big.Int).SetUint64(unitsConsumed), EstimateComputeUnitLimitBuffer).Uint64()
	// Ensure computeUnitLimit does not exceed the max compute unit limit for a transaction after adding buffer
	computeUnitLimit = mathutil.Min(computeUnitLimit, MaxComputeUnitLimit)

	return uint32(computeUnitLimit), nil //nolint // computeUnitLimit can only be a maximum of 1.4M
}

// simulateTx simulates transactions using the SimulateTx client method
func (txm *Txm) simulateTx(ctx context.Context, tx *solanaGo.Transaction) (res *rpc.SimulateTransactionResult, err error) {
	// get client
	client, err := txm.client.Get()
	if err != nil {
		txm.lggr.Errorw("failed to get client", "error", err)
		return
	}

	// Simulate with signature verification enabled since it can have an impact on the compute units used
	res, err = client.SimulateTx(ctx, tx, &rpc.SimulateTransactionOpts{SigVerify: true, Commitment: txm.cfg.Commitment()})
	if err != nil {
		// This error can occur if endpoint goes down or if invalid signature
		txm.lggr.Errorw("failed to simulate tx", "error", err)
		return
	}
	return
}

// processError parses and handles relevant errors found in simulation results
func (txm *Txm) processError(sig solanaGo.Signature, resErr interface{}, simulation bool) (txState TxState, errType TxErrType) {
	if resErr != nil {
		// handle various errors
		// https://github.com/solana-labs/solana/blob/master/sdk/src/transaction/error.rs
		errStr := fmt.Sprintf("%v", resErr) // convert to string to handle various interfaces
		txm.lggr.Info(errStr)
		logValues := []interface{}{
			"signature", sig,
			"error", resErr,
		}
		// return TxFailRevert on any error if when processing error during confirmation
		errType := TxFailRevert
		// return TxFailSimRevert on any known error when processing simulation error
		if simulation {
			errType = TxFailSimRevert
		}
		switch {
		// blockhash not found when simulating, occurs when network bank has not seen the given blockhash or tx is too old
		// let confirmation process clean up
		case strings.Contains(errStr, "BlockhashNotFound"):
			txm.lggr.Debugw("simulate: BlockhashNotFound", logValues...)
		// transaction will encounter execution error/revert, mark as reverted to remove from confirmation + retry
		case strings.Contains(errStr, "InstructionError"):
			_, err := txm.txs.OnError(sig, txm.cfg.TxRetentionTimeout(), TxFailSimRevert) // cancel retry
			if err != nil {
				logValues = append(logValues, "stateTransitionErr", err)
			}
			txm.lggr.Debugw("simulate: InstructionError", logValues...)
		// transaction is already processed in the chain, letting txm confirmation handle
		case strings.Contains(errStr, "AlreadyProcessed"):
			txm.lggr.Debugw("AlreadyProcessed", logValues...)
			// return no failure for this error when simulating in case there is a race between broadcast and simulation
			// when doing both in parallel
			if simulation {
				return txState, NoFailure
			}
			return Errored, errType
		// unrecognized errors (indicates more concerning failures)
		default:
			// if simulating, return TxFailSimOther if error unknown
			if simulation {
				errType = TxFailSimOther
			}
			txm.lggr.Errorw("unrecognized error", logValues...)
			return Errored, errType
		}
	}
	return
}

func (txm *Txm) InflightTxs() int {
	return len(txm.txs.ListAll())
}

// Close close service
func (txm *Txm) Close() error {
	return txm.StopOnce("Txm", func() error {
		close(txm.chStop)
		txm.done.Wait()
		return txm.fee.Close()
	})
}
func (txm *Txm) Name() string { return txm.lggr.Name() }

func (txm *Txm) HealthReport() map[string]error { return map[string]error{txm.Name(): txm.Healthy()} }

func (txm *Txm) defaultTxConfig() TxConfig {
	return TxConfig{
		Timeout:                  txm.cfg.TxRetryTimeout(),
		FeeBumpPeriod:            txm.cfg.FeeBumpPeriod(),
		BaseComputeUnitPrice:     txm.fee.BaseComputeUnitPrice(),
		ComputeUnitPriceMin:      txm.cfg.ComputeUnitPriceMin(),
		ComputeUnitPriceMax:      txm.cfg.ComputeUnitPriceMax(),
		ComputeUnitLimit:         txm.cfg.ComputeUnitLimitDefault(),
		EstimateComputeUnitLimit: txm.cfg.EstimateComputeUnitLimit(),
	}
}

// isEmptyTransactionAccountKeys validates that a solana tx and its account keys are not empty.
func isEmptyTransactionAccountKeys(tx solana.Transaction) bool {
	return len(tx.Signatures) == 0 && len(tx.Message.AccountKeys) == 0
}
