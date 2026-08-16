package pg

import (
	"context"
	"fmt"
	"testing"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestDBExecMany|TestDBSetConstraintsDeferred' -v .

const execManyScratchTable = "test_db_execmany_scratch"

// TestDBExecMany verifies both of ExecMany's documented outcomes: statements that all succeed
// commit together, and a failing statement rolls back every earlier one in the same call (none
// of them persist).
func TestDBExecMany(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	if _, err = db.Exec(ctx, "DROP TABLE IF EXISTS "+execManyScratchTable); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (id BIGSERIAL PRIMARY KEY, name TEXT UNIQUE NOT NULL)", execManyScratchTable)); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP TABLE IF EXISTS "+execManyScratchTable)

	t.Run("all statements commit together", func(t *testing.T) {
		err := db.ExecMany(ctx,
			fmt.Sprintf("INSERT INTO %s (name) VALUES ('one')", execManyScratchTable),
			fmt.Sprintf("INSERT INTO %s (name) VALUES ('two')", execManyScratchTable),
		)
		if err != nil {
			t.Fatalf("ExecMany: %v", err)
		}

		count, err := db.Count(ctx, "SELECT COUNT(*) FROM "+execManyScratchTable)
		if err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Fatalf("expected 2 rows after ExecMany, got %d", count)
		}
	})

	t.Run("a failing statement rolls back the earlier ones", func(t *testing.T) {
		// The second statement violates the UNIQUE constraint on name ('two' already exists
		// from the previous subtest), so the whole call must fail and 'three' must not persist.
		err := db.ExecMany(ctx,
			fmt.Sprintf("INSERT INTO %s (name) VALUES ('three')", execManyScratchTable),
			fmt.Sprintf("INSERT INTO %s (name) VALUES ('two')", execManyScratchTable),
		)
		if err == nil {
			t.Fatal("expected ExecMany to fail on the duplicate second statement")
		}

		exists, err := db.QueryBoolean(ctx, fmt.Sprintf(
			"SELECT EXISTS (SELECT 1 FROM %s WHERE name = 'three')", execManyScratchTable))
		if err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatal("expected the first statement's row ('three') to have been rolled back")
		}
	})
}

const (
	deferredScratchParentTable = "test_db_deferred_scratch_parent"
	deferredScratchChildTable  = "test_db_deferred_scratch_child"
	deferredScratchFKName      = "test_db_deferred_scratch_fk"
)

// setupDeferredScratchTables (re)creates a parent/child pair with a DEFERRABLE (but not
// INITIALLY DEFERRED, i.e. checked immediately by default) foreign key, named explicitly as
// deferredScratchFKName so tests can exercise SetConstraintsDeferred's named-constraint code
// path (SET CONSTRAINTS "c1" DEFERRED) as well as its ALL variant.
func setupDeferredScratchTables(ctx context.Context, db *DB) error {
	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+deferredScratchChildTable); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+deferredScratchParentTable); err != nil {
		return err
	}

	if _, err := db.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (id BIGINT PRIMARY KEY)", deferredScratchParentTable)); err != nil {
		return err
	}

	_, err := db.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE %s (id BIGINT PRIMARY KEY, parent_id BIGINT NOT NULL,
			CONSTRAINT %s FOREIGN KEY (parent_id) REFERENCES %s (id) DEFERRABLE)`,
		deferredScratchChildTable, deferredScratchFKName, deferredScratchParentTable))
	return err
}

func dropDeferredScratchTables(ctx context.Context, db *DB) {
	db.Exec(ctx, "DROP TABLE IF EXISTS "+deferredScratchChildTable)
	db.Exec(ctx, "DROP TABLE IF EXISTS "+deferredScratchParentTable)
}

// TestDBSetConstraintsDeferred verifies: SetConstraintsDeferred errors outside of a
// transaction; and, inside one, deferring a DEFERRABLE-but-not-INITIALLY-DEFERRED foreign key
// (via both the ALL and the named-constraint form) lets an out-of-order insert pair (the
// referencing child row before the referenced parent row) commit, because the FK check is
// postponed until COMMIT instead of running immediately after the child INSERT.
func TestDBSetConstraintsDeferred(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	t.Run("outside a transaction", func(t *testing.T) {
		if err := db.SetConstraintsDeferred(ctx); err == nil {
			t.Fatal("expected SetConstraintsDeferred to error outside of a transaction")
		}
	})

	t.Run("ALL, out-of-order insert pair commits", func(t *testing.T) {
		if err := setupDeferredScratchTables(ctx, db); err != nil {
			t.Fatal(err)
		}
		defer dropDeferredScratchTables(ctx, db)

		err := db.InTransaction(ctx, func(db *DB) error {
			if err := db.SetConstraintsDeferred(ctx); err != nil {
				return err
			}

			// Child references parent id=1, which does not exist yet: only legal because the
			// FK check was just deferred to COMMIT.
			if _, err := db.Exec(ctx, fmt.Sprintf(
				"INSERT INTO %s (id, parent_id) VALUES (1, 1)", deferredScratchChildTable)); err != nil {
				return err
			}
			if _, err := db.Exec(ctx, fmt.Sprintf(
				"INSERT INTO %s (id) VALUES (1)", deferredScratchParentTable)); err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			t.Fatalf("expected the out-of-order insert pair to commit after SetConstraintsDeferred(ALL): %v", err)
		}

		count, err := db.Count(ctx, "SELECT COUNT(*) FROM "+deferredScratchChildTable)
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("expected the child row to have committed, got count %d", count)
		}
	})

	t.Run("named constraint, out-of-order insert pair commits", func(t *testing.T) {
		if err := setupDeferredScratchTables(ctx, db); err != nil {
			t.Fatal(err)
		}
		defer dropDeferredScratchTables(ctx, db)

		err := db.InTransaction(ctx, func(db *DB) error {
			if err := db.SetConstraintsDeferred(ctx, deferredScratchFKName); err != nil {
				return err
			}

			if _, err := db.Exec(ctx, fmt.Sprintf(
				"INSERT INTO %s (id, parent_id) VALUES (2, 2)", deferredScratchChildTable)); err != nil {
				return err
			}
			if _, err := db.Exec(ctx, fmt.Sprintf(
				"INSERT INTO %s (id) VALUES (2)", deferredScratchParentTable)); err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			t.Fatalf("expected the out-of-order insert pair to commit after SetConstraintsDeferred(%q): %v", deferredScratchFKName, err)
		}
	})

	t.Run("without deferring, out-of-order insert pair fails", func(t *testing.T) {
		if err := setupDeferredScratchTables(ctx, db); err != nil {
			t.Fatal(err)
		}
		defer dropDeferredScratchTables(ctx, db)

		err := db.InTransaction(ctx, func(db *DB) error {
			_, err := db.Exec(ctx, fmt.Sprintf(
				"INSERT INTO %s (id, parent_id) VALUES (3, 3)", deferredScratchChildTable))
			return err
		})
		if err == nil {
			t.Fatal("expected the out-of-order insert to fail its immediate (non-deferred) FK check")
		}
	})
}
