package desc

import (
	"strings"
	"testing"
)

// newOrderByTestTable builds a small table for OrderBy tests without going through
// ConvertStructToTable; only Name and PrimaryKey matter to OrderBy's logic.
func newOrderByTestTable(columns ...*Column) *Table {
	td := &Table{Name: "users"}
	td.AddColumns(columns...)
	return td
}

func TestTableOrderByValidColumn(t *testing.T) {
	td := newOrderByTestTable(
		&Column{Name: "id", PrimaryKey: true},
		&Column{Name: "email"},
	)

	got, err := td.OrderBy("email", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `"email" ASC`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestTableOrderByCaseInsensitiveMatch verifies that a caller-supplied column name is matched
// against the table's columns case-insensitively (consistent with GetColumnByName) but the
// returned fragment quotes the descriptor's own, canonical casing, not the caller's.
func TestTableOrderByCaseInsensitiveMatch(t *testing.T) {
	td := newOrderByTestTable(
		&Column{Name: "email", PrimaryKey: false},
	)

	got, err := td.OrderBy("EMAIL", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `"email" ASC`
	if got != want {
		t.Fatalf("got %q, want %q (expected canonical descriptor casing, not caller casing)", got, want)
	}
}

func TestTableOrderByDescending(t *testing.T) {
	td := newOrderByTestTable(&Column{Name: "email"})

	got, err := td.OrderBy("email", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `"email" DESC`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTableOrderByUnknownColumn(t *testing.T) {
	td := newOrderByTestTable(&Column{Name: "email"})

	_, err := td.OrderBy("does_not_exist", false)
	if err == nil {
		t.Fatal("expected an error for an unknown column, got nil")
	}

	if !strings.Contains(err.Error(), "does_not_exist") {
		t.Fatalf("expected the error to name the offending column, got: %v", err)
	}

	// The full allowlist must not be echoed back into the error.
	if strings.Contains(err.Error(), "email") {
		t.Fatalf("error should not leak the table's other column names, got: %v", err)
	}
}

// TestTableOrderByExtraColumns verifies that a column exposed only as a computed/aliased
// expression in the SELECT list, not present in td.Columns, is accepted via extraColumns.
func TestTableOrderByExtraColumns(t *testing.T) {
	td := newOrderByTestTable(&Column{Name: "id", PrimaryKey: true})

	got, err := td.OrderBy("full_name", false, "full_name", "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `"full_name" ASC`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestTableOrderByExtraColumnsExactMatch verifies that extraColumns membership is exact
// (case-sensitive), unlike the case-insensitive match against the table's own columns.
func TestTableOrderByExtraColumnsExactMatch(t *testing.T) {
	td := newOrderByTestTable(&Column{Name: "id", PrimaryKey: true})

	_, err := td.OrderBy("Full_Name", false, "full_name")
	if err == nil {
		t.Fatal("expected an error: extraColumns membership should be case-sensitive, exact match only")
	}
}

// TestTableOrderByExtraColumnsRejectsDottedEntry verifies that a schema/alias-qualified
// extraColumns entry (containing a dot) is rejected outright with a descriptive error, even
// though the requested column ("id") is otherwise perfectly valid and unrelated to it -
// extraColumns entries are validated up front, not only when they end up matching column.
func TestTableOrderByExtraColumnsRejectsDottedEntry(t *testing.T) {
	td := newOrderByTestTable(&Column{Name: "id", PrimaryKey: true})

	_, err := td.OrderBy("id", false, "t.name")
	if err == nil {
		t.Fatal("expected an error for a dotted (schema/alias-qualified) extraColumns entry, got nil")
	}

	if !strings.Contains(err.Error(), "t.name") {
		t.Fatalf("expected the error to name the offending entry %q, got: %v", "t.name", err)
	}
}

// TestTableOrderByExtraColumnsRejectsWhitespaceEntry mirrors
// TestTableOrderByExtraColumnsRejectsDottedEntry for an extraColumns entry containing embedded
// whitespace.
func TestTableOrderByExtraColumnsRejectsWhitespaceEntry(t *testing.T) {
	td := newOrderByTestTable(&Column{Name: "id", PrimaryKey: true})

	_, err := td.OrderBy("id", false, "full name")
	if err == nil {
		t.Fatal("expected an error for an extraColumns entry containing whitespace, got nil")
	}

	if !strings.Contains(err.Error(), "full name") {
		t.Fatalf("expected the error to name the offending entry %q, got: %v", "full name", err)
	}
}

// TestTableOrderByExtraColumnsRejectsEmptyEntry mirrors the two tests above for an empty-string
// extraColumns entry. Note this is distinct from column itself being empty (which triggers the
// fallback chain instead): here column is "id" and it is one of the extraColumns entries that is
// blank.
func TestTableOrderByExtraColumnsRejectsEmptyEntry(t *testing.T) {
	td := newOrderByTestTable(&Column{Name: "id", PrimaryKey: true})

	_, err := td.OrderBy("id", false, "")
	if err == nil {
		t.Fatal("expected an error for an empty extraColumns entry, got nil")
	}
}

// TestTableOrderByMixedCaseQuoting verifies a mixed-case caller input is still resolved and
// that the returned fragment is properly quoted (double-quoted identifier).
func TestTableOrderByMixedCaseQuoting(t *testing.T) {
	td := newOrderByTestTable(&Column{Name: "created_at"})

	got, err := td.OrderBy("Created_At", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `"created_at" DESC`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestTableOrderByFallbackCreatedAt verifies that an empty column falls back to created_at
// when the table has one, even if updated_at and a primary key also exist.
func TestTableOrderByFallbackCreatedAt(t *testing.T) {
	td := newOrderByTestTable(
		&Column{Name: "id", PrimaryKey: true},
		&Column{Name: "created_at"},
		&Column{Name: "updated_at"},
	)

	got, err := td.OrderBy("", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `"created_at" ASC`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestTableOrderByFallbackUpdatedAt verifies the second link of the fallback chain: no
// created_at, but an updated_at column exists.
func TestTableOrderByFallbackUpdatedAt(t *testing.T) {
	td := newOrderByTestTable(
		&Column{Name: "id", PrimaryKey: true},
		&Column{Name: "updated_at"},
	)

	got, err := td.OrderBy("", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `"updated_at" DESC`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestTableOrderByFallbackPrimaryKey verifies the last link of the fallback chain: neither
// created_at nor updated_at exist, so the primary key is used.
func TestTableOrderByFallbackPrimaryKey(t *testing.T) {
	td := newOrderByTestTable(
		&Column{Name: "id", PrimaryKey: true},
		&Column{Name: "email"},
	)

	got, err := td.OrderBy("", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `"id" ASC`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestTableOrderByNoFallbackAvailable verifies that an empty column on a table with neither
// created_at, updated_at nor a primary key column returns a descriptive error instead of
// silently picking an arbitrary column.
func TestTableOrderByNoFallbackAvailable(t *testing.T) {
	td := newOrderByTestTable(&Column{Name: "email"})

	_, err := td.OrderBy("", false)
	if err == nil {
		t.Fatal("expected an error when no fallback column is available, got nil")
	}

	if !strings.Contains(err.Error(), td.Name) {
		t.Fatalf("expected the error to name the table, got: %v", err)
	}
}
