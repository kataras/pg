package desc

import (
	"reflect"
	"testing"
	"uuid"
)

// nullableUUIDRow models a nullable uuid column whose Go field is a non-pointer uuid.UUID, the
// shape that routes through nullableScanner (see findScanTargets: a non-pointer, nullable
// UUID/Text/CharacterVarying column is wrapped so a SQL NULL is tolerated).
type nullableUUIDRow struct {
	ID uuid.UUID `pg:"type=uuid,nullable"`
}

// TestNullableScannerUUIDFromString covers the value pgx actually hands a sql.Scanner for a uuid
// column: pgtype.UUIDCodec.DecodeDatabaseSQLValue returns the *canonical string form*, not the
// 16 raw bytes. A Go string is neither assignable nor convertible to a [16]byte-based type, so
// without an encoding.TextUnmarshaler fallback this scan fails and a nullable uuid.UUID field is
// unusable.
func TestNullableScannerUUIDFromString(t *testing.T) {
	want := uuid.MustParse("0198f1a0-0000-7000-8000-000000000001")

	var row nullableUUIDRow
	field := reflect.ValueOf(&row).Elem().Field(0)

	sc := &nullableScanner{colName: "id", fieldPtr: field}
	if err := sc.Scan(want.String()); err != nil {
		t.Fatalf("Scan(%q) into a uuid.UUID field: %v", want.String(), err)
	}

	if row.ID != want {
		t.Fatalf("expected %s, got %s", want, row.ID)
	}
}

// TestNullableScannerUUIDFromBytes covers the binary-format sibling: when the driver yields the
// raw 16 bytes, the existing convertible path must still handle it.
func TestNullableScannerUUIDFromBytes(t *testing.T) {
	want := uuid.MustParse("0198f1a0-0000-7000-8000-000000000002")

	var row nullableUUIDRow
	field := reflect.ValueOf(&row).Elem().Field(0)

	sc := &nullableScanner{colName: "id", fieldPtr: field}
	if err := sc.Scan([16]byte(want)); err != nil {
		t.Fatalf("Scan([16]byte) into a uuid.UUID field: %v", err)
	}

	if row.ID != want {
		t.Fatalf("expected %s, got %s", want, row.ID)
	}
}

// TestNullableScannerUUIDNullStaysZero is the reason nullableScanner exists at all: a SQL NULL
// must leave the non-pointer field at its zero value rather than erroring.
func TestNullableScannerUUIDNullStaysZero(t *testing.T) {
	var row nullableUUIDRow
	field := reflect.ValueOf(&row).Elem().Field(0)

	sc := &nullableScanner{colName: "id", fieldPtr: field}
	if err := sc.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}

	if row.ID != (uuid.UUID{}) {
		t.Fatalf("expected the zero UUID, got %s", row.ID)
	}
}

// TestNullableScannerRejectsUnscannable keeps the descriptive-error behavior for a value that is
// genuinely not scannable into the field, instead of panicking inside reflect.Value.Set.
func TestNullableScannerRejectsUnscannable(t *testing.T) {
	var row nullableUUIDRow
	field := reflect.ValueOf(&row).Elem().Field(0)

	sc := &nullableScanner{colName: "id", fieldPtr: field}
	err := sc.Scan("not-a-uuid")
	if err == nil {
		t.Fatal("expected an error scanning a malformed uuid string, got nil")
	}
}
