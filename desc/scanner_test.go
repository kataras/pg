package desc

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestNullableScannerScan exercises nullableScanner.Scan's driver-value handling: an assignable
// src is set directly, a convertible src (e.g. int64 from the driver into an int field) is
// converted and set, and an incompatible src returns a descriptive error instead of panicking
// inside reflect.Value.Set: the bug this hardens (see task B6; nullableScanner backs nullable
// UUID/text/varchar columns, see findScanTargets).
func TestNullableScannerScan(t *testing.T) {
	t.Run("assignable src", func(t *testing.T) {
		type row struct {
			Name string
		}
		var r row
		fieldPtr := reflect.ValueOf(&r).Elem().FieldByName("Name")

		s := &nullableScanner{colName: "name", fieldPtr: fieldPtr}
		if err := s.Scan("hello"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Name != "hello" {
			t.Fatalf("expected Name to be set to %q, got %q", "hello", r.Name)
		}
	})

	t.Run("convertible src (int64 -> int)", func(t *testing.T) {
		type row struct {
			Count int
		}
		var r row
		fieldPtr := reflect.ValueOf(&r).Elem().FieldByName("Count")

		s := &nullableScanner{colName: "count", fieldPtr: fieldPtr}
		if err := s.Scan(int64(42)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Count != 42 {
			t.Fatalf("expected Count to be set to 42, got %d", r.Count)
		}
	})

	t.Run("incompatible src (string -> int) returns an error, not a panic", func(t *testing.T) {
		type row struct {
			Count int
		}
		var r row
		fieldPtr := reflect.ValueOf(&r).Elem().FieldByName("Count")

		s := &nullableScanner{colName: "count", fieldPtr: fieldPtr}

		var err error
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Scan panicked: %v", rec)
				}
			}()
			err = s.Scan("not a number")
		}()

		if err == nil {
			t.Fatal("expected an error for an incompatible src type, got nil")
		}
	})

	t.Run("nil src is a no-op", func(t *testing.T) {
		type row struct {
			Name string
		}
		var r row
		fieldPtr := reflect.ValueOf(&r).Elem().FieldByName("Name")

		s := &nullableScanner{colName: "name", fieldPtr: fieldPtr}
		if err := s.Scan(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Name != "" {
			t.Fatalf("expected Name to remain empty, got %q", r.Name)
		}
	})
}

// TestPasswordTextScannerScanNonStringField verifies passwordTextScanner.Scan returns an error
// (instead of panicking via reflect.Value.Set) when the destination struct field isn't a string.
func TestPasswordTextScannerScanNonStringField(t *testing.T) {
	type row struct {
		Password int // deliberately the wrong kind.
	}
	var r row
	fieldPtr := reflect.ValueOf(&r).Elem().FieldByName("Password")

	s := &passwordTextScanner{
		tableName: "users",
		passwordHandler: &PasswordHandler{
			Decrypt: func(tableName, encryptedPassword string) (string, error) {
				return "plain-text-password", nil
			},
		},
		passwordTextFieldPtr: fieldPtr,
	}

	var err error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("Scan panicked: %v", rec)
			}
		}()
		err = s.Scan("encrypted")
	}()

	if err == nil {
		t.Fatal("expected an error for a non-string password field, got nil")
	}
}

// TestBuildColumnLookup exercises buildColumnLookup, the O(1) case-insensitive column index
// RowsToStruct/RowToStruct/ConvertRowsToStruct build once and pass into findScanTargets, replacing
// a per-row, per-column linear GetColumnByName scan (task B8).
func TestBuildColumnLookup(t *testing.T) {
	myCol := &Column{Name: "MyCol"}
	otherCol := &Column{Name: "other_col"}
	td := &Table{Columns: []*Column{myCol, otherCol}}

	lookup := buildColumnLookup(td)

	t.Run("case-insensitive hit", func(t *testing.T) {
		col, ok := lookup["mycol"] // lower-cased lookup key for a "MyCol" column.
		if !ok {
			t.Fatalf("expected a hit for lookup key %q", "mycol")
		}
		if col != myCol {
			t.Fatalf("expected the %q column, got %v", "MyCol", col)
		}
	})

	t.Run("miss returns nil", func(t *testing.T) {
		col, ok := lookup["does_not_exist"]
		if ok {
			t.Fatalf("expected no hit for %q", "does_not_exist")
		}
		if col != nil {
			t.Fatalf("expected a nil column for a miss, got %v", col)
		}
	})
}

// TestFindScanTargetsCaseInsensitive is a scan-path test proving findScanTargets (the function
// RowsToStruct, RowToStruct and ConvertRowsToStruct all call to resolve scan targets, now via a
// buildColumnLookup map instead of GetColumnByName's linear scan) still resolves a row's column
// name to the right struct field case-insensitively, exactly as GetColumnByName did.
//
// It exercises findScanTargets directly rather than going through RowsToStruct with a real
// pgx.Rows, since driving that end-to-end needs either a live database or a hand-rolled pgx.Rows
// mock that reimplements Scan's driver-value decoding (out of scope for this behavior-preserving
// refactor). findScanTargets has no pgx.Rows dependency (only reflect.Value, *Table, the lookup map
// and []pgconn.FieldDescription), so it can be exercised directly without either.
func TestFindScanTargetsCaseInsensitive(t *testing.T) {
	type row struct {
		MyCol string
	}

	col := &Column{
		Name:       "MyCol",
		FieldIndex: []int{0},
		FieldType:  reflect.TypeFor[string](),
		Type:       Text,
	}
	td := &Table{Name: "row", Columns: []*Column{col}}
	col.Table = td

	lookup := buildColumnLookup(td)

	var dst row
	dstElemValue := reflect.ValueOf(&dst).Elem()

	// The row's field name comes back lower-cased, differing in case from the declared
	// column name "MyCol": the same situation GetColumnByName's EqualFold comparison used
	// to tolerate.
	fieldDescs := []pgconn.FieldDescription{{Name: "mycol"}}

	scanTargets, err := findScanTargets(dstElemValue, td, lookup, fieldDescs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scanTargets) != 1 {
		t.Fatalf("expected 1 scan target, got %d", len(scanTargets))
	}

	target, ok := scanTargets[0].(*string)
	if !ok {
		t.Fatalf("expected scan target to be *string, got %T", scanTargets[0])
	}

	*target = "hello"
	if dst.MyCol != "hello" {
		t.Fatalf("expected MyCol to be set through the case-insensitively resolved scan target, got %q", dst.MyCol)
	}
}
