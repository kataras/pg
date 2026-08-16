package desc

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestBuildCreateTableQueryGolden is the byte-identical golden test for BuildCreateTableQuery; see
// goldenAccount's doc comment (insert_query_test.go) for how the expected literal was captured.
func TestBuildCreateTableQueryGolden(t *testing.T) {
	td := goldenAccountTable(t)

	got := BuildCreateTableQuery(td)
	want := `CREATE TABLE IF NOT EXISTS golden_accounts ("id" uuid DEFAULT gen_random_uuid() NOT NULL, "email" varchar(255) NOT NULL, "nickname" varchar(64) DEFAULT null::character varying, "created_at" timestamp DEFAULT clock_timestamp() NOT NULL, PRIMARY KEY ("id"), CONSTRAINT golden_accounts_email_idx UNIQUE ("email"));`
	if got != want {
		t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

// TestBuildCreateTableQueryQuotesColumnNames is defense-in-depth coverage for the column-name
// quoting fix itself (desc/create_table_query.go ~:21), independent of ConvertStructToTable's
// identifier validation: even a manually-built *Table (bypassing ConvertStructToTable entirely,
// the way duplicate_query_test.go's TestBuildDuplicateQuery does) must get correctly ""-doubled
// quoting, not strconv.Quote's backslash escaping.
func TestBuildCreateTableQueryQuotesColumnNames(t *testing.T) {
	td := &Table{SearchPath: "public", Name: "manual_probe"}
	td.AddColumns(&Column{
		Name: `evil"column`,
		Type: Text,
	})

	got := BuildCreateTableQuery(td)

	wantColumnPart := pgx.Identifier{`evil"column`}.Sanitize()
	if !strings.Contains(got, wantColumnPart) {
		t.Fatalf("expected correctly quoted column %q in query, got: %s", wantColumnPart, got)
	}
	if strings.Contains(got, `\"`) {
		t.Fatalf("query still contains a backslash-escaped quote (strconv.Quote artifact): %s", got)
	}
}

// TestBuildCreateTableQueryQuotesUniqueIndexColumnNames mirrors
// TestBuildCreateTableQueryQuotesColumnNames for the unique-index column list quoting fix
// (desc/create_table_query.go ~:82), which is a separate code path from the column definition
// list above.
func TestBuildCreateTableQueryQuotesUniqueIndexColumnNames(t *testing.T) {
	td := &Table{SearchPath: "public", Name: "manual_probe2"}
	td.AddColumns(&Column{
		Name:        `evil"col2`,
		Type:        Text,
		UniqueIndex: "manual_probe2_idx",
	})

	got := BuildCreateTableQuery(td)

	wantColumnPart := pgx.Identifier{`evil"col2`}.Sanitize()
	if !strings.Contains(got, wantColumnPart) {
		t.Fatalf("expected correctly quoted unique-index column %q in query, got: %s", wantColumnPart, got)
	}
	if strings.Contains(got, `\"`) {
		t.Fatalf("query still contains a backslash-escaped quote (strconv.Quote artifact): %s", got)
	}
}
