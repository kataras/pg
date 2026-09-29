package desc

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseReferenceTagValue(t *testing.T) {
	// Define some test cases with different input values and expected outputs.
	testCases := []struct {
		input          string
		refTableName   string
		refColumnName  string
		onDeleteAction string
		isDeferrable   bool
		err            error
	}{
		// Valid cases.
		{"blogs(id no action deferrable)", "blogs", "id", "NO ACTION", true, nil},
		{"blogs(id no action)", "blogs", "id", "NO ACTION", false, nil},
		{"blogs(id)", "blogs", "id", "CASCADE", false, nil},
		{"blogs(id cascade)", "blogs", "id", "CASCADE", false, nil},
		{"blogs(id set null deferrable)", "blogs", "id", "SET NULL", true, nil},
		{"blogs(id set default)", "blogs", "id", "SET DEFAULT", false, nil},
		{"users(id no action deferrable)", "users", "id", "NO ACTION", true, nil},
		// Invalid cases.
		{"blogs(id foo)", "", "", "", false, errInvalidReferenceTag},
		{"blogs(id restrict deferrable)", "", "", "", false, errInvalidReferenceTag},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			// Call the function with the input value and get the output values.
			refTableName, refColumnName, onDeleteAction, isDeferrable, err := parseReferenceTagValue(tc.input)

			// Check if the output values match the expected values.
			if refTableName != tc.refTableName {
				t.Errorf("%s: expected refTableName to be %s, got %s", tc.input, tc.refTableName, refTableName)
			}

			if refColumnName != tc.refColumnName {
				t.Errorf("%s: expected refColumnName to be %s, got %s", tc.input, tc.refColumnName, refColumnName)
			}

			if onDeleteAction != tc.onDeleteAction {
				t.Errorf("%s: expected onDeleteAction to be %s, got %s", tc.input, tc.onDeleteAction, onDeleteAction)
			}

			if isDeferrable != tc.isDeferrable {
				t.Errorf("%s: expected isDeferrable to be %t, got %t", tc.input, tc.isDeferrable, isDeferrable)
			}

			if err != tc.err {
				if !errors.Is(err, tc.err) {
					t.Errorf("%s: expected err to be %v, got %v", tc.input, tc.err, err)
				}
			}
		})
	}
}

// A bare option that is not a boolean flag is the column-name shorthand
// (`pg:"id"` is name=id). That includes the value-taking option keys:
// `pg:"name,unique"` used to set the column name to "true".
func TestBareValueOptionIsNameShorthand(t *testing.T) {
	tests := []struct {
		tag    string
		name   string
		unique bool
		index  IndexType
	}{
		{tag: "name", name: "name"},
		{tag: "name,unique", name: "name", unique: true},
		{tag: "name,index", name: "name", index: Btree},
		{tag: "unique,name", name: "name", unique: true},
		{tag: "type", name: "type"},
		{tag: "default", name: "default"},
		{tag: "check", name: "check"},
		{tag: "generated", name: "generated"},
		{tag: "conflict", name: "conflict"},
		{tag: "ref", name: "ref"},
		{tag: "reference", name: "reference"},
		{tag: "references", name: "references"},
		{tag: "unique_index", name: "unique_index"},
		// Unchanged: plain names, boolean flags, bare index, explicit name=.
		{tag: "id", name: "id"},
		{tag: "unique", name: "field_value", unique: true},
		{tag: "index", name: "field_value", index: Btree},
		{tag: "name=title,unique", name: "title", unique: true},
	}
	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			field := reflect.StructField{Name: "FieldValue", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(`pg:"` + tt.tag + `"`)}
			c, err := convertStructFieldToColumnDefinion("things", field)
			if err != nil {
				t.Fatal(err)
			}
			if c.Name != tt.name || c.Unique != tt.unique || c.Index != tt.index {
				t.Fatalf("name %q unique %v index %v; want %q %v %v", c.Name, c.Unique, c.Index, tt.name, tt.unique, tt.index)
			}
			if c.Type != Text || c.Default != "" || c.CheckConstraint != "" || c.GeneratedExpression != "" || c.Conflict != "" || c.ReferenceTableName != "" || c.UniqueIndex != "" {
				t.Fatalf("a name shorthand also set an option: %+v", c)
			}
		})
	}
}
