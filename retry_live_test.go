package pg

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestInTransactionRetry' -v .

// TestInTransactionRetryNestedRunsOnce verifies the documented nested-transaction rule: when
// InTransactionRetry is called on a *DB that is already inside a transaction, fn runs exactly
// once (no retry, even for an error IsErrRetryableTx would classify as retryable) and its
// result is returned as-is.
func TestInTransactionRetryNestedRunsOnce(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	txDB, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Best-effort cleanup: the transaction is rolled back inside the test body, so this
	// second Rollback is expected to report that the transaction is already closed.
	defer func() { _ = txDB.Rollback(ctx) }()

	// A fabricated, but real, SQLSTATE 40001 PgError: exactly what IsErrRetryableTx (the
	// default classifier) would call retryable, so if InTransactionRetry retried here despite
	// being nested, this test would see more than 1 call.
	fabricated := &pgconn.PgError{Severity: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update"}

	var calls int
	retErr := txDB.InTransactionRetry(ctx, RetryOptions{MaxAttempts: 5}, func(tx *DB) error {
		calls++
		if tx != txDB {
			t.Fatal("expected the nested call to run fn with the same already-transactional *DB")
		}
		return fabricated
	})

	if calls != 1 {
		t.Fatalf("expected fn to run exactly once for a nested InTransactionRetry (no retry), got %d calls", calls)
	}
	if !errors.Is(retErr, fabricated) {
		t.Fatalf("expected the fabricated error to be returned as-is, got: %v", retErr)
	}
}

const retryScratchTable = "retry_scratch_counter"

// setupRetryScratchTable (re)creates retryScratchTable with a single row (id=1, counter=0) for
// TestInTransactionRetrySerializationRace's two goroutines to race on.
func setupRetryScratchTable(ctx context.Context, db *DB) error {
	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+retryScratchTable); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (id INTEGER PRIMARY KEY, counter INTEGER NOT NULL)", retryScratchTable)); err != nil {
		return err
	}

	_, err := db.Exec(ctx, fmt.Sprintf("INSERT INTO %s (id, counter) VALUES (1, 0)", retryScratchTable))
	return err
}

// TestInTransactionRetrySerializationRace runs two goroutines under pgx.Serializable, both
// doing a classic read-modify-write (SELECT counter, then UPDATE counter = <that value> + 1)
// on the same row, synchronized with a barrier so their reads deliberately overlap before
// either writes: the textbook pattern for forcing PostgreSQL's SERIALIZABLE isolation to
// detect a conflict and fail one side with SQLSTATE 40001.
//
// It asserts three things, not just that both goroutines eventually returned without error:
//   - the final counter value is 2, i.e. both increments actually landed (neither was lost to
//     an unretried, silently-discarded serialization failure);
//   - IsRetryable (wrapped to count its own retryable verdicts) was actually invoked with a
//     retryable error at least once, proving a retry genuinely happened, not merely that both
//     goroutines happened to finish;
//   - both InTransactionRetry calls returned nil.
func TestInTransactionRetrySerializationRace(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := setupRetryScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, retryScratchTable)

	var retryableObservations int32

	opts := RetryOptions{
		MaxAttempts: 8,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    200 * time.Millisecond,
		TxOptions:   pgx.TxOptions{IsoLevel: pgx.Serializable},
		IsRetryable: func(err error) bool {
			if IsErrRetryableTx(err) {
				atomic.AddInt32(&retryableObservations, 1)
				return true
			}
			return false
		},
	}

	// Two-party rendezvous: each goroutine's very first attempt (never its retries) calls
	// Done then Wait, so neither can proceed past its initial SELECT until both have read -
	// guaranteeing their read sets overlap before either issues its UPDATE.
	var barrier sync.WaitGroup
	barrier.Add(2)

	increment := func() error {
		attempt := 0

		return db.InTransactionRetry(ctx, opts, func(tx *DB) error {
			attempt++

			var current int
			if err := tx.QueryRow(ctx, fmt.Sprintf("SELECT counter FROM %s WHERE id = 1", retryScratchTable)).Scan(&current); err != nil {
				return err
			}

			if attempt == 1 {
				barrier.Done()
				barrier.Wait()
			}

			_, err := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET counter = $1 WHERE id = 1", retryScratchTable), current+1)
			return err
		})
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	for i := range errs {
		go func(i int) {
			defer wg.Done()
			errs[i] = increment()
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: InTransactionRetry: %v", i, err)
		}
	}

	if atomic.LoadInt32(&retryableObservations) < 1 {
		t.Fatal("expected at least one attempt to fail with a retryable SQLSTATE (40001/40P01) and actually be retried, " +
			"but IsRetryable was never invoked with a retryable error - the barrier failed to force a genuine conflict")
	}

	var final int
	if err := db.QueryRow(ctx, fmt.Sprintf("SELECT counter FROM %s WHERE id = 1", retryScratchTable)).Scan(&final); err != nil {
		t.Fatal(err)
	}
	if final != 2 {
		t.Fatalf("expected the final counter to reflect both increments (2), got %d", final)
	}
}
