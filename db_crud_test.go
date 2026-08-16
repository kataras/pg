package pg

import "testing"

// TestParseColValPairsOddCount verifies that an odd-length colValPairs slice (e.g. a trailing
// column name with no value) is rejected before ever touching the database or the Schema.
func TestParseColValPairsOddCount(t *testing.T) {
	_, _, err := parseColValPairs([]any{"col1", 1, "col2"})
	if err == nil {
		t.Fatal("expected an error for an odd number of colValPairs arguments, got nil")
	}
}

// TestParseColValPairsNonStringKey verifies that a pair whose key (the even-indexed argument)
// is not a string is rejected before ever touching the database or the Schema.
func TestParseColValPairsNonStringKey(t *testing.T) {
	_, _, err := parseColValPairs([]any{"col1", 1, 2, "not-a-column-name"})
	if err == nil {
		t.Fatal("expected an error for a non-string column key, got nil")
	}
}

// TestParseColValPairsEmpty verifies the documented zero-pairs case: no error, and empty
// (nil-length) cols/vals slices.
func TestParseColValPairsEmpty(t *testing.T) {
	cols, vals, err := parseColValPairs(nil)
	if err != nil {
		t.Fatalf("unexpected error for zero pairs: %v", err)
	}
	if len(cols) != 0 {
		t.Fatalf("expected zero columns, got %v", cols)
	}
	if len(vals) != 0 {
		t.Fatalf("expected zero values, got %v", vals)
	}
}

// TestParseColValPairsValid verifies the happy path: cols and vals are split out in the same
// order the pairs were given in.
func TestParseColValPairsValid(t *testing.T) {
	cols, vals, err := parseColValPairs([]any{"col1", 1, "col2", "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCols := []string{"col1", "col2"}
	if len(cols) != len(wantCols) || cols[0] != wantCols[0] || cols[1] != wantCols[1] {
		t.Fatalf("cols = %v, want %v", cols, wantCols)
	}

	if len(vals) != 2 || vals[0] != 1 || vals[1] != "x" {
		t.Fatalf("vals = %v, want [1 x]", vals)
	}
}
