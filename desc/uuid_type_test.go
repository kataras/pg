package desc

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"
)

// insertColumnList returns the column names from an INSERT's leading column list. Callers must
// not substring-match a bare column name against the whole statement: "uuid" itself contains
// "id", and the table name may too.
func insertColumnList(t *testing.T, query string) []string {
	t.Helper()

	open := strings.Index(query, "(")
	end := strings.Index(query, ")")
	if open < 0 || end < open {
		t.Fatalf("could not find a column list in query: %s", query)
	}

	return strings.Split(query[open+1:end], ",")
}

// stdUUIDRow uses the Go 1.27 standard library uuid.UUID as a primary key *without* an explicit
// `type=uuid` tag. uuid.UUID has a String method, so before uuid.UUID was classified explicitly
// it fell through goTypeToDataType's fmt.Stringer fallback and silently became a text column:
// the table was created with the wrong column type and PostgreSQL never applied uuid semantics.
type stdUUIDRow struct {
	ID   uuid.UUID `pg:"primary"`
	Name string    `pg:"type=varchar(255)"`
	Tags []string  `pg:"type=varchar[]"`
	// A name-only tag: the field is included (lookupFields skips untagged fields) but its
	// column type still comes from Go type inference, which is what this test is about.
	Peers []uuid.UUID `pg:"peers"`
}

// TestGoTypeToDataTypeStdUUID pins the classification of the standard library's uuid types,
// which is the whole point of the Stringer-fallback ordering in goTypeToDataType.
func TestGoTypeToDataTypeStdUUID(t *testing.T) {
	if got := goTypeToDataType(reflect.TypeFor[uuid.UUID]()); got != UUID {
		t.Fatalf("uuid.UUID: expected UUID, got %v (a Stringer fallback to Text means the column would be created as text)", got)
	}

	if got := goTypeToDataType(reflect.TypeFor[[]uuid.UUID]()); got != UUIDArray {
		t.Fatalf("[]uuid.UUID: expected UUIDArray, got %v", got)
	}
}

// TestConvertStructToTableStdUUIDPrimaryKey verifies that an untagged uuid.UUID primary key gets
// the uuid column type and therefore also picks up the automatic gen_random_uuid() default that
// struct_table.go applies to non-nullable, non-referencing UUID primary keys.
func TestConvertStructToTableStdUUIDPrimaryKey(t *testing.T) {
	td, err := ConvertStructToTable("std_uuid_rows", reflect.TypeFor[stdUUIDRow]())
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}

	id := td.GetColumnByName("id")
	if id == nil {
		t.Fatal(`expected an "id" column`)
	}
	if id.Type != UUID {
		t.Fatalf("id column: expected type UUID, got %v", id.Type)
	}
	if id.Default != genRandomUUIDPGCryptoFunction1 {
		t.Fatalf("id column: expected the automatic %s default, got %q", genRandomUUIDPGCryptoFunction1, id.Default)
	}

	peers := td.GetColumnByName("peers")
	if peers == nil {
		t.Fatal(`expected a "peers" column`)
	}
	if peers.Type != UUIDArray {
		t.Fatalf("peers column: expected type UUIDArray, got %v", peers.Type)
	}
}

// TestBuildCreateTableQueryStdUUID is the golden guard against the silent-text regression: the
// emitted DDL must say uuid, not text, for an untagged uuid.UUID field.
func TestBuildCreateTableQueryStdUUID(t *testing.T) {
	td, err := ConvertStructToTable("std_uuid_rows", reflect.TypeFor[stdUUIDRow]())
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}

	got := BuildCreateTableQuery(td)
	want := `CREATE TABLE IF NOT EXISTS std_uuid_rows ("id" uuid DEFAULT gen_random_uuid() NOT NULL, "name" varchar(255) NOT NULL, "tags" varchar[] NOT NULL, "peers" uuid[] NOT NULL, PRIMARY KEY ("id"));`
	if got != want {
		t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

// TestBuildInsertQueryStdUUIDZeroValue mirrors TestBuildInsertQueryUUIDPKZeroValue for the
// standard library type: a zero uuid.UUID (uuid.Nil) must leave the primary key column out of
// the INSERT so the server-side gen_random_uuid() default fires, and a set one must be bound.
func TestBuildInsertQueryStdUUIDZeroValue(t *testing.T) {
	td, err := ConvertStructToTable("std_uuid_rows", reflect.TypeFor[stdUUIDRow]())
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}

	t.Run("zero uuid.UUID omits the PK column", func(t *testing.T) {
		sv := reflect.ValueOf(stdUUIDRow{Name: "a"})

		q, args, err := BuildInsertQuery(td, sv, nil, "", false)
		if err != nil {
			t.Fatalf("BuildInsertQuery: %v", err)
		}
		if slices.Contains(insertColumnList(t, q), "id") {
			t.Fatalf("expected the id column to be omitted so gen_random_uuid() fires, got: %s", q)
		}
		for _, arg := range args {
			if _, ok := arg.(uuid.UUID); ok {
				t.Fatalf("expected no uuid.UUID argument to be bound, got args: %v", args)
			}
		}
	})

	t.Run("non-zero uuid.UUID binds the PK column", func(t *testing.T) {
		id := uuid.MustParse("0198f1a0-0000-7000-8000-000000000001")
		sv := reflect.ValueOf(stdUUIDRow{ID: id, Name: "a"})

		q, args, err := BuildInsertQuery(td, sv, nil, "", false)
		if err != nil {
			t.Fatalf("BuildInsertQuery: %v", err)
		}
		if !slices.Contains(insertColumnList(t, q), "id") {
			t.Fatalf("expected the id column to be present, got: %s", q)
		}

		var found bool
		for _, arg := range args {
			if got, ok := arg.(uuid.UUID); ok && got == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected %s to be bound as an argument, got: %v", id, args)
		}
	})
}
