package pg

import (
	"context"
	"fmt"
	"testing"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestDBDeleteByID|TestDBDeleteBy|TestDBExistsByAndCountBy|TestDBTableNameCRUDUnknownTableAndColumn|TestDBSelectSingle' -v .

// crudScratchItem is a scratch entity registered only for this file's tests, so the table-name
// CRUD API added in db_crud.go (DeleteByID, DeleteBy, ExistsBy, CountBy, SelectSingle) can be
// exercised against a real registered table. Its scratch table is created/dropped with plain
// DDL (see setupCRUDScratchTable below), mirroring paginationItem/setupPaginationScratchTable
// in pagination_live_test.go, rather than via DB.CreateSchema/DeleteSchema, which drops the
// whole search_path schema and would be unsafe to run against a database shared with other
// concurrently running tests/tasks.
type crudScratchItem struct {
	ID       int64  `pg:"type=bigint,primary"`
	Name     string `pg:"type=varchar(255)"`
	Category string `pg:"type=varchar(255)"`
}

const crudScratchTable = "test_db_crud_scratch_items"

// openCRUDTestConnection opens a *DB whose schema has only crudScratchItem registered, so
// DeleteByID/DeleteBy/ExistsBy/CountBy/SelectSingle can resolve its table descriptor by name or
// by type. It does not create the table itself (see setupCRUDScratchTable) or run
// CreateSchema/DeleteSchema.
func openCRUDTestConnection() (*DB, error) {
	schema := NewSchema()
	schema.MustRegister(crudScratchTable, crudScratchItem{})

	return Open(context.Background(), schema, getTestConnString())
}

// setupCRUDScratchTable (re)creates crudScratchTable with plain DDL, mirroring the scratch-table
// pattern already used by TestDBCount/TestQueryMap/TestQueryFunc (query_helpers_live_test.go)
// and setupPaginationScratchTable (pagination_live_test.go).
func setupCRUDScratchTable(ctx context.Context, db *DB) error {
	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+crudScratchTable); err != nil {
		return err
	}

	_, err := db.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (id BIGSERIAL PRIMARY KEY, name VARCHAR(255) NOT NULL, category VARCHAR(255) NOT NULL)",
		crudScratchTable))
	return err
}

func insertCRUDScratchRow(ctx context.Context, db *DB, name, category string) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, fmt.Sprintf(
		"INSERT INTO %s (name, category) VALUES ($1, $2) RETURNING id", crudScratchTable),
		name, category).Scan(&id)
	return id, err
}

// TestDBDeleteByID verifies both outcomes DeleteByID documents: true for a row that existed and
// was removed, false for one that did not (already deleted).
func TestDBDeleteByID(t *testing.T) {
	db, err := openCRUDTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err = setupCRUDScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, crudScratchTable)

	id, err := insertCRUDScratchRow(ctx, db, "alpha", "a")
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := db.DeleteByID(ctx, crudScratchTable, id)
	if err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
	if !deleted {
		t.Fatal("expected DeleteByID to report true for a row that existed")
	}

	deleted, err = db.DeleteByID(ctx, crudScratchTable, id)
	if err != nil {
		t.Fatalf("DeleteByID (second call): %v", err)
	}
	if deleted {
		t.Fatal("expected DeleteByID to report false for an already-deleted row")
	}
}

// TestDBDeleteBy verifies deleting by a two-pair AND'ed match: with three rows where only one
// matches both pairs, DeleteBy must remove exactly that one and leave the other two.
func TestDBDeleteBy(t *testing.T) {
	db, err := openCRUDTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err = setupCRUDScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, crudScratchTable)

	if _, err = insertCRUDScratchRow(ctx, db, "alpha", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err = insertCRUDScratchRow(ctx, db, "beta", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err = insertCRUDScratchRow(ctx, db, "gamma", "b"); err != nil {
		t.Fatal(err)
	}

	// Only "alpha" matches both category="a" AND name="alpha"; "beta" matches category="a" but
	// not name="alpha", so it must survive.
	n, err := db.DeleteBy(ctx, crudScratchTable, "category", "a", "name", "alpha")
	if err != nil {
		t.Fatalf("DeleteBy: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected DeleteBy to remove 1 row, removed %d", n)
	}

	remaining, err := db.CountBy(ctx, crudScratchTable)
	if err != nil {
		t.Fatalf("CountBy: %v", err)
	}
	if remaining != 2 {
		t.Fatalf("expected 2 rows to remain, got %d", remaining)
	}
}

// TestDBExistsByAndCountBy verifies ExistsBy/CountBy both with and without colValPairs, matching
// their documented no-pairs behavior (whether/how many rows the whole table has).
func TestDBExistsByAndCountBy(t *testing.T) {
	db, err := openCRUDTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err = setupCRUDScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, crudScratchTable)

	t.Run("empty table", func(t *testing.T) {
		exists, err := db.ExistsBy(ctx, crudScratchTable)
		if err != nil {
			t.Fatalf("ExistsBy: %v", err)
		}
		if exists {
			t.Fatal("expected ExistsBy(no pairs) to report false on an empty table")
		}

		count, err := db.CountBy(ctx, crudScratchTable)
		if err != nil {
			t.Fatalf("CountBy: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected CountBy(no pairs) to report 0 on an empty table, got %d", count)
		}
	})

	if _, err = insertCRUDScratchRow(ctx, db, "alpha", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err = insertCRUDScratchRow(ctx, db, "beta", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err = insertCRUDScratchRow(ctx, db, "gamma", "b"); err != nil {
		t.Fatal(err)
	}

	t.Run("no pairs", func(t *testing.T) {
		exists, err := db.ExistsBy(ctx, crudScratchTable)
		if err != nil {
			t.Fatalf("ExistsBy: %v", err)
		}
		if !exists {
			t.Fatal("expected ExistsBy(no pairs) to report true once rows exist")
		}

		count, err := db.CountBy(ctx, crudScratchTable)
		if err != nil {
			t.Fatalf("CountBy: %v", err)
		}
		if count != 3 {
			t.Fatalf("expected CountBy(no pairs) to report 3, got %d", count)
		}
	})

	t.Run("with pairs, match", func(t *testing.T) {
		exists, err := db.ExistsBy(ctx, crudScratchTable, "category", "b")
		if err != nil {
			t.Fatalf("ExistsBy: %v", err)
		}
		if !exists {
			t.Fatal("expected ExistsBy(category=b) to report true")
		}

		count, err := db.CountBy(ctx, crudScratchTable, "category", "a")
		if err != nil {
			t.Fatalf("CountBy: %v", err)
		}
		if count != 2 {
			t.Fatalf("expected CountBy(category=a) to report 2, got %d", count)
		}
	})

	t.Run("with pairs, no match", func(t *testing.T) {
		exists, err := db.ExistsBy(ctx, crudScratchTable, "category", "does-not-exist")
		if err != nil {
			t.Fatalf("ExistsBy: %v", err)
		}
		if exists {
			t.Fatal("expected ExistsBy(category=does-not-exist) to report false")
		}
	})
}

// TestDBTableNameCRUDUnknownTableAndColumn verifies the core security guarantee of this file's
// API: an unknown table name AND an unknown column name must both produce a descriptive error
// before any SQL reaches the server, for every one of DeleteByID/DeleteBy/ExistsBy/CountBy.
func TestDBTableNameCRUDUnknownTableAndColumn(t *testing.T) {
	db, err := openCRUDTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err = setupCRUDScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, crudScratchTable)

	const unknownTable = "this_table_was_never_registered"

	t.Run("unknown table", func(t *testing.T) {
		if _, err := db.DeleteByID(ctx, unknownTable, 1); err == nil {
			t.Error("DeleteByID: expected an error for an unknown table")
		}
		if _, err := db.DeleteBy(ctx, unknownTable, "col", "v"); err == nil {
			t.Error("DeleteBy: expected an error for an unknown table")
		}
		if _, err := db.ExistsBy(ctx, unknownTable); err == nil {
			t.Error("ExistsBy: expected an error for an unknown table")
		}
		if _, err := db.CountBy(ctx, unknownTable); err == nil {
			t.Error("CountBy: expected an error for an unknown table")
		}
	})

	const unknownColumn = "this_column_was_never_registered"

	t.Run("unknown column", func(t *testing.T) {
		if _, err := db.DeleteBy(ctx, crudScratchTable, unknownColumn, "v"); err == nil {
			t.Error("DeleteBy: expected an error for an unknown column")
		}
		if _, err := db.ExistsBy(ctx, crudScratchTable, unknownColumn, "v"); err == nil {
			t.Error("ExistsBy: expected an error for an unknown column")
		}
		if _, err := db.CountBy(ctx, crudScratchTable, unknownColumn, "v"); err == nil {
			t.Error("CountBy: expected an error for an unknown column")
		}
	})
}

// TestDBSelectSingle verifies the happy path (scanning a matching row into a registered struct)
// and the documented ErrNoRows case (a query that matches nothing).
func TestDBSelectSingle(t *testing.T) {
	db, err := openCRUDTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err = setupCRUDScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, crudScratchTable)

	id, err := insertCRUDScratchRow(ctx, db, "solo", "x")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("match", func(t *testing.T) {
		var item crudScratchItem
		err := db.SelectSingle(ctx, &item,
			fmt.Sprintf("SELECT * FROM %s WHERE id = $1", crudScratchTable), id)
		if err != nil {
			t.Fatalf("SelectSingle: %v", err)
		}
		if item.ID != id || item.Name != "solo" || item.Category != "x" {
			t.Fatalf("SelectSingle: unexpected result: %+v", item)
		}
	})

	t.Run("no rows", func(t *testing.T) {
		var item crudScratchItem
		err := db.SelectSingle(ctx, &item,
			fmt.Sprintf("SELECT * FROM %s WHERE id = $1", crudScratchTable), int64(-1))
		if !IsErrNoRows(err) {
			t.Fatalf("SelectSingle: expected ErrNoRows for a query matching nothing, got: %v", err)
		}
	})
}
