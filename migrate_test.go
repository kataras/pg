package pg

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestMigrate' -v .

// migrateTestSuffix returns a suffix unique enough (process start time in nanoseconds plus a
// label) that tracking and data tables created by one test run never collide with leftovers
// from a previous run, or with another test, on the shared test database.
func migrateTestSuffix(label string) string {
	return fmt.Sprintf("%s_%d", label, time.Now().UnixNano())
}

// dropMigrateTestTables drops the given tables, ignoring errors (best-effort cleanup; the
// tables may not have been created if the test failed before creating them).
func dropMigrateTestTables(t *testing.T, db *DB, tables ...string) {
	t.Helper()

	for _, table := range tables {
		_, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS "+QuoteIdentifier(table))
	}
}

// TestMigrateSequential drives DB.Migrate through a fresh run, an idempotent re-run against the
// same files, and an incremental run after a third file is added, in that order, against the
// same tracking table, mirroring how an application calls Migrate repeatedly across deploys.
func TestMigrateSequential(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	trackingTable := migrateTestSuffix("test_migrate_ledger")
	dataTable := migrateTestSuffix("test_migrate_data")
	defer dropMigrateTestTables(t, db, trackingTable, dataTable)

	fsys := fstest.MapFS{
		"0001_create.sql": &fstest.MapFile{
			Data: []byte(fmt.Sprintf("CREATE TABLE %s (id bigint PRIMARY KEY)", QuoteIdentifier(dataTable))),
		},
		"0002_seed.sql": &fstest.MapFile{
			Data: []byte(fmt.Sprintf("INSERT INTO %s (id) VALUES (1), (2)", QuoteIdentifier(dataTable))),
		},
	}
	opts := &MigrateOptions{TableName: trackingTable}

	t.Run("fresh run applies files in order", func(t *testing.T) {
		applied, err := db.Migrate(ctx, fsys, opts)
		if err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		wantApplied := []string{"0001_create.sql", "0002_seed.sql"}
		if !slices.Equal(applied, wantApplied) {
			t.Fatalf("applied = %v, want %v", applied, wantApplied)
		}

		count, err := db.Count(ctx, "SELECT COUNT(*) FROM "+QuoteIdentifier(dataTable))
		if err != nil {
			t.Fatalf("count data table: %v", err)
		}
		if count != 2 {
			t.Fatalf("data table row count = %d, want 2", count)
		}

		ledger, err := QuerySlice[string](ctx, db, "SELECT version FROM "+QuoteIdentifier(trackingTable)+" ORDER BY version")
		if err != nil {
			t.Fatalf("read ledger: %v", err)
		}
		if !slices.Equal(ledger, wantApplied) {
			t.Fatalf("ledger = %v, want %v", ledger, wantApplied)
		}
	})

	t.Run("idempotent re-run applies nothing", func(t *testing.T) {
		applied, err := db.Migrate(ctx, fsys, opts)
		if err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if len(applied) != 0 {
			t.Fatalf("applied = %v, want empty", applied)
		}
	})

	t.Run("incremental run applies only the new file", func(t *testing.T) {
		fsys["0003_seed_more.sql"] = &fstest.MapFile{
			Data: []byte(fmt.Sprintf("INSERT INTO %s (id) VALUES (3)", QuoteIdentifier(dataTable))),
		}

		applied, err := db.Migrate(ctx, fsys, opts)
		if err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		wantApplied := []string{"0003_seed_more.sql"}
		if !slices.Equal(applied, wantApplied) {
			t.Fatalf("applied = %v, want %v", applied, wantApplied)
		}

		count, err := db.Count(ctx, "SELECT COUNT(*) FROM "+QuoteIdentifier(dataTable))
		if err != nil {
			t.Fatalf("count data table: %v", err)
		}
		if count != 3 {
			t.Fatalf("data table row count = %d, want 3", count)
		}
	})
}

// TestMigrateFailureAtomicity is the important proof that Migrate runs entirely inside one
// transaction: when the second file's SQL fails, the first file's effects must not persist and
// the tracking table must be left exactly as it was (no partial ledger row for either file).
func TestMigrateFailureAtomicity(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	trackingTable := migrateTestSuffix("test_migrate_ledger_fail")
	dataTable := migrateTestSuffix("test_migrate_data_fail")
	defer dropMigrateTestTables(t, db, trackingTable, dataTable)

	fsys := fstest.MapFS{
		"0001_create.sql": &fstest.MapFile{
			Data: []byte(fmt.Sprintf("CREATE TABLE %s (id bigint PRIMARY KEY)", QuoteIdentifier(dataTable))),
		},
		"0002_broken.sql": &fstest.MapFile{
			Data: []byte("THIS IS NOT VALID SQL;"),
		},
	}
	opts := &MigrateOptions{TableName: trackingTable}

	applied, err := db.Migrate(ctx, fsys, opts)
	if err == nil {
		t.Fatalf("Migrate: expected an error from the broken second file, got applied = %v", applied)
	}
	if applied != nil {
		t.Fatalf("applied = %v, want nil on error", applied)
	}

	// The first file's CREATE TABLE must have been rolled back along with everything else in
	// the same transaction, so the data table must not exist.
	exists, err := db.QueryBoolean(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)", dataTable)
	if err != nil {
		t.Fatalf("check data table existence: %v", err)
	}
	if exists {
		t.Fatalf("data table %q exists after a failed Migrate call; rollback did not undo the first file", dataTable)
	}

	// The tracking table itself is created by the same transaction, so it must not exist
	// either, proving the ledger was not left with a partial row for either file.
	exists, err = db.QueryBoolean(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)", trackingTable)
	if err != nil {
		t.Fatalf("check tracking table existence: %v", err)
	}
	if exists {
		t.Fatalf("tracking table %q exists after a failed Migrate call; rollback did not undo its creation", trackingTable)
	}
}

// TestMigrateBadTableName verifies that an unsafe TableName is rejected before Migrate runs any
// SQL at all (it cannot be a bind parameter, since it names a table).
func TestMigrateBadTableName(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fsys := fstest.MapFS{
		"0001_create.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
	}
	opts := &MigrateOptions{TableName: `x"; DROP TABLE schema_migrations; --`}

	applied, err := db.Migrate(context.Background(), fsys, opts)
	if err == nil {
		t.Fatal("Migrate: expected a validation error for an unsafe table name")
	}
	if applied != nil {
		t.Fatalf("applied = %v, want nil", applied)
	}
	if !strings.Contains(err.Error(), "invalid table name") {
		t.Fatalf("error = %v, want it to mention an invalid table name", err)
	}
}
