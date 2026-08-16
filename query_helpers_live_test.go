package pg

import (
	"context"
	"fmt"
	"testing"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestDBCount|TestRepositoryCount|TestQueryMap|TestQueryFunc' -v .

// TestDBCount verifies DB.Count against a normal COUNT(*) as well as the documented
// zero-rows case: a COUNT(*) ... GROUP BY query against an empty table produces no rows at
// all (not a single row with count 0), which DB.Count must still report as (0, nil) instead
// of surfacing ErrNoRows to the caller.
func TestDBCount(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	const table = "test_db_count_scratch"

	if _, err = db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (id SERIAL PRIMARY KEY, category TEXT)", table)); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, table)

	t.Run("normal count", func(t *testing.T) {
		if _, err = db.Exec(ctx, fmt.Sprintf("INSERT INTO %s (category) VALUES ('a'), ('a'), ('b')", table)); err != nil {
			t.Fatal(err)
		}

		count, err := db.Count(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table))
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 3 {
			t.Fatalf("expected count 3, got %d", count)
		}
	})

	t.Run("zero rows from GROUP BY on an empty table", func(t *testing.T) {
		if _, err = db.Exec(ctx, fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			t.Fatal(err)
		}

		// A GROUP BY over an empty table yields no rows at all, unlike a plain COUNT(*)
		// (which always yields exactly one row, with value 0, over an empty table).
		count, err := db.Count(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s GROUP BY category", table))
		if err != nil {
			t.Fatalf("expected nil error for a no-rows query, got: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected count 0, got %d", count)
		}
	})
}

// TestRepositoryCount verifies that Repository[T].Count delegates to DB.Count, using the
// registered "customers" table.
func TestRepositoryCount(t *testing.T) {
	db, err := openTestConnection(true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	repo := NewRepository[Customer](db)

	count, err := repo.Count(ctx, "SELECT COUNT(*) FROM customers")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected count 0 on a freshly reset schema, got %d", count)
	}

	customer := Customer{
		CognitoUserID: "dddddddd-dddd-dddd-dddd-dddddddddddd",
		Email:         "repository-count@example.com",
		Name:          "Count",
	}
	if err = repo.InsertSingle(ctx, customer, &customer.ID); err != nil {
		t.Fatal(err)
	}

	count, err = repo.Count(ctx, "SELECT COUNT(*) FROM customers")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1 after inserting a row, got %d", count)
	}
}

// TestQueryMap verifies QueryMap's round trip, its documented duplicate-key-overwrite
// behavior (later rows win), and that a no-rows query returns an empty, non-nil map.
func TestQueryMap(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	const table = "test_query_map_scratch"

	if _, err = db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (k TEXT, v INTEGER, ord SERIAL)", table)); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, table)

	t.Run("round trip and duplicate key overwrite", func(t *testing.T) {
		// "a" appears twice; the row with v=99 has the higher "ord" so it must be the one
		// QueryMap's ORDER BY ord surfaces last, and therefore the one left in the map.
		if _, err = db.Exec(ctx, fmt.Sprintf(
			"INSERT INTO %s (k, v) VALUES ('a', 1), ('b', 2), ('a', 99)", table)); err != nil {
			t.Fatal(err)
		}

		result, err := QueryMap[string, int](ctx, db, fmt.Sprintf("SELECT k, v FROM %s ORDER BY ord", table))
		if err != nil {
			t.Fatalf("QueryMap: %v", err)
		}

		if len(result) != 2 {
			t.Fatalf("expected 2 keys, got %d: %#v", len(result), result)
		}
		if result["a"] != 99 {
			t.Fatalf("expected the later duplicate-key row to overwrite the earlier one, got a=%d", result["a"])
		}
		if result["b"] != 2 {
			t.Fatalf("expected b=2, got %d", result["b"])
		}
	})

	t.Run("no rows returns an empty non-nil map", func(t *testing.T) {
		result, err := QueryMap[string, int](ctx, db, fmt.Sprintf("SELECT k, v FROM %s WHERE k = 'does-not-exist'", table))
		if err != nil {
			t.Fatalf("QueryMap: %v", err)
		}
		if result == nil {
			t.Fatal("expected a non-nil empty map for a no-rows query")
		}
		if len(result) != 0 {
			t.Fatalf("expected an empty map, got %d entries: %#v", len(result), result)
		}
	})
}

// nameAndCount is a row shape that fits neither QuerySlice (more than one column) nor a
// registered struct, used to exercise QueryFunc's custom ScanFunc.
type nameAndCount struct {
	Name  string
	Count int64
}

// TestQueryFunc verifies QueryFunc against a two-column query using a custom ScanFunc.
func TestQueryFunc(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	const table = "test_query_func_scratch"

	if _, err = db.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (name TEXT)", table)); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, table)

	if _, err = db.Exec(ctx, fmt.Sprintf(
		"INSERT INTO %s (name) VALUES ('a'), ('a'), ('b')", table)); err != nil {
		t.Fatal(err)
	}

	scan := func(rows Rows) (nameAndCount, error) {
		var nc nameAndCount
		err := rows.Scan(&nc.Name, &nc.Count)
		return nc, err
	}

	results, err := QueryFunc(ctx, db, scan, fmt.Sprintf(
		"SELECT name, COUNT(*) FROM %s GROUP BY name ORDER BY name", table))
	if err != nil {
		t.Fatalf("QueryFunc: %v", err)
	}

	want := []nameAndCount{{Name: "a", Count: 2}, {Name: "b", Count: 1}}
	if len(results) != len(want) {
		t.Fatalf("expected %d rows, got %d: %#v", len(want), len(results), results)
	}
	for i, w := range want {
		if results[i] != w {
			t.Fatalf("row %d: expected %#v, got %#v", i, w, results[i])
		}
	}
}
