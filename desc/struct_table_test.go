package desc

import (
	"errors"
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
