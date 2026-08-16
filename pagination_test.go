package pg

import (
	"reflect"
	"testing"
)

// TestBuildPaginatedQuery exercises buildPaginatedQuery's SQL assembly and bind-numbering rules
// directly, without a live database: see pagination_live_test.go for the round-trip tests.
func TestBuildPaginatedQuery(t *testing.T) {
	t.Run("limit only", func(t *testing.T) {
		query, args := buildPaginatedQuery("SELECT * FROM t", PageOptions{Limit: 5}, 1)

		wantQuery := "SELECT * FROM t LIMIT $1"
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}

		wantArgs := []any{int64(5)}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args: got %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("offset only", func(t *testing.T) {
		query, args := buildPaginatedQuery("SELECT * FROM t", PageOptions{Offset: 20}, 1)

		wantQuery := "SELECT * FROM t OFFSET $1"
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}

		wantArgs := []any{int64(20)}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args: got %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("both limit and offset", func(t *testing.T) {
		query, args := buildPaginatedQuery("SELECT * FROM t", PageOptions{Limit: 5, Offset: 20}, 1)

		wantQuery := "SELECT * FROM t LIMIT $1 OFFSET $2"
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}

		wantArgs := []any{int64(5), int64(20)}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args: got %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("order by only", func(t *testing.T) {
		query, args := buildPaginatedQuery("SELECT * FROM t", PageOptions{OrderBy: `"id" DESC`}, 1)

		wantQuery := `SELECT * FROM t ORDER BY "id" DESC`
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}

		if len(args) != 0 {
			t.Fatalf("expected no extra args, got %#v", args)
		}
	})

	t.Run("order by, limit and offset all together", func(t *testing.T) {
		query, args := buildPaginatedQuery("SELECT * FROM t", PageOptions{OrderBy: `"id" DESC`, Limit: 5, Offset: 20}, 1)

		wantQuery := `SELECT * FROM t ORDER BY "id" DESC LIMIT $1 OFFSET $2`
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}

		wantArgs := []any{int64(5), int64(20)}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args: got %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("none of order by, limit or offset", func(t *testing.T) {
		query, args := buildPaginatedQuery("SELECT * FROM t", PageOptions{}, 1)

		wantQuery := "SELECT * FROM t"
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}

		if len(args) != 0 {
			t.Fatalf("expected no extra args, got %#v", args)
		}
	})

	// Zero and negative Limit/Offset must both add no clause, per PageOptions' documented
	// "zero or negative adds no LIMIT/OFFSET" contract.
	t.Run("zero and negative limit/offset add no clause", func(t *testing.T) {
		query, args := buildPaginatedQuery("SELECT * FROM t", PageOptions{Limit: 0, Offset: -1}, 1)

		wantQuery := "SELECT * FROM t"
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}

		if len(args) != 0 {
			t.Fatalf("expected no extra args, got %#v", args)
		}
	})

	// A trailing semicolon and/or whitespace on the incoming query must be trimmed before the
	// appended clauses, or the assembled SQL would be invalid ("SELECT * FROM t; LIMIT $1").
	t.Run("trims a trailing semicolon and whitespace", func(t *testing.T) {
		query, args := buildPaginatedQuery("SELECT * FROM t ;  \n", PageOptions{Limit: 5}, 1)

		wantQuery := "SELECT * FROM t LIMIT $1"
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}

		wantArgs := []any{int64(5)}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args: got %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("trims a trailing semicolon with no surrounding whitespace", func(t *testing.T) {
		query, _ := buildPaginatedQuery("SELECT * FROM t;", PageOptions{}, 1)

		wantQuery := "SELECT * FROM t"
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}
	})

	// The heart of buildPaginatedQuery's contract: bind numbering must continue from
	// startIndex, not restart at $1, so LIMIT/OFFSET don't collide with the caller's own query
	// parameters.
	t.Run("bind numbering continues from startIndex", func(t *testing.T) {
		query, args := buildPaginatedQuery(
			"SELECT * FROM t WHERE a = $1 AND b = $2 AND c = $3",
			PageOptions{Limit: 10, Offset: 30},
			4, // len(args)+1 for 3 existing args.
		)

		wantQuery := "SELECT * FROM t WHERE a = $1 AND b = $2 AND c = $3 LIMIT $4 OFFSET $5"
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}

		wantArgs := []any{int64(10), int64(30)}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args: got %#v, want %#v", args, wantArgs)
		}
	})

	// Same as above but with only OFFSET appended, to confirm startIndex (not a hardcoded
	// $<startIndex+1>) drives the single placeholder's number too.
	t.Run("bind numbering continues from startIndex, offset only", func(t *testing.T) {
		query, args := buildPaginatedQuery(
			"SELECT * FROM t WHERE a = $1",
			PageOptions{Offset: 30},
			2, // len(args)+1 for 1 existing arg.
		)

		wantQuery := "SELECT * FROM t WHERE a = $1 OFFSET $2"
		if query != wantQuery {
			t.Fatalf("query: got %q, want %q", query, wantQuery)
		}

		wantArgs := []any{int64(30)}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args: got %#v, want %#v", args, wantArgs)
		}
	})
}

// TestTrimQuery exercises trimQuery directly (buildPaginatedQuery and SelectPaginated both rely
// on it) across the whitespace/semicolon combinations it must handle.
func TestTrimQuery(t *testing.T) {
	cases := map[string]string{
		"SELECT 1":            "SELECT 1",
		"SELECT 1;":           "SELECT 1",
		"SELECT 1 ;":          "SELECT 1",
		"SELECT 1;   ":        "SELECT 1",
		"SELECT 1  ;  \n":     "SELECT 1",
		"SELECT 1\n":          "SELECT 1",
		"SELECT 1;;":          "SELECT 1",
		"SELECT 1 FROM t; ; ": "SELECT 1 FROM t",
	}

	for in, want := range cases {
		if got := trimQuery(in); got != want {
			t.Fatalf("trimQuery(%q): got %q, want %q", in, got, want)
		}
	}
}
