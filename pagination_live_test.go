package pg

import (
	"context"
	"fmt"
	"testing"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestRepositorySelectPaginated|TestRepositorySelectWithTotal' -v .

// paginationItem is a scratch entity registered only for this file's tests, so
// SelectPaginated/SelectWithTotal can be exercised through a real Repository[T] instead of raw
// SQL scanning. Its scratch table is created/dropped with plain DDL (see
// openPaginationTestConnection/setupPaginationScratchTable below) rather than via
// DB.CreateSchema/DeleteSchema, and rows are seeded via raw db.Exec rather than
// Repository.Insert, so these tests never touch (or need to reason about defaults for) anything
// beyond their own scratch table. In particular, they never run DeleteSchema, which drops the
// whole search_path schema and would be unsafe to run against a database shared with other
// concurrently running tests.
type paginationItem struct {
	ID       int64  `pg:"type=bigint,primary"`
	Name     string `pg:"type=varchar(255)"`
	Category string `pg:"type=varchar(255)"`
}

const paginationScratchTable = "pagination_scratch_items"

// openPaginationTestConnection opens a *DB whose schema has only paginationItem registered, so
// NewRepository[paginationItem] can resolve its table descriptor. It does not create the table
// itself (see setupPaginationScratchTable) or run CreateSchema/DeleteSchema.
func openPaginationTestConnection() (*DB, error) {
	schema := NewSchema()
	schema.MustRegister(paginationScratchTable, paginationItem{})

	return Open(context.Background(), schema, getTestConnString())
}

// setupPaginationScratchTable (re)creates the paginationScratchTable table with plain DDL,
// mirroring the scratch-table pattern already used by TestDBCount/TestQueryMap/TestQueryFunc in
// query_helpers_live_test.go.
func setupPaginationScratchTable(ctx context.Context, db *DB) error {
	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+paginationScratchTable); err != nil {
		return err
	}

	_, err := db.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (id BIGSERIAL PRIMARY KEY, name VARCHAR(255) NOT NULL, category VARCHAR(255) NOT NULL)",
		paginationScratchTable))
	return err
}

// seedPaginationScratchTable inserts n rows (name "item-0" .. "item-<n-1>"), all with category
// "a" except for the very last one, which gets category "b", giving tests a category value
// ("b") that matches exactly one row and one ("does-not-exist") that matches none.
func seedPaginationScratchTable(ctx context.Context, db *DB, n int) error {
	for i := 0; i < n; i++ {
		category := "a"
		if i == n-1 {
			category = "b"
		}

		if _, err := db.Exec(ctx, fmt.Sprintf(
			"INSERT INTO %s (name, category) VALUES ($1, $2)", paginationScratchTable),
			fmt.Sprintf("item-%d", i), category); err != nil {
			return err
		}
	}

	return nil
}

// TestRepositorySelectPaginated verifies SelectPaginated's documented behavior end to end:
// paging through consecutive, non-overlapping pages with a correct total; a filter matching no
// rows short-circuiting to (empty, 0, nil) without error; WithoutTotal reporting total -1 while
// still returning items; and page.OrderBy (sourced from Repository.OrderBy, as its doc requires)
// actually driving row order.
func TestRepositorySelectPaginated(t *testing.T) {
	db, err := openPaginationTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	if err = setupPaginationScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, paginationScratchTable)

	const rowCount = 10
	if err = seedPaginationScratchTable(ctx, db, rowCount); err != nil {
		t.Fatal(err)
	}

	repo := NewRepository[paginationItem](db)

	orderBy, err := repo.OrderBy("id", false)
	if err != nil {
		t.Fatalf("repo.OrderBy: %v", err)
	}

	baseQuery := fmt.Sprintf("SELECT id, name, category FROM %s", paginationScratchTable)

	t.Run("page 1 and page 2 cover all rows without overlap", func(t *testing.T) {
		page1 := PageOptions{Limit: 4, Offset: 0, OrderBy: orderBy}
		items1, total1, err := repo.SelectPaginated(ctx, page1, baseQuery)
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		if total1 != rowCount {
			t.Fatalf("page 1: expected total %d, got %d", rowCount, total1)
		}
		if len(items1) != 4 {
			t.Fatalf("page 1: expected 4 items, got %d", len(items1))
		}

		page2 := PageOptions{Limit: 4, Offset: 4, OrderBy: orderBy}
		items2, total2, err := repo.SelectPaginated(ctx, page2, baseQuery)
		if err != nil {
			t.Fatalf("page 2: %v", err)
		}
		if total2 != rowCount {
			t.Fatalf("page 2: expected total %d, got %d", rowCount, total2)
		}
		if len(items2) != 4 {
			t.Fatalf("page 2: expected 4 items, got %d", len(items2))
		}

		seen := make(map[int64]bool, len(items1)+len(items2))
		for _, it := range items1 {
			seen[it.ID] = true
		}
		for _, it := range items2 {
			if seen[it.ID] {
				t.Fatalf("expected no overlap between page 1 and page 2, but id %d appeared in both", it.ID)
			}
		}

		// orderBy ("id" ASC) must make page 1's last id strictly less than page 2's first id.
		if items1[len(items1)-1].ID >= items2[0].ID {
			t.Fatalf("expected page 1's last id (%d) < page 2's first id (%d) under ascending order",
				items1[len(items1)-1].ID, items2[0].ID)
		}
	})

	t.Run("filter matching zero rows short-circuits", func(t *testing.T) {
		page := PageOptions{Limit: 4, OrderBy: orderBy}
		query := fmt.Sprintf("SELECT id, name, category FROM %s WHERE category = $1", paginationScratchTable)

		items, total, err := repo.SelectPaginated(ctx, page, query, "does-not-exist")
		if err != nil {
			t.Fatalf("expected nil error for a zero-row filter, got: %v", err)
		}
		if total != 0 {
			t.Fatalf("expected total 0, got %d", total)
		}
		if len(items) != 0 {
			t.Fatalf("expected no items, got %d", len(items))
		}
	})

	t.Run("WithoutTotal reports total -1 but still returns items", func(t *testing.T) {
		page := PageOptions{Limit: 4, OrderBy: orderBy, WithoutTotal: true}
		items, total, err := repo.SelectPaginated(ctx, page, baseQuery)
		if err != nil {
			t.Fatalf("WithoutTotal: %v", err)
		}
		if total != -1 {
			t.Fatalf("expected total -1 with WithoutTotal set, got %d", total)
		}
		if len(items) != 4 {
			t.Fatalf("expected 4 items, got %d", len(items))
		}
	})

	t.Run("OrderBy from Repository.OrderBy is honored descending", func(t *testing.T) {
		descOrderBy, err := repo.OrderBy("id", true)
		if err != nil {
			t.Fatalf("repo.OrderBy(descending): %v", err)
		}

		page := PageOptions{Limit: rowCount, OrderBy: descOrderBy}
		items, total, err := repo.SelectPaginated(ctx, page, baseQuery)
		if err != nil {
			t.Fatalf("descending page: %v", err)
		}
		if total != rowCount {
			t.Fatalf("expected total %d, got %d", rowCount, total)
		}
		if len(items) != rowCount {
			t.Fatalf("expected %d items, got %d", rowCount, len(items))
		}
		for i := 1; i < len(items); i++ {
			if items[i-1].ID <= items[i].ID {
				t.Fatalf("expected strictly descending ids, got %d then %d at index %d", items[i-1].ID, items[i].ID, i)
			}
		}
	})
}

// TestRepositorySelectWithTotal verifies SelectWithTotal against a query carrying its own
// COUNT(*) OVER() AS total_count window column, plus the zero-rows case.
func TestRepositorySelectWithTotal(t *testing.T) {
	db, err := openPaginationTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	if err = setupPaginationScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer dropTestTables(ctx, db, paginationScratchTable)

	const rowCount = 10
	if err = seedPaginationScratchTable(ctx, db, rowCount); err != nil {
		t.Fatal(err)
	}

	repo := NewRepository[paginationItem](db)

	t.Run("query with COUNT(*) OVER() AS total_count", func(t *testing.T) {
		query := fmt.Sprintf(
			"SELECT id, name, category, COUNT(*) OVER() AS total_count FROM %s ORDER BY id",
			paginationScratchTable)

		items, total, err := repo.SelectWithTotal(ctx, query)
		if err != nil {
			t.Fatalf("SelectWithTotal: %v", err)
		}
		if total != rowCount {
			t.Fatalf("expected total %d, got %d", rowCount, total)
		}
		if len(items) != rowCount {
			t.Fatalf("expected %d items, got %d", rowCount, len(items))
		}
		for _, it := range items {
			if it.Name == "" {
				t.Fatalf("expected every item's Name to be populated (not routed to the total capture), got %#v", it)
			}
		}
	})

	t.Run("zero rows", func(t *testing.T) {
		query := fmt.Sprintf(
			"SELECT id, name, category, COUNT(*) OVER() AS total_count FROM %s WHERE category = $1",
			paginationScratchTable)

		items, total, err := repo.SelectWithTotal(ctx, query, "does-not-exist")
		if err != nil {
			t.Fatalf("expected nil error for a zero-row query, got: %v", err)
		}
		if total != 0 {
			t.Fatalf("expected total 0, got %d", total)
		}
		if len(items) != 0 {
			t.Fatalf("expected no items, got %d", len(items))
		}
	})
}
