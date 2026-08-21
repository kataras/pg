package pg

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
)

// defaultMaxAttempts, defaultBaseDelay and defaultMaxDelay are the RetryOptions defaults
// documented on that type's fields, applied by normalizeRetryOptions for any zero field.
const (
	defaultMaxAttempts = 3
	defaultBaseDelay   = 50 * time.Millisecond
	defaultMaxDelay    = time.Second
)

// maxBackoffDuration is the largest time.Duration value (time.Duration is an int64 count of
// nanoseconds), used by backoffDelay as the saturating clamp for a left shift that would
// otherwise overflow.
const maxBackoffDuration = time.Duration(math.MaxInt64)

// RetryOptions configures InTransactionRetry (and its Repository counterpart). The zero
// value applies the documented defaults: MaxAttempts 3, BaseDelay 50ms, MaxDelay 1s, no
// TxOptions (the same zero pgx.TxOptions DB.Begin uses today) and IsErrRetryableTx as the
// classifier.
type RetryOptions struct {
	// MaxAttempts is the total number of attempts, including the first, before
	// InTransactionRetry gives up and returns the last attempt's error. Zero uses the
	// default of 3. A value less than 1 that is not zero (e.g. a caller-computed -1) is
	// treated as 1 (a single attempt, no retries) rather than falling back to the
	// default, so an explicit "don't retry" request is honored instead of silently
	// upgraded to 3 attempts.
	MaxAttempts int
	// BaseDelay is the initial backoff bound: the delay before the second attempt is chosen
	// uniformly at random between 0 and BaseDelay ("full jitter"). Zero uses the default of
	// 50ms.
	BaseDelay time.Duration
	// MaxDelay caps how large the backoff bound is allowed to grow as attempts continue to
	// fail; the bound doubles each attempt (BaseDelay << (attempt-1)) until it reaches
	// MaxDelay. Zero uses the default of 1s.
	MaxDelay time.Duration
	// TxOptions are the per-attempt transaction options, e.g. IsoLevel: pgx.Serializable,
	// which is normally what you want when retrying on serialization_failure/
	// deadlock_detected, since the default pgx.TxOptions{} zero value applies pgx's/
	// PostgreSQL's default isolation level (read committed), under which SQLSTATE 40001
	// mostly cannot happen in the first place. The same TxOptions are reused, unchanged, for
	// every attempt.
	TxOptions pgx.TxOptions
	// IsRetryable classifies whether an error returned by an attempt (including one that
	// surfaced at COMMIT) should be retried. nil means IsErrRetryableTx. A non-nil
	// IsRetryable completely replaces IsErrRetryableTx (it is not combined with it), so a
	// caller who wants to retry on additional conditions as well as the two transient
	// SQLSTATEs must call IsErrRetryableTx itself from within their own function.
	IsRetryable func(error) bool
}

// normalizeRetryOptions returns a copy of opts with every zero field replaced by its
// documented default, and MaxAttempts clamped to at least 1 (see the MaxAttempts field doc
// for the distinction between "zero" and "negative").
func normalizeRetryOptions(opts RetryOptions) RetryOptions {
	switch {
	case opts.MaxAttempts == 0:
		opts.MaxAttempts = defaultMaxAttempts
	case opts.MaxAttempts < 1:
		opts.MaxAttempts = 1
	}

	if opts.BaseDelay <= 0 {
		opts.BaseDelay = defaultBaseDelay
	}

	if opts.MaxDelay <= 0 {
		opts.MaxDelay = defaultMaxDelay
	}

	return opts
}

// backoffDelay computes the "full jitter" backoff wait before retrying the given attempt
// number (attempt is the attempt that just failed; 1 for the first attempt, 2 for the
// second, and so on), following the algorithm from
// https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/:
//
//	delay = rand.N(min(opts.MaxDelay, opts.BaseDelay << (attempt-1)))
//
// opts must already be normalized (normalizeRetryOptions), so BaseDelay and MaxDelay are
// both guaranteed positive here.
//
// The left shift is guarded against overflowing time.Duration's underlying int64: once
// BaseDelay<<(attempt-1) would exceed maxBackoffDuration, the shift is skipped in favor of
// that saturating clamp (which min() below then immediately reduces to opts.MaxDelay
// anyway), rather than letting the shift silently wrap around to a small or negative
// number, which, left unguarded, could make backoffDelay return an incorrect near-zero (or
// even negative, which rand.N would panic on) delay after enough attempts.
func backoffDelay(opts RetryOptions, attempt int) time.Duration {
	bound := opts.BaseDelay

	if shift := attempt - 1; shift > 0 {
		if shift >= 63 || bound > maxBackoffDuration>>shift {
			bound = maxBackoffDuration
		} else {
			bound <<= shift
		}
	}

	bound = min(bound, opts.MaxDelay)
	if bound <= 0 {
		// Only reachable if a caller hand-built a RetryOptions bypassing
		// normalizeRetryOptions with a non-positive MaxDelay; treat it as "no wait"
		// rather than letting rand.N panic on a non-positive bound.
		return 0
	}

	return rand.N(bound)
}

// waitBackoff blocks for backoffDelay(opts, attempt), or returns ctx.Err() promptly if ctx
// is done first.
func waitBackoff(ctx context.Context, opts RetryOptions, attempt int) error {
	return waitDelay(ctx, backoffDelay(opts, attempt))
}

// waitDelay blocks for delay, or returns ctx.Err() promptly if ctx is done first.
//
// It is split out of waitBackoff so that the wait itself can be tested with a delay the test
// chooses. backoffDelay draws full jitter from [0, bound), so a test that goes through
// waitBackoff cannot know how long the wait it is trying to interrupt will actually be.
//
// The context is checked up front, before the delay is consulted and regardless of it. A zero
// delay is a routine draw from that range rather than a rare one, and returning nil for it
// would let retryLoop begin another attempt against a context the caller has already canceled.
func waitDelay(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// retryLoop drives the attempt/backoff/retry bookkeeping shared by DB.InTransactionRetry and
// Repository.InTransactionRetry, independent of any transaction/database concern: attemptFn
// is called up to opts.MaxAttempts times (opts must already be normalized), waiting via
// waitBackoff between attempts as long as the most recent error is both non-nil and
// classified as retryable by isRetryable. It is deliberately free of any *DB/pgx dependency
// so it can be (and is, see retry_test.go) exercised directly by unit tests with a stub
// attemptFn, instead of requiring a live PostgreSQL server to test attempt counting, default
// application or backoff/cancellation behavior.
//
//   - attemptFn returning nil stops the loop immediately and retryLoop returns nil.
//   - attemptFn returning a non-nil error that isRetryable rejects, or on the final attempt,
//     stops the loop immediately and retryLoop returns that error as-is.
//   - Otherwise retryLoop waits out the backoff for this attempt before looping. If ctx is
//     canceled/expires during that wait, retryLoop returns immediately with
//     errors.Join(ctx.Err(), lastErr) (both inspectable via errors.Is/errors.As) instead of
//     continuing to retry against a context the caller no longer wants to wait on.
func retryLoop(ctx context.Context, opts RetryOptions, isRetryable func(error) bool, attemptFn func(attempt int) error) error {
	var lastErr error

	for attempt := 1; attempt <= opts.MaxAttempts; attempt++ {
		lastErr = attemptFn(attempt)
		if lastErr == nil {
			return nil
		}

		if attempt == opts.MaxAttempts || !isRetryable(lastErr) {
			return lastErr
		}

		if err := waitBackoff(ctx, opts, attempt); err != nil {
			return errors.Join(err, lastErr)
		}
	}

	return lastErr // unreachable (the loop above always returns), kept for an exhaustive/obviously-correct control flow.
}

// beginTx is the pgx.TxOptions-aware counterpart of Begin: same nested-transaction behavior
// (db.tx.Begin when db is itself already inside a transaction, so a nested beginTx joins the
// same underlying connection instead of trying to open a second one), but honors opts on the
// outermost Begin call instead of Begin's hardcoded zero pgx.TxOptions{}. Begin itself is
// unchanged and still the right choice for every caller that doesn't need non-default
// TxOptions; beginTx exists only because InTransactionRetry's per-attempt runner
// (runInTransactionOnce, below) needs to honor RetryOptions.TxOptions (e.g.
// IsoLevel: pgx.Serializable) on every attempt, which Begin has no way to accept.
func (db *DB) beginTx(ctx context.Context, opts pgx.TxOptions) (*DB, error) {
	var (
		tx  pgx.Tx
		err error
	)
	if db.tx != nil {
		tx, err = db.tx.Begin(ctx)
	} else {
		tx, err = db.Pool.BeginTx(ctx, opts)
	}
	if err != nil {
		return nil, err
	}

	txDB := db.clone(tx)
	return txDB, nil
}

// runInTransactionOnce runs a single transaction attempt: begin (with opts), call fn, then
// commit or roll back depending on fn's result: the exact same commit/rollback/panic
// bookkeeping DB.InTransaction performs, reimplemented here (rather than calling
// InTransaction) only because InTransaction always begins with a zero pgx.TxOptions{} via
// Begin, and RetryOptions.TxOptions needs to reach BeginTx on every attempt.
//
// Like InTransaction, this uses a NAMED error return specifically so the deferred func can
// overwrite it with the COMMIT error: transient serialization/deadlock failures are commonly
// only detected by PostgreSQL at COMMIT time (not on the earlier statements inside fn), so a
// version of this that returned fn's error unconditionally (discarding whatever
// tx.Commit(ctx) itself returned) would silently tell the caller (and, for
// InTransactionRetry, the retry loop) that the attempt succeeded when nothing was actually
// persisted and the failure was never classified/retried at all. This is the same class of
// bug an earlier task fixed in DB.InTransaction.
func (db *DB) runInTransactionOnce(ctx context.Context, opts pgx.TxOptions, fn func(*DB) error) (err error) {
	tx, err := db.beginTx(ctx, opts)
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p) // re-throw panic after rollback.
		} else if err != nil {
			if errors.Is(err, ErrIntentionalRollback) {
				err = tx.Rollback(ctx)
				return
			}

			rollbackErr := tx.Rollback(ctx)
			if rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
		} else {
			err = tx.Commit(ctx)
		}
	}()

	err = fn(tx)
	return err
}

// InTransactionRetry runs fn in a transaction and retries it with exponential backoff and
// full jitter when it fails with a transient SQLSTATE, 40001 serialization_failure or
// 40P01 deadlock_detected by default (see RetryOptions.IsRetryable to customize), including
// failures that only surface when the transaction commits rather than on one of fn's own
// statements (see runInTransactionOnce's doc for why that case needs care). Every attempt
// runs in its own, brand new transaction: nothing from a failed attempt (including anything
// fn wrote to variables it closes over, if fn is not idempotent about that) carries over to
// the next one except whatever fn itself re-derives by reading the database again, so fn
// should re-read whatever it needs on each call rather than assuming it only runs once.
//
//   - If db is already inside a transaction (db.IsTransaction()), InTransactionRetry runs fn
//     exactly once, with no retry, and returns its result as-is. This is the same short-circuit
//     InTransaction itself applies for nested calls, extended to a firm rule here: a
//     transaction that is already a subtransaction of some other, outer transaction cannot
//     be retried in isolation (rolling back and reopening it would still leave the outer
//     transaction, and whatever it already did, exactly where it was; there is no
//     "sub-BEGIN" to redo), so retrying is not merely skipped as an optimization here, it
//     would be wrong to attempt. Nest InTransactionRetry calls only for this short-circuit;
//     to get real retries, call it on a *DB that is not already in a transaction.
//   - Otherwise, opts is normalized (normalizeRetryOptions) and the (up to) MaxAttempts
//     attempts each run in a fresh transaction via runInTransactionOnce, backing off between
//     attempts per RetryOptions.BaseDelay/MaxDelay: see retryLoop's doc for the exact
//     stop/retry/backoff rules, all of which apply here unchanged with isRetryable being
//     opts.IsRetryable (or IsErrRetryableTx if that is nil).
//   - fn returning ErrIntentionalRollback is never retried: exactly as for InTransaction,
//     the transaction is rolled back and InTransactionRetry returns nil on a successful
//     rollback (or the rollback error otherwise). Since runInTransactionOnce reuses
//     InTransaction's own ErrIntentionalRollback handling, this "resolves" to nil (or a
//     rollback error, which is essentially never one of the two retryable SQLSTATEs) before
//     retryLoop's retry check ever sees ErrIntentionalRollback itself.
//   - If ctx is canceled/expires while waiting out a backoff between attempts, this returns
//     promptly with errors.Join(ctx.Err(), lastAttemptErr) instead of continuing to retry.
//
// Server-side cursors are intentionally not part of this API: see SelectIter's doc (in
// repository_iter.go) for why, and for the DECLARE CURSOR/FETCH pattern to reach for when
// you need one; it composes with a plain InTransaction/InTransactionRetry call the same way.
func (db *DB) InTransactionRetry(ctx context.Context, opts RetryOptions, fn func(*DB) error) error {
	if db.IsTransaction() {
		return fn(db)
	}

	opts = normalizeRetryOptions(opts)

	isRetryable := opts.IsRetryable
	if isRetryable == nil {
		isRetryable = IsErrRetryableTx
	}

	return retryLoop(ctx, opts, isRetryable, func(int) error {
		return db.runInTransactionOnce(ctx, opts.TxOptions, fn)
	})
}

// InTransactionRetry is the Repository counterpart of DB.InTransactionRetry: same retry,
// backoff and nested-transaction (run-once, no retry) semantics, but fn is called with a
// *Repository[T] wrapping the transactional *DB on each attempt (matching how
// Repository.InTransaction wraps DB.InTransaction) instead of a bare *DB.
//
// InTransactionRetry does not gate on repo.IsReadOnly the way the write-capable
// Insert/Update/Delete family does: fn may be a read-only, repeatable-read-sensitive
// operation just as easily as a write (SQLSTATE 40001 is raised for read-write conflicts
// under SERIALIZABLE isolation, not only for writes), so gating here would incorrectly
// block a legitimate read-only retry use case.
func (repo *Repository[T]) InTransactionRetry(ctx context.Context, opts RetryOptions, fn func(*Repository[T]) error) error {
	if repo.db.IsTransaction() {
		return fn(repo)
	}

	return repo.db.InTransactionRetry(ctx, opts, func(db *DB) error {
		txRepo := &Repository[T]{
			db: db,
			td: repo.td,
		}

		return fn(txRepo)
	})
}
