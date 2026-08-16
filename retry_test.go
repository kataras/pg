package pg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestIsErrRetryableTx exercises IsErrRetryableTx's SQLSTATE classification: the two
// documented transient codes (wrapped, as they normally reach it via errors.As: see wrap,
// defined in errors_test.go), an unrelated PgError code, a plain error and nil.
func TestIsErrRetryableTx(t *testing.T) {
	t.Run("40001 serialization_failure wrapped", func(t *testing.T) {
		pgErr := &pgconn.PgError{Severity: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update"}
		if !IsErrRetryableTx(wrap(pgErr)) {
			t.Fatal("expected SQLSTATE 40001 to be classified as retryable")
		}
	})

	t.Run("40P01 deadlock_detected wrapped", func(t *testing.T) {
		pgErr := &pgconn.PgError{Severity: "ERROR", Code: "40P01", Message: "deadlock detected"}
		if !IsErrRetryableTx(wrap(pgErr)) {
			t.Fatal("expected SQLSTATE 40P01 to be classified as retryable")
		}
	})

	t.Run("23505 unique_violation is not retryable", func(t *testing.T) {
		pgErr := &pgconn.PgError{Severity: "ERROR", Code: "23505", Message: "duplicate key value violates unique constraint"}
		if IsErrRetryableTx(wrap(pgErr)) {
			t.Fatal("expected SQLSTATE 23505 to NOT be classified as retryable")
		}
	})

	t.Run("plain error is not retryable", func(t *testing.T) {
		if IsErrRetryableTx(errors.New("boom")) {
			t.Fatal("expected a plain (non-PgError) error to NOT be classified as retryable")
		}
	})

	t.Run("nil error is not retryable", func(t *testing.T) {
		if IsErrRetryableTx(nil) {
			t.Fatal("expected nil to NOT be classified as retryable")
		}
	})
}

// TestNormalizeRetryOptionsDefaults verifies that every zero RetryOptions field is replaced
// by its documented default, that non-zero fields are left alone, and that MaxAttempts < 0 is
// clamped to 1 rather than upgraded to the default of 3 (see the MaxAttempts field doc for why
// those two cases ("unset" vs "explicitly invalid") are handled differently).
func TestNormalizeRetryOptionsDefaults(t *testing.T) {
	t.Run("zero value applies every default", func(t *testing.T) {
		got := normalizeRetryOptions(RetryOptions{})
		if got.MaxAttempts != defaultMaxAttempts {
			t.Fatalf("expected default MaxAttempts %d, got %d", defaultMaxAttempts, got.MaxAttempts)
		}
		if got.BaseDelay != defaultBaseDelay {
			t.Fatalf("expected default BaseDelay %v, got %v", defaultBaseDelay, got.BaseDelay)
		}
		if got.MaxDelay != defaultMaxDelay {
			t.Fatalf("expected default MaxDelay %v, got %v", defaultMaxDelay, got.MaxDelay)
		}
	})

	t.Run("negative MaxAttempts clamps to 1, not the default", func(t *testing.T) {
		got := normalizeRetryOptions(RetryOptions{MaxAttempts: -7})
		if got.MaxAttempts != 1 {
			t.Fatalf("expected MaxAttempts -7 to clamp to 1, got %d", got.MaxAttempts)
		}
	})

	t.Run("explicit non-zero fields are preserved", func(t *testing.T) {
		got := normalizeRetryOptions(RetryOptions{MaxAttempts: 9, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
		if got.MaxAttempts != 9 || got.BaseDelay != time.Millisecond || got.MaxDelay != 5*time.Millisecond {
			t.Fatalf("expected explicit fields to survive normalization untouched, got %#v", got)
		}
	})
}

// TestRetryLoopAttemptCounting drives retryLoop directly (bypassing any *DB/transaction) with
// a stub attemptFn that always fails and a stub IsRetryable that always says "retry", and
// verifies the default MaxAttempts of 3 is honored exactly: attemptFn is called 3 times, no
// more, and retryLoop returns the last attempt's error.
func TestRetryLoopAttemptCounting(t *testing.T) {
	opts := normalizeRetryOptions(RetryOptions{
		// Keep the test fast: still exercises the two backoff waits between the 3 attempts,
		// just with a small bound.
		BaseDelay: time.Millisecond,
		MaxDelay:  5 * time.Millisecond,
	})

	stubErr := errors.New("always fails")
	var calls int

	err := retryLoop(context.Background(), opts, func(error) bool { return true }, func(attempt int) error {
		calls++
		if attempt != calls {
			t.Fatalf("expected attemptFn to be called with attempt=%d on call #%d, got attempt=%d", calls, calls, attempt)
		}
		return stubErr
	})

	if calls != defaultMaxAttempts {
		t.Fatalf("expected %d attempts (the default MaxAttempts), got %d", defaultMaxAttempts, calls)
	}
	if !errors.Is(err, stubErr) {
		t.Fatalf("expected the last attempt's error to be returned, got: %v", err)
	}
}

// TestRetryLoopStopsWhenNotRetryable verifies that retryLoop stops after the first attempt
// (without consuming the rest of MaxAttempts, and without backing off) when isRetryable
// rejects the error.
func TestRetryLoopStopsWhenNotRetryable(t *testing.T) {
	opts := normalizeRetryOptions(RetryOptions{})
	stubErr := errors.New("not retryable")
	var calls int

	err := retryLoop(context.Background(), opts, func(error) bool { return false }, func(attempt int) error {
		calls++
		return stubErr
	})

	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt when isRetryable rejects the error, got %d", calls)
	}
	if !errors.Is(err, stubErr) {
		t.Fatalf("expected the non-retryable error to be returned as-is, got: %v", err)
	}
}

// TestRetryLoopSucceedsAfterRetry verifies the success path: attemptFn fails once (retryable)
// then succeeds, and retryLoop returns nil after exactly 2 calls.
func TestRetryLoopSucceedsAfterRetry(t *testing.T) {
	opts := normalizeRetryOptions(RetryOptions{BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	stubErr := errors.New("transient")
	var calls int

	err := retryLoop(context.Background(), opts, func(error) bool { return true }, func(attempt int) error {
		calls++
		if calls == 1 {
			return stubErr
		}
		return nil
	})

	if err != nil {
		t.Fatalf("expected nil after a successful retry, got: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected exactly 2 attempts (1 failure + 1 success), got %d", calls)
	}
}

// TestRetryLoopCtxCanceledMidBackoff verifies that a context canceled while retryLoop is
// waiting out a backoff makes it return promptly (well before the attempts it didn't get to
// run would otherwise have exhausted MaxAttempts) with an error that, via errors.Is, still
// exposes both ctx.Err() and the last attempt's error (errors.Join).
func TestRetryLoopCtxCanceledMidBackoff(t *testing.T) {
	opts := normalizeRetryOptions(RetryOptions{
		MaxAttempts: 5,
		// Long enough that, uncanceled, this test would take at least 200ms+400ms+...; the
		// cancellation below fires quickly, so a "prompt return" assertion of well under
		// that is a meaningful signal that retryLoop actually stopped waiting rather than
		// happening to finish its backoff before we checked.
		BaseDelay: 200 * time.Millisecond,
		MaxDelay:  time.Second,
	})

	stubErr := errors.New("still failing")
	ctx, cancel := context.WithCancel(context.Background())

	var calls int32
	go func() {
		time.Sleep(20 * time.Millisecond) // let the first attempt run and start backing off.
		cancel()
	}()

	start := time.Now()
	err := retryLoop(ctx, opts, func(error) bool { return true }, func(attempt int) error {
		calls++
		return stubErr
	})
	elapsed := time.Since(start)

	if elapsed > 150*time.Millisecond {
		t.Fatalf("expected retryLoop to return promptly after ctx cancellation, took %v", elapsed)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt before cancellation interrupted the backoff wait, got %d", calls)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the returned error to wrap context.Canceled, got: %v", err)
	}
	if !errors.Is(err, stubErr) {
		t.Fatalf("expected the returned error to also wrap the last attempt's error, got: %v", err)
	}
}

// TestBackoffDelayOverflowGuard verifies that backoffDelay's shift-overflow guard actually
// engages instead of wrapping into a garbage (possibly negative) duration: with a large
// attempt number, BaseDelay<<(attempt-1) would overflow int64 many times over without the
// guard, yet the returned delay must still land within [0, MaxDelay].
func TestBackoffDelayOverflowGuard(t *testing.T) {
	opts := normalizeRetryOptions(RetryOptions{BaseDelay: time.Second, MaxDelay: 3 * time.Second})

	for _, attempt := range []int{2, 10, 64, 1000} {
		got := backoffDelay(opts, attempt)
		if got < 0 || got > opts.MaxDelay {
			t.Fatalf("attempt %d: expected backoffDelay in [0, %v], got %v", attempt, opts.MaxDelay, got)
		}
	}
}
