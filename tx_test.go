package pg

import (
	"context"
	"testing"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestInTransactionGeneric' -v .

// txCustomerRepo is a small typed repository wrapper over Repository[Customer], used only to
// exercise the package-level InTransaction helper. It mirrors the CustomerRepository pattern
// from repository_example_test.go but is kept separate so this test does not depend on (or get
// tangled up with) that example's own hand-written InTransaction override.
type txCustomerRepo struct {
	*Repository[Customer]
}

// newTxCustomerRepo is the wrap function passed to InTransaction: it rebuilds txCustomerRepo
// around whatever *DB InTransaction hands it (transactional or not).
func newTxCustomerRepo(db *DB) *txCustomerRepo {
	return &txCustomerRepo{Repository: NewRepository[Customer](db)}
}

// customerRowExists reports whether a "customers" row with the given email exists, queried
// directly by SQL so the check does not depend on which of Customer's other fields are zero.
func customerRowExists(ctx context.Context, db *DB, email string) (bool, error) {
	return db.QueryBoolean(ctx, "SELECT EXISTS(SELECT 1 FROM customers WHERE email = $1)", email)
}

// TestInTransactionGeneric verifies the package-level InTransaction helper's three documented
// behaviors: rolling back on ErrIntentionalRollback, committing on success, and joining
// (instead of nesting) an already-open transaction.
func TestInTransactionGeneric(t *testing.T) {
	db, err := openTestConnection(true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	t.Run("intentional rollback leaves no row behind", func(t *testing.T) {
		customer := Customer{
			CognitoUserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			Email:         "tx-generic-rollback@example.com",
			Name:          "Rollback",
		}

		err := InTransaction(ctx, db, newTxCustomerRepo, func(txRepo *txCustomerRepo) error {
			if err := txRepo.InsertSingle(ctx, customer, &customer.ID); err != nil {
				return err
			}

			return ErrIntentionalRollback
		})
		if err != nil {
			t.Fatalf("expected nil error on intentional rollback, got: %v", err)
		}

		exists, err := customerRowExists(ctx, db, customer.Email)
		if err != nil {
			t.Fatalf("exists: %v", err)
		}
		if exists {
			t.Fatal("expected no row to exist after an intentional rollback")
		}
	})

	t.Run("success commits the row", func(t *testing.T) {
		customer := Customer{
			CognitoUserID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
			Email:         "tx-generic-commit@example.com",
			Name:          "Commit",
		}

		err := InTransaction(ctx, db, newTxCustomerRepo, func(txRepo *txCustomerRepo) error {
			return txRepo.InsertSingle(ctx, customer, &customer.ID)
		})
		if err != nil {
			t.Fatalf("expected nil error on successful commit, got: %v", err)
		}

		exists, err := customerRowExists(ctx, db, customer.Email)
		if err != nil {
			t.Fatalf("exists: %v", err)
		}
		if !exists {
			t.Fatal("expected the row to exist after a successful commit")
		}
	})

	t.Run("nested call joins the outer transaction", func(t *testing.T) {
		customer := Customer{
			CognitoUserID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
			Email:         "tx-generic-nested@example.com",
			Name:          "Nested",
		}

		err := db.InTransaction(ctx, func(tx *DB) error {
			return InTransaction(ctx, tx, newTxCustomerRepo, func(txRepo *txCustomerRepo) error {
				if txRepo.DB() != tx {
					t.Fatal("expected the nested InTransaction to reuse the same transactional *DB, got a different one")
				}

				return txRepo.InsertSingle(ctx, customer, &customer.ID)
			})
		})
		if err != nil {
			t.Fatalf("expected the outer transaction to commit but got: %v", err)
		}

		exists, err := customerRowExists(ctx, db, customer.Email)
		if err != nil {
			t.Fatalf("exists: %v", err)
		}
		if !exists {
			t.Fatal("expected the row inserted through the nested call to be committed")
		}
	})
}
