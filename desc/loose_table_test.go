package desc

import (
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// looseChild is a plain nested struct used as the pointee/element type for the JSON-wrap
// assertions in TestLooseTableFields.
type looseChild struct {
	ID int64
}

// customLooseScanner implements sql.Scanner via a pointer receiver only, so implementsScanner
// reports true for the value type too (see struct_table.go's own convertStructFieldToColumnDefinion,
// which relies on exactly the same asymmetry).
type customLooseScanner struct{ Raw string }

func (c *customLooseScanner) Scan(src any) error { return nil }

// looseAllFields exercises every LooseTable rule in one struct: name precedence, pg:"-"/json:"-"
// skipping, unexported-field skipping, and JSON-wrap marking (including its exclusions).
type looseAllFields struct {
	//nolint:unused // The point of the field is that LooseTable skips it; nothing reads it.
	unexported string // must never produce a column.

	PGName   string `pg:"name=custom_pg_name" json:"json_name"` // pg name= must win over json name.
	JSONOnly string `json:"json_only_name,omitempty"`           // no pg tag: json tag name wins.
	Plain    string // neither tag: SnakeCase(field name).

	PGDash   string `pg:"-" json:"should_not_matter"` // pg:"-" skips regardless of json tag.
	JSONDash string `json:"-"`                        // json:"-" skips when there's no pg tag.

	StructPtr  *looseChild    // JSON-wrap eligible (deref'd kind struct).
	StructVal  looseChild     // JSON-wrap eligible (kind struct).
	MapField   map[string]any // JSON-wrap eligible (kind map).
	SliceField []looseChild   // JSON-wrap eligible (kind slice, non-byte element).
	ByteSlice  []byte         // excluded: []byte is bytea, not JSON.
	BytePtr    *[]byte        // excluded: *[]byte derefs to []byte.
	When       time.Time      // excluded: time.Time is a native timestamp column.
	WhenPtr    *time.Time     // excluded: derefs to time.Time.

	Scanner customLooseScanner // excluded: implements sql.Scanner (via pointer receiver).

	// Neither pgtype.Range[T] nor pgtype.Array[T] implements sql.Scanner, but pgx's own driver
	// already scans them directly (that's their entire purpose); both must be excluded from
	// JSON-wrap via their package path, not the sql.Scanner check.
	RangeField pgtype.Range[int32]
	ArrayField pgtype.Array[string]

	// BareTag carries a bare, comma-less pg tag that isn't the "name=" form LooseTable honors;
	// it must fall through to SnakeCase(field name) ("bare_tag") rather than being misread as a
	// column literally named "unique".
	BareTag string `pg:"unique"`
}

// mustLooseColumn fetches the column named colName from td, failing the test if it's missing.
func mustLooseColumn(t *testing.T, td *Table, colName string) *Column {
	t.Helper()
	col := td.GetColumnByName(colName)
	if col == nil {
		t.Fatalf("expected a column named %q, got columns: %v", colName, td.ListColumnNames())
	}
	return col
}

// TestLooseTableFields drives LooseTable(looseAllFields{}) and checks every documented rule
// against the resulting *Table in one pass: name precedence (including a bare, non-"name="
// pg tag correctly falling through instead of being read as a literal column name),
// pg:"-"/json:"-" skipping, the unexported-field skip, JSON-wrap marking (struct/map/slice,
// pointer and non-pointer), the time.Time/[]byte/sql.Scanner/pgtype exclusions from JSON-wrap,
// and the table-level shape (non-strict, TableTypePresenter) LooseTable documents.
func TestLooseTableFields(t *testing.T) {
	td, err := LooseTable(reflect.TypeOf(looseAllFields{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("table shape", func(t *testing.T) {
		if td.Strict {
			t.Fatal("expected a non-strict table (Strict must be false, its zero value)")
		}
		if td.Type != TableTypePresenter {
			t.Fatalf("expected Type TableTypePresenter, got %v", td.Type)
		}
		if td.StructName != "looseAllFields" {
			t.Fatalf("expected StructName %q, got %q", "looseAllFields", td.StructName)
		}
	})

	t.Run("unexported field produces no column", func(t *testing.T) {
		if col := td.GetColumnByName("unexported"); col != nil {
			t.Fatalf("expected no column for the unexported field, got %v", col)
		}
	})

	t.Run("name precedence: pg name= beats json name", func(t *testing.T) {
		col := mustLooseColumn(t, td, "custom_pg_name")
		if col.FieldName != "PGName" {
			t.Fatalf("expected column %q to map to field PGName, got %q", "custom_pg_name", col.FieldName)
		}
		if got := td.GetColumnByName("json_name"); got != nil {
			t.Fatalf("expected no column named %q (pg name= must win), got %v", "json_name", got)
		}
	})

	t.Run("name precedence: json name beats snake_case", func(t *testing.T) {
		col := mustLooseColumn(t, td, "json_only_name")
		if col.FieldName != "JSONOnly" {
			t.Fatalf("expected column %q to map to field JSONOnly, got %q", "json_only_name", col.FieldName)
		}
	})

	t.Run("name precedence: snake_case fallback", func(t *testing.T) {
		col := mustLooseColumn(t, td, "plain")
		if col.FieldName != "Plain" {
			t.Fatalf("expected column %q to map to field Plain, got %q", "plain", col.FieldName)
		}
	})

	t.Run("pg:\"-\" skips the field", func(t *testing.T) {
		if col := td.GetColumnByName("should_not_matter"); col != nil {
			t.Fatalf("expected pg:\"-\" to skip the field regardless of its json tag, got %v", col)
		}
		if col := td.GetColumnByName("pg_dash"); col != nil {
			t.Fatalf("expected pg:\"-\" to skip the field entirely, got %v", col)
		}
	})

	t.Run("json:\"-\" skips the field when there's no pg tag", func(t *testing.T) {
		if col := td.GetColumnByName("json_dash"); col != nil {
			t.Fatalf("expected json:\"-\" to skip the field, got %v", col)
		}
	})

	t.Run("bare non-name= pg tag falls through to snake_case, not a literal name", func(t *testing.T) {
		col := mustLooseColumn(t, td, "bare_tag")
		if col.FieldName != "BareTag" {
			t.Fatalf("expected column %q to map to field BareTag, got %q", "bare_tag", col.FieldName)
		}
		if got := td.GetColumnByName("unique"); got != nil {
			t.Fatalf("expected the bare tag %q not to be read as a literal column name, got %v", "unique", got)
		}
	})

	jsonWrapCases := []string{"struct_ptr", "struct_val", "map_field", "slice_field"}
	for _, name := range jsonWrapCases {
		t.Run("JSON-wrap marks "+name+" as JSONB", func(t *testing.T) {
			col := mustLooseColumn(t, td, name)
			if col.Type != JSONB {
				t.Fatalf("expected column %q to be marked JSONB, got %v", name, col.Type)
			}
		})
	}

	notJSONWrapCases := []string{"byte_slice", "byte_ptr", "when", "when_ptr", "scanner", "range_field", "array_field"}
	for _, name := range notJSONWrapCases {
		t.Run(name+" is excluded from JSON-wrap", func(t *testing.T) {
			col := mustLooseColumn(t, td, name)
			if col.Type == JSONB {
				t.Fatalf("expected column %q NOT to be marked JSONB, got %v", name, col.Type)
			}
		})
	}
}

// TestLooseTableStructPtrIsPtr specifically checks the isPtr flag on the struct_ptr column,
// which is what makes findScanTargets (desc/scanner.go) skip the package's own jsonScanner for
// it and fall through to the default Addr().Interface() scan target instead, relying on pgx's
// built-in generic JSON decode for a pointer-to-pointer destination, as documented on LooseTable.
func TestLooseTableStructPtrIsPtr(t *testing.T) {
	td, err := LooseTable(reflect.TypeOf(looseAllFields{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	col := mustLooseColumn(t, td, "struct_ptr")
	if !col.isPtr {
		t.Fatal("expected struct_ptr's column to have isPtr true")
	}

	col = mustLooseColumn(t, td, "struct_val")
	if col.isPtr {
		t.Fatal("expected struct_val's column to have isPtr false")
	}
}

// TestLooseTableScannerFieldIsMarkedScanner verifies a field whose type implements sql.Scanner
// (here via a pointer receiver, so only *customLooseScanner satisfies the interface directly) is
// recorded as such and therefore excluded from JSON-wrap even though its kind is struct.
func TestLooseTableScannerFieldIsMarkedScanner(t *testing.T) {
	td, err := LooseTable(reflect.TypeOf(looseAllFields{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	col := mustLooseColumn(t, td, "scanner")
	if !col.isScanner {
		t.Fatal("expected the scanner column to be marked isScanner")
	}
	if col.Type == JSONB {
		t.Fatal("expected a sql.Scanner-implementing field not to be marked JSONB")
	}
}

// TestLooseTableCachesPerType verifies LooseTable returns the exact same *Table instance for
// repeated calls with the same type, and that a pointer type shares its pointee's cache entry.
func TestLooseTableCachesPerType(t *testing.T) {
	typ := reflect.TypeOf(looseChild{})

	first, err := LooseTable(typ)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	second, err := LooseTable(typ)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first != second {
		t.Fatal("expected LooseTable to return the same cached *Table instance for the same type")
	}

	third, err := LooseTable(reflect.TypeOf(&looseChild{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if third != first {
		t.Fatal("expected LooseTable(*T) to resolve to T's cache entry")
	}
}

// TestIsLooseJSONFieldExcludesPgtypeTypes unit-tests isLooseJSONField directly against
// pgtype.Range[T] and pgtype.Array[T] (github.com/jackc/pgx/v5/pgtype): both are ordinary structs
// (not time.Time) that do NOT implement sql.Scanner, so the sql.Scanner check alone would not
// exclude them. It's the package-path check (pgxPgtypePackagePath) that must. A plain struct
// (looseChild) is included as a control to confirm the package-path check doesn't over-exclude.
func TestIsLooseJSONFieldExcludesPgtypeTypes(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		want bool
	}{
		{"pgtype.Range[int32]", reflect.TypeOf(pgtype.Range[int32]{}), false},
		{"pgtype.Array[string]", reflect.TypeOf(pgtype.Array[string]{}), false},
		{"*pgtype.Range[int32] (pointer)", reflect.TypeOf(&pgtype.Range[int32]{}), false},
		{"plain struct (control)", reflect.TypeOf(looseChild{}), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLooseJSONField(tc.typ); got != tc.want {
				t.Fatalf("isLooseJSONField(%s) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}

// TestLooseTableNonStructError verifies LooseTable rejects any type that isn't a struct (after
// dereferencing a pointer).
func TestLooseTableNonStructError(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(42),
		reflect.TypeOf("a string"),
		reflect.TypeOf([]int{}),
		reflect.TypeOf(map[string]int{}),
	} {
		t.Run(typ.String(), func(t *testing.T) {
			if _, err := LooseTable(typ); err == nil {
				t.Fatalf("expected an error for non-struct type %s", typ)
			}
		})
	}
}
