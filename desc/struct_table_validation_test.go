package desc

import (
	"reflect"
	"strings"
	"testing"
)

// TestValidateIdentifier covers the safe-identifier charset that guards every tag-derived name
// (table, column and unique index) before it is interpolated into generated SQL.
func TestValidateIdentifier(t *testing.T) {
	valid := []string{"users", "_private", "MyColumn$2", "a", "col_1", "T1"}
	for _, v := range valid {
		if err := validateIdentifier(v); err != nil {
			t.Errorf("expected %q to be accepted as a valid identifier, got error: %v", v, err)
		}
	}

	invalid := []string{
		`bad"name`, // embedded double quote: could break out of a quoted identifier.
		"bad;name", // statement separator.
		"bad name", // space.
		"1bad",     // leading digit.
		"",         // empty.
		"naïve",    // non-ASCII / unicode.
		"bad`name", // backtick.
	}
	for _, v := range invalid {
		if err := validateIdentifier(v); err == nil {
			t.Errorf("expected %q to be rejected as an invalid identifier", v)
		}
	}
}

// validationProbe is a minimal, always-valid struct used to isolate the table-name identifier
// check in ConvertStructToTable from the column-name / unique-index-name checks below.
type validationProbe struct {
	F string `pg:"type=text"`
}

// TestConvertStructToTableRejectsUnsafeTableName is the Register-level rejection table for the
// table name: every unsafe name must produce a descriptive error, never reach a query builder.
func TestConvertStructToTableRejectsUnsafeTableName(t *testing.T) {
	typ := reflect.TypeFor[validationProbe]()

	invalidNames := []string{
		`bad"table`,
		"bad;table",
		"bad table",
		"1bad",
		"",
		"naïve",
		"bad`table",
	}
	for _, name := range invalidNames {
		t.Run(name, func(t *testing.T) {
			if _, err := ConvertStructToTable(name, typ); err == nil {
				t.Errorf("expected ConvertStructToTable(%q, ...) to fail for an unsafe table name", name)
			}
		})
	}

	if _, err := ConvertStructToTable("valid_table_name", typ); err != nil {
		t.Errorf("expected a valid table name to be accepted, got error: %v", err)
	}
}

// newSingleFieldStructType builds an anonymous struct type at runtime with a single exported
// string field "F" carrying the given raw `pg` struct tag. It lets the column-name and
// unique-index-name rejection tests below drive ConvertStructToTable with arbitrary tag values
// without declaring one named Go type per case.
func newSingleFieldStructType(tag string) reflect.Type {
	return reflect.StructOf([]reflect.StructField{
		{
			Name: "F",
			Type: reflect.TypeFor[string](),
			Tag:  reflect.StructTag(tag),
		},
	})
}

// TestConvertStructToTableRejectsUnsafeColumnName is the Register-level rejection table for the
// final column name (after tag resolution): every unsafe name must produce a descriptive error.
func TestConvertStructToTableRejectsUnsafeColumnName(t *testing.T) {
	invalidColumnNames := []string{
		"bad;col",
		"bad col",
		"1bad",
		"",
		"naïve",
		"bad`col",
	}
	for _, name := range invalidColumnNames {
		t.Run(name, func(t *testing.T) {
			typ := newSingleFieldStructType(`pg:"name=` + name + `,type=text"`)
			_, err := ConvertStructToTable("valid_table", typ)
			if err == nil {
				t.Errorf("expected ConvertStructToTable to fail for an unsafe column name %q", name)
				return
			}
			if !strings.Contains(err.Error(), "column name") {
				t.Errorf("expected the error to mention %q, got: %v", "column name", err)
			}
		})
	}
}

// TestConvertStructToTableRejectsColumnNameWithEmbeddedQuote covers the same rejection as
// TestConvertStructToTableRejectsUnsafeColumnName, but for a literal embedded double quote.
// Struct tags are themselves Go-double-quoted strings, so the embedded quote has to be written
// with the tag's own backslash escaping (\") to survive reflect.StructTag.Get intact. It is kept
// separate from the table-driven test above to keep that escaping localized and explained.
func TestConvertStructToTableRejectsColumnNameWithEmbeddedQuote(t *testing.T) {
	// Raw tag bytes: pg:"name=bad"col,type=text". The middle quote is backslash-escaped so
	// reflect.StructTag.Get("pg") unquotes it back to a literal '"' inside the "name=" value,
	// instead of prematurely closing the tag's quoted value.
	tag := "pg:\"name=bad\\\"col,type=text\""

	typ := newSingleFieldStructType(tag)
	_, err := ConvertStructToTable("valid_table", typ)
	if err == nil {
		t.Fatal(`expected ConvertStructToTable to fail for a column name containing an embedded '"'`)
	}
	if !strings.Contains(err.Error(), "column name") {
		t.Errorf("expected the error to mention %q, got: %v", "column name", err)
	}
}

// TestConvertStructToTableAcceptsValidMixedCaseColumnName proves the identifier check does not
// reject legitimate mixed-case, dollar-suffixed names.
func TestConvertStructToTableAcceptsValidMixedCaseColumnName(t *testing.T) {
	typ := newSingleFieldStructType(`pg:"name=MyColumn$2,type=text"`)
	td, err := ConvertStructToTable("valid_table", typ)
	if err != nil {
		t.Fatalf("expected a valid mixed-case column name to be accepted, got error: %v", err)
	}
	if len(td.Columns) != 1 || td.Columns[0].Name != "MyColumn$2" {
		t.Fatalf("expected column name %q, got table: %+v", "MyColumn$2", td)
	}
}

// TestConvertStructToTableRejectsUnsafeUniqueIndexName is the Register-level rejection table for
// the unique_index tag value: every unsafe name must produce a descriptive error. An empty value
// is deliberately not included here: it means "no unique index" (see ConvertStructToTable) and
// is legitimately skipped rather than rejected.
func TestConvertStructToTableRejectsUnsafeUniqueIndexName(t *testing.T) {
	invalidIndexNames := []string{
		"bad;idx",
		"bad idx",
		"1bad",
		"naïve",
		"bad`idx",
	}
	for _, name := range invalidIndexNames {
		t.Run(name, func(t *testing.T) {
			typ := newSingleFieldStructType(`pg:"type=text,unique_index=` + name + `"`)
			_, err := ConvertStructToTable("valid_table", typ)
			if err == nil {
				t.Errorf("expected ConvertStructToTable to fail for an unsafe unique index name %q", name)
				return
			}
			if !strings.Contains(err.Error(), "unique index name") {
				t.Errorf("expected the error to mention %q, got: %v", "unique index name", err)
			}
		})
	}
}

// TestConvertStructToTableRejectsUniqueIndexNameWithEmbeddedQuote mirrors
// TestConvertStructToTableRejectsColumnNameWithEmbeddedQuote for the unique_index tag value: it
// is interpolated unquoted as a CONSTRAINT name by BuildCreateTableQuery, so an embedded quote (or
// worse) must never reach it.
func TestConvertStructToTableRejectsUniqueIndexNameWithEmbeddedQuote(t *testing.T) {
	// Raw tag bytes: pg:"type=text,unique_index=bad"idx". See the sibling column-name test for
	// why the middle quote needs the tag's own backslash escaping.
	tag := "pg:\"type=text,unique_index=bad\\\"idx\""

	typ := newSingleFieldStructType(tag)
	_, err := ConvertStructToTable("valid_table", typ)
	if err == nil {
		t.Fatal(`expected ConvertStructToTable to fail for a unique index name containing an embedded '"'`)
	}
	if !strings.Contains(err.Error(), "unique index name") {
		t.Errorf("expected the error to mention %q, got: %v", "unique index name", err)
	}
}

// parenBadType carries a malformed "type=" tag where the right parenthesis appears before the
// left one. This used to panic with a negative-length slice bounds error (desc/struct_table.go,
// the "type" case). It must now return a descriptive error instead.
type parenBadType struct {
	F string `pg:"type=varchar)x(255"`
}

// parenGoodType carries a well-formed "type=" tag with a type argument, used as the control case
// alongside parenBadType.
type parenGoodType struct {
	F string `pg:"type=varchar(255)"`
}

// TestConvertStructToTableTypeArgumentParenParsing covers the "type=" tag parenthesis parsing fix:
// a malformed tag must return an error rather than panic, and a well-formed tag must still parse
// its type argument correctly.
func TestConvertStructToTableTypeArgumentParenParsing(t *testing.T) {
	t.Run("malformed parens return an error, not a panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf(`expected an error for pg:"type=varchar)x(255", got a panic instead: %v`, r)
			}
		}()

		_, err := ConvertStructToTable("paren_bad", reflect.TypeFor[parenBadType]())
		if err == nil {
			t.Fatal(`expected an error for pg:"type=varchar)x(255", got nil`)
		}
	})

	t.Run("well-formed parens still parse the type argument", func(t *testing.T) {
		td, err := ConvertStructToTable("paren_good", reflect.TypeFor[parenGoodType]())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(td.Columns) != 1 {
			t.Fatalf("expected 1 column, got %d", len(td.Columns))
		}

		c := td.Columns[0]
		if c.Type != CharacterVarying {
			t.Fatalf("expected type %v, got %v", CharacterVarying, c.Type)
		}
		if c.TypeArgument != "255" {
			t.Fatalf("expected TypeArgument %q, got %q", "255", c.TypeArgument)
		}
	})
}
