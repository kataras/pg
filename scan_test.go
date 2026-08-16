package pg

import (
	"context"
	"fmt"
	"testing"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestQueryStructs|TestQueryStruct|TestScanStructs' -v .

// scanTestParent is the "one" side of the ad-hoc join scanTestChild joins against below. Neither
// it nor scanTestChild/adHocItem is ever passed to Schema.Register. That's the point of this
// test: QueryStructs/QueryStruct/ScanStructs must work against a type Schema knows nothing
// about, unlike Repository[T] (NewRepository panics for an unregistered type).
type scanTestParent struct {
	ID   int64
	Name string
}

// adHocItem is the ad-hoc, unregistered read-model struct QueryStructs/QueryStruct/ScanStructs
// scan into below: ID and Name come straight off scanTestChild's own columns, Parent is
// populated by decoding a `to_jsonb(p.*) AS parent` projection of scanTestParent: exactly the
// "SELECT ... to_jsonb(x.*) AS thing" shape this task exists to support without a hand-written
// rows.Scan + json.Unmarshal. The query additionally selects scanTestChild's parent_id column,
// which adHocItem has no field for at all, to prove an unknown result column is ignored rather
// than causing an error (LooseTable's descriptor is non-strict).
type adHocItem struct {
	ID     int64
	Name   string
	Parent *scanTestParent
}

// setupScanTestTables (re)creates the parent+child scratch tables QueryStructs/QueryStruct/
// ScanStructs are exercised against below, and registers their cleanup via t.Cleanup.
func setupScanTestTables(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()

	const (
		parentTable = "test_scan_parent_scratch"
		childTable  = "test_scan_child_scratch"
	)

	if _, err := db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", childTable)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", parentTable)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (id BIGINT PRIMARY KEY, name TEXT)", parentTable)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (id BIGINT PRIMARY KEY, name TEXT, parent_id BIGINT REFERENCES %s(id))",
		childTable, parentTable)); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		dropTestTables(context.Background(), db, childTable, parentTable)
	})

	if _, err := db.Exec(ctx, fmt.Sprintf(
		"INSERT INTO %s (id, name) VALUES (1, 'parent-one')", parentTable)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, fmt.Sprintf(
		"INSERT INTO %s (id, name, parent_id) VALUES (100, 'child-one', 1)", childTable)); err != nil {
		t.Fatal(err)
	}
}

// scanTestJoinQuery is the query every test below scans with: a join projecting the parent row
// as JSONB (to_jsonb(p.*)) plus an extra column (c.parent_id) that adHocItem has no field for.
const scanTestJoinQuery = `
SELECT c.id, c.name, to_jsonb(p.*) AS parent, c.parent_id
FROM test_scan_child_scratch c
JOIN test_scan_parent_scratch p ON p.id = c.parent_id`

func assertAdHocItem(t *testing.T, item adHocItem) {
	t.Helper()

	if item.ID != 100 {
		t.Fatalf("expected ID 100, got %d", item.ID)
	}
	if item.Name != "child-one" {
		t.Fatalf("expected Name %q, got %q", "child-one", item.Name)
	}
	if item.Parent == nil {
		t.Fatal("expected Parent to be populated from the to_jsonb(p.*) projection, got nil")
	}
	if item.Parent.ID != 1 {
		t.Fatalf("expected Parent.ID 1, got %d", item.Parent.ID)
	}
	if item.Parent.Name != "parent-one" {
		t.Fatalf("expected Parent.Name %q, got %q", "parent-one", item.Parent.Name)
	}
}

// TestQueryStructsLooseJoin verifies QueryStructs against an unregistered ad-hoc struct: plain
// columns populate by name, a to_jsonb(...) projection decodes into a pointer field, and a
// result column with no matching struct field (c.parent_id) is ignored instead of erroring.
func TestQueryStructsLooseJoin(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupScanTestTables(t, db)

	items, err := QueryStructs[adHocItem](context.Background(), db, scanTestJoinQuery)
	if err != nil {
		t.Fatalf("QueryStructs: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 row, got %d: %#v", len(items), items)
	}

	assertAdHocItem(t, items[0])
}

// TestQueryStructNotFound verifies QueryStruct's single-row behavior: a match returns the
// populated adHocItem (fields, JSON-decoded Parent and all), and a non-matching WHERE clause
// reports ErrNoRows.
func TestQueryStructNotFound(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupScanTestTables(t, db)
	ctx := context.Background()

	t.Run("match", func(t *testing.T) {
		item, err := QueryStruct[adHocItem](ctx, db, scanTestJoinQuery+" WHERE c.id = $1", 100)
		if err != nil {
			t.Fatalf("QueryStruct: %v", err)
		}
		assertAdHocItem(t, item)
	})

	t.Run("no match reports ErrNoRows", func(t *testing.T) {
		_, err := QueryStruct[adHocItem](ctx, db, scanTestJoinQuery+" WHERE c.id = $1", 999)
		if !IsErrNoRows(err) {
			t.Fatalf("expected ErrNoRows, got: %v", err)
		}
	})
}

// TestScanStructs verifies ScanStructs scans a *DB.Query result the same way QueryStructs does,
// using only a Rows value (no *DB, so it always takes the desc.LooseTable path).
func TestScanStructs(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupScanTestTables(t, db)

	rows, err := db.Query(context.Background(), scanTestJoinQuery)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	items, err := ScanStructs[adHocItem](rows)
	if err != nil {
		t.Fatalf("ScanStructs: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 row, got %d: %#v", len(items), items)
	}

	assertAdHocItem(t, items[0])
}

// TestQueryStructsRegisteredType verifies QueryStructs against a type that IS registered in db's
// Schema (Customer, registered by openTestConnection) still resolves and scans through its real,
// registered *desc.Table descriptor instead of falling back to desc.LooseTable.
func TestQueryStructsRegisteredType(t *testing.T) {
	db, err := openTestConnection(true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	repo := NewRepository[Customer](db)

	customer := Customer{
		CognitoUserID: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee",
		Email:         "query-structs-registered@example.com",
		Name:          "Registered Path",
	}
	if err = repo.InsertSingle(ctx, customer, &customer.ID); err != nil {
		t.Fatal(err)
	}

	results, err := QueryStructs[Customer](ctx, db, "SELECT * FROM customers WHERE cognito_user_id = $1", customer.CognitoUserID)
	if err != nil {
		t.Fatalf("QueryStructs: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 row, got %d: %#v", len(results), results)
	}

	got := results[0]
	if got.ID != customer.ID {
		t.Fatalf("expected ID %q, got %q", customer.ID, got.ID)
	}
	if got.Email != customer.Email {
		t.Fatalf("expected Email %q, got %q", customer.Email, got.Email)
	}
	if got.Name != customer.Name {
		t.Fatalf("expected Name %q, got %q", customer.Name, got.Name)
	}
}
