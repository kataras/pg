package desc

import (
	"reflect"
	"testing"
)

// jsonScanChild has no json tags at all, which is the normal shape for a pg entity: fields are
// described with pg tags, and PostgreSQL emits lower-cased column names in a to_jsonb(x.*) or
// row_to_json(...) projection. encoding/json v1 matched those case-insensitively; v2 does not
// unless asked, so this type is the regression guard for jsonScanner's option set.
type jsonScanChild struct {
	ID        int64
	Name      string
	AlsoNamed string `pg:"also_named"`
}

// TestJSONScannerMatchesLowercaseKeys is the DB-free counterpart to the live to_jsonb scan
// tests. If jsonScanner ever loses json.MatchCaseInsensitiveNames, these fields silently stay
// at their zero values rather than erroring, which is the dangerous kind of regression: the
// query succeeds and the data is quietly missing.
func TestJSONScannerMatchesLowercaseKeys(t *testing.T) {
	for _, src := range []any{
		`{"id":7,"name":"parent-one","alsonamed":"x"}`,         // string, as modern pgx yields
		[]byte(`{"id":7,"name":"parent-one","alsonamed":"x"}`), // []byte, the older shape
	} {
		var child jsonScanChild
		field := reflect.ValueOf(&child).Elem()

		sc := &jsonScanner{fieldPtr: field}
		if err := sc.Scan(src); err != nil {
			t.Fatalf("Scan(%T): %v", src, err)
		}

		if child.ID != 7 {
			t.Fatalf("Scan(%T): expected ID 7 from a lower-cased \"id\" key, got %d", src, child.ID)
		}
		if child.Name != "parent-one" {
			t.Fatalf("Scan(%T): expected Name %q, got %q", src, "parent-one", child.Name)
		}
		if child.AlsoNamed != "x" {
			t.Fatalf("Scan(%T): expected AlsoNamed %q, got %q", src, "x", child.AlsoNamed)
		}
	}
}

// TestJSONScannerNullIsNoOp pins the SQL NULL behavior: the field keeps its zero value and no
// error is reported, which is what lets a non-pointer JSON/JSONB field tolerate a NULL column.
func TestJSONScannerNullIsNoOp(t *testing.T) {
	child := jsonScanChild{Name: "untouched"}
	field := reflect.ValueOf(&child).Elem()

	sc := &jsonScanner{fieldPtr: field}
	if err := sc.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}

	if child.Name != "untouched" {
		t.Fatalf("expected a NULL scan to leave the field alone, got %q", child.Name)
	}
}
