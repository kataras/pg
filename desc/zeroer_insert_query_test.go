package desc

import (
	"reflect"
	"testing"
)

// uuidPK is a [16]byte-based type standing in for a hand-rolled or
// vendored UUID implementation. It intentionally implements no Zeroer, so
// isZero must fall back to generic reflect-based zero detection for it.
type uuidPK [16]byte

// uuidPKRow's ID is a *pointer* to a [16]byte-based UUID type: a realistic
// shape for a settable UUID primary key. A non-nil pointer to a zero-valued
// [16]byte is exactly the case the pre-rewrite isZero got wrong:
// desc/argument.go's first, generic "isZero(field)" check (reflect.Value
// "path 1") sees a non-nil pointer and does not skip the column, so it
// falls to the UUID-specific second check (desc/argument.go:89-93,
// mirrored in desc/insert_query.go's BuildBulkInsertQuery), which operates
// on the dereferenced interface value via "path 2". The pre-rewrite type
// switch had no case for *uuidPK, fell to `default: return false`, and
// bound the pointer as a query parameter instead of letting
// gen_random_uuid() fire.
type uuidPKRow struct {
	ID   *uuidPK `pg:"type=uuid,primary,default=gen_random_uuid()"`
	Name string  `pg:"type=varchar(255)"`
}

// TestBuildInsertQueryUUIDPKZeroValue is the insert-query-level regression
// test for the isZero rewrite: a struct whose primary key is a
// pointer-to-[16]byte-based UUID type with a DB-side default must have its
// PK column omitted from the INSERT (so gen_random_uuid() fires) when the
// pointer is non-nil but points at the zero UUID, and must include it when
// the UUID is non-zero.
func TestBuildInsertQueryUUIDPKZeroValue(t *testing.T) {
	td, err := ConvertStructToTable("uuid_pk_rows", reflect.TypeOf(uuidPKRow{}))
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}

	t.Run("zero UUID pointer omits the PK column", func(t *testing.T) {
		zero := uuidPK{}
		sv := reflect.ValueOf(uuidPKRow{ID: &zero, Name: "a"})

		q, args, err := BuildInsertQuery(td, sv, nil, "", false)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."uuid_pk_rows" (name) VALUES($1);`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v (the id must be omitted, not bound)", args, wantArgs)
		}
	})

	t.Run("non-zero UUID pointer is bound", func(t *testing.T) {
		id := uuidPK{1}
		sv := reflect.ValueOf(uuidPKRow{ID: &id, Name: "a"})

		q, args, err := BuildInsertQuery(td, sv, nil, "", false)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."uuid_pk_rows" (id,name) VALUES($1,$2);`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		if len(args) != 2 {
			t.Fatalf("args = %#v, want exactly 2 binds (id, name)", args)
		}
	})
}
