package pg

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestInTransactionCommitError is the regression test for the InTransaction dead-store
// bug: with the old unnamed-result code, a failed COMMIT was silently discarded and
// InTransaction returned nil even though nothing was actually persisted.
//
// It creates a parent/child pair of tables with a DEFERRABLE INITIALLY DEFERRED foreign
// key, so the constraint is only checked at COMMIT time: the INSERT inside fn succeeds,
// but the COMMIT that InTransaction issues afterwards fails with a foreign key violation.
func TestInTransactionCommitError(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	const parentTable, childTable = "test_intx_commit_parent", "test_intx_commit_child"

	if _, err = db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s, %s", childTable, parentTable)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (id INTEGER PRIMARY KEY)", parentTable)); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", parentTable))

	if _, err = db.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE %s (
			id SERIAL PRIMARY KEY,
			parent_id INTEGER NOT NULL REFERENCES %s(id) DEFERRABLE INITIALLY DEFERRED
		)`, childTable, parentTable)); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", childTable))

	err = db.InTransaction(ctx, func(tx *DB) error {
		// The parent row with id=1 does not exist; this succeeds because the FK check
		// is deferred to COMMIT.
		_, err := tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (parent_id) VALUES (1)", childTable))
		return err
	})
	if err == nil {
		t.Fatal("expected InTransaction to return the deferred COMMIT error but got nil")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a *pgconn.PgError but got: %#+v (%v)", err, err)
	}
	if pgErr.Code != "23503" { // foreign_key_violation.
		t.Fatalf("expected foreign key violation (23503) but got code: %s (%v)", pgErr.Code, err)
	}
}

// TestInTransactionIntentionalRollback verifies that returning ErrIntentionalRollback
// from the function passed to InTransaction rolls the transaction back, InTransaction
// itself returns nil, and none of the work done inside fn is persisted.
func TestInTransactionIntentionalRollback(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	const scratchTable = "test_intx_rollback_scratch"

	if _, err = db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", scratchTable)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (id SERIAL PRIMARY KEY, val TEXT)", scratchTable)); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", scratchTable))

	err = db.InTransaction(ctx, func(tx *DB) error {
		if _, err := tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (val) VALUES ($1)", scratchTable), "should not be persisted"); err != nil {
			return err
		}

		return ErrIntentionalRollback
	})
	if err != nil {
		t.Fatalf("expected InTransaction to return nil on a successful intentional rollback but got: %v", err)
	}

	var count int
	if err = db.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", scratchTable)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after intentional rollback but got: %d", count)
	}
}

// TestInTransactionNested verifies that calling InTransaction again on the *DB passed
// into fn (which is already inside a transaction) runs the inner function directly,
// without starting a nested transaction, and that the whole thing still commits.
func TestInTransactionNested(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	const scratchTable = "test_intx_nested_scratch"

	if _, err = db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", scratchTable)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (id SERIAL PRIMARY KEY, val TEXT)", scratchTable)); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", scratchTable))

	err = db.InTransaction(ctx, func(tx *DB) error {
		return tx.InTransaction(ctx, func(tx2 *DB) error {
			if tx2 != tx {
				return fmt.Errorf("expected the nested InTransaction to run fn with the same *DB, got a different one")
			}

			_, err := tx2.Exec(ctx, fmt.Sprintf("INSERT INTO %s (val) VALUES ($1)", scratchTable), "nested")
			return err
		})
	})
	if err != nil {
		t.Fatalf("expected the nested transaction to commit but got: %v", err)
	}

	var count int
	if err = db.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", scratchTable)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after commit but got: %d", count)
	}
}

// TestRepositoryInTransactionContext is the regression test for repository.go's
// InTransaction discarding the caller's context: with the old code, an already-canceled
// context was replaced with context.Background() before reaching DB.Begin, so the call
// would still succeed. With the fix, the canceled context reaches Begin and the call
// fails promptly instead of opening a transaction.
func TestRepositoryInTransactionContext(t *testing.T) {
	db, err := openTestConnection(false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository[Customer](db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before InTransaction is even called.

	err = repo.InTransaction(ctx, func(txRepo *Repository[Customer]) error {
		t.Fatal("fn should not run when ctx is already canceled")
		return nil
	})
	if err == nil {
		t.Fatal("expected InTransaction to return an error for an already-canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the error to wrap context.Canceled but got: %v", err)
	}
}
