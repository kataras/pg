package pg

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestSelectIter|TestQueryIter' -v .

// iterScratchItem is a scratch entity registered only for this file's tests, mirroring the
// pattern paginationItem (pagination_live_test.go) already established: plain DDL for the
// table, raw db.Exec for seeding, so these tests stay confined to their own scratch table
// instead of touching (or needing to reason about) the wider schema via
// DB.CreateSchema/DeleteSchema.
type iterScratchItem struct {
	ID   int64  `pg:"type=bigint,primary"`
	Name string `pg:"type=varchar(255)"`
}

const iterScratchTable = "iter_scratch_items"

// openIterTestConnection opens a *DB whose schema has only iterScratchItem registered; it does
// not create the table itself, see setupIterScratchTable.
func openIterTestConnection() (*DB, error) {
	schema := NewSchema()
	schema.MustRegister(iterScratchTable, iterScratchItem{})

	return Open(context.Background(), schema, getTestConnString())
}

// openIterTestConnectionPoolSize is openIterTestConnection but with the connection pool
// constrained to exactly n connections. TestSelectIterEarlyBreakReleasesConnection uses n=1 to
// turn "the connection was released back to the pool" into an observable, deterministic fact
// (a second query can only possibly succeed if the one and only connection was actually handed
// back) instead of a coincidence of the (otherwise multi-connection) pool happening to have a
// spare connection regardless of whether the first one leaked.
func openIterTestConnectionPoolSize(n int) (*DB, error) {
	schema := NewSchema()
	schema.MustRegister(iterScratchTable, iterScratchItem{})

	return Open(context.Background(), schema, withPoolMaxConns(getTestConnString(), n))
}

// withPoolMaxConns appends a pool_max_conns setting to connString, in whichever of the two
// connection-string forms Open/pgxpool.ParseConfig accept: a "postgres://" URL, or the
// space-separated "key=value ..." DSN form that getTestConnString's own default (and its
// PG_CONNSTRING override, as documented there) both use.
func withPoolMaxConns(connString string, n int) string {
	param := fmt.Sprintf("pool_max_conns=%d", n)

	if strings.HasPrefix(connString, "postgres://") || strings.HasPrefix(connString, "postgresql://") {
		sep := "?"
		if strings.Contains(connString, "?") {
			sep = "&"
		}
		return connString + sep + param
	}

	return connString + " " + param
}

// setupIterScratchTable (re)creates iterScratchTable with plain DDL. The "tag" column is not
// part of iterScratchItem and is never selected by the struct-decoding tests (their queries
// name columns explicitly); it exists only for TestQueryIterIncludesEmptyStrings.
func setupIterScratchTable(ctx context.Context, db *DB) error {
	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+iterScratchTable); err != nil {
		return err
	}

	_, err := db.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (id BIGSERIAL PRIMARY KEY, name VARCHAR(255) NOT NULL, tag TEXT NOT NULL DEFAULT '')",
		iterScratchTable))
	return err
}

// seedIterScratchTable inserts n rows named "item-0".."item-<n-1>", with an empty tag for even
// i and a non-empty "tag-<i>" for odd i, giving TestQueryIterIncludesEmptyStrings both empty
// and non-empty values to tell QueryIter's behavior apart from QuerySlice's.
func seedIterScratchTable(ctx context.Context, db *DB, n int) error {
	for i := 0; i < n; i++ {
		tag := ""
		if i%2 == 1 {
			tag = fmt.Sprintf("tag-%d", i)
		}

		if _, err := db.Exec(ctx, fmt.Sprintf("INSERT INTO %s (name, tag) VALUES ($1, $2)", iterScratchTable),
			fmt.Sprintf("item-%d", i), tag); err != nil {
			return err
		}
	}

	return nil
}

// TestSelectIterRoundTrip verifies that collecting every (value, nil) pair SelectIter yields
// produces the exact same rows, in the exact same order, as Select over the identical query.
func TestSelectIterRoundTrip(t *testing.T) {
	db, err := openIterTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := setupIterScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP TABLE IF EXISTS "+iterScratchTable)

	const n = 100
	if err := seedIterScratchTable(ctx, db, n); err != nil {
		t.Fatal(err)
	}

	repo := NewRepository[iterScratchItem](db)
	query := fmt.Sprintf("SELECT id, name FROM %s ORDER BY id", iterScratchTable)

	want, err := repo.Select(ctx, query)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(want) != n {
		t.Fatalf("expected %d rows from Select, got %d", n, len(want))
	}

	var got []iterScratchItem
	for value, err := range repo.SelectIter(ctx, query) {
		if err != nil {
			t.Fatalf("SelectIter: %v", err)
		}
		got = append(got, value)
	}

	if len(got) != len(want) {
		t.Fatalf("expected SelectIter to yield %d rows (matching Select), got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d: SelectIter %#v != Select %#v", i, got[i], want[i])
		}
	}
}

// TestSelectIterEarlyBreakReleasesConnection verifies that breaking out of a SelectIter loop
// early actually releases the connection it was holding, rather than merely appearing to (e.g.
// because the pool had another connection free to mask a leak). It forces this with a
// pool_max_conns=1 *DB: the follow-up query after the break can only succeed if the one and
// only connection came back.
func TestSelectIterEarlyBreakReleasesConnection(t *testing.T) {
	db, err := openIterTestConnectionPoolSize(1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := setupIterScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP TABLE IF EXISTS "+iterScratchTable)

	const n = 100
	if err := seedIterScratchTable(ctx, db, n); err != nil {
		t.Fatal(err)
	}

	repo := NewRepository[iterScratchItem](db)
	query := fmt.Sprintf("SELECT id, name FROM %s ORDER BY id", iterScratchTable)

	var seen int
	for _, err := range repo.SelectIter(ctx, query) {
		if err != nil {
			t.Fatalf("SelectIter: %v", err)
		}
		seen++
		if seen == 3 {
			break
		}
	}
	if seen != 3 {
		t.Fatalf("expected to break after exactly 3 rows, got %d", seen)
	}

	// With pool_max_conns=1, this second query can only succeed if the early break above
	// actually released the pool's one and only connection; a context deadline instead of a
	// clean result here would mean the connection leaked.
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	count, err := db.Count(queryCtx, fmt.Sprintf("SELECT COUNT(*) FROM %s", iterScratchTable))
	if err != nil {
		t.Fatalf("expected the connection to have been released so this query could run, got: %v", err)
	}
	if count != n {
		t.Fatalf("expected count %d, got %d", n, count)
	}
}

// TestSelectIterQueryError verifies that a query error (bad SQL) makes SelectIter yield
// exactly one (zero T, err) pair and then stop.
func TestSelectIterQueryError(t *testing.T) {
	db, err := openIterTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	repo := NewRepository[iterScratchItem](db)

	var (
		yields int
		gotErr error
	)
	for value, err := range repo.SelectIter(ctx, "SELECT id, name FROM this_table_does_not_exist_at_all_xyz") {
		yields++
		gotErr = err
		if value != (iterScratchItem{}) {
			t.Fatalf("expected the zero value alongside the error, got %#v", value)
		}
	}

	if yields != 1 {
		t.Fatalf("expected exactly 1 yield for a query error, got %d", yields)
	}
	if gotErr == nil {
		t.Fatal("expected a non-nil error")
	}
}

// TestQueryIterIncludesEmptyStrings verifies that QueryIter, unlike QuerySlice, does not skip
// empty-string results: QuerySlice[string] drops them (its documented quirk), while
// QueryIter[string] yields every row.
func TestQueryIterIncludesEmptyStrings(t *testing.T) {
	db, err := openIterTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := setupIterScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP TABLE IF EXISTS "+iterScratchTable)

	const n = 10 // tag: "" for even i (5 rows), "tag-<i>" for odd i (5 rows).
	if err := seedIterScratchTable(ctx, db, n); err != nil {
		t.Fatal(err)
	}

	query := fmt.Sprintf("SELECT tag FROM %s ORDER BY id", iterScratchTable)

	viaSlice, err := QuerySlice[string](ctx, db, query)
	if err != nil {
		t.Fatalf("QuerySlice: %v", err)
	}
	if len(viaSlice) != n/2 {
		t.Fatalf("expected QuerySlice to skip the %d empty-string rows (its documented quirk), got %d entries: %#v", n/2, len(viaSlice), viaSlice)
	}

	var viaIter []string
	for tag, err := range QueryIter[string](ctx, db, query) {
		if err != nil {
			t.Fatalf("QueryIter: %v", err)
		}
		viaIter = append(viaIter, tag)
	}
	if len(viaIter) != n {
		t.Fatalf("expected QueryIter to include every row (no skip-empty-string quirk), got %d entries: %#v", len(viaIter), viaIter)
	}
	for i, tag := range viaIter {
		wantEmpty := i%2 == 0
		if (tag == "") != wantEmpty {
			t.Fatalf("row %d: expected empty=%v, got tag=%q", i, wantEmpty, tag)
		}
	}
}
