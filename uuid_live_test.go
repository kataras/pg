package pg_test

// These tests require a live PostgreSQL server; they skip themselves (via pgtest.ConnString)
// when the PG_CONNSTRING environment variable is not set. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test . -run UUID -v

import (
	"context"
	"testing"
	"uuid"

	"github.com/kataras/pg"
	"github.com/kataras/pg/pgtest"
)

// stdUUIDWidget exercises the Go 1.27 standard library uuid.UUID as a real column type end to
// end. The primary key carries no explicit `type=` tag on purpose: the column type comes from Go
// type inference, which is what desc.goTypeToDataType's uuid case exists for. Before that case
// existed, uuid.UUID matched the fmt.Stringer fallback and this table was created with a text
// primary key instead of a uuid one.
type stdUUIDWidget struct {
	ID      uuid.UUID `pg:"primary"`
	Name    string    `pg:"type=varchar(64)"`
	OwnerID uuid.UUID `pg:"type=uuid,nullable"`
}

func stdUUIDSchema() *pg.Schema {
	schema := pg.NewSchema()
	schema.MustRegister("std_uuid_widgets", stdUUIDWidget{})
	return schema
}

// TestUUIDRoundTrip covers the three things only a real server can confirm: that the column is
// created as uuid, that a zero uuid.UUID lets the server-side gen_random_uuid() default fire,
// and that a uuid.UUID value survives the encode/decode round trip through pgx (which wraps a
// named [16]byte via its byte16Wrapper, with no codec registration on our side).
func TestUUIDRoundTrip(t *testing.T) {
	connString := pgtest.ConnString(t)
	ctx := context.Background()

	db := pgtest.New(t, stdUUIDSchema(), connString)
	repo := pg.NewRepository[stdUUIDWidget](db)

	t.Run("server generates the primary key when the field is the zero UUID", func(t *testing.T) {
		var id uuid.UUID
		if err := repo.InsertSingle(ctx, stdUUIDWidget{Name: "generated"}, &id); err != nil {
			t.Fatalf("insert single: %v", err)
		}
		if id == (uuid.UUID{}) {
			t.Fatal("expected gen_random_uuid() to populate the primary key, got the zero UUID")
		}

		got, err := repo.SelectByID(ctx, id)
		if err != nil {
			t.Fatalf("select by id: %v", err)
		}
		if got.ID != id {
			t.Fatalf("expected id %s to round trip, got %s", id, got.ID)
		}
		if got.Name != "generated" {
			t.Fatalf("expected name %q, got %q", "generated", got.Name)
		}
	})

	t.Run("a client-supplied uuid.UUID round trips unchanged", func(t *testing.T) {
		want := uuid.NewV7()

		if err := repo.InsertSingle(ctx, stdUUIDWidget{ID: want, Name: "explicit"}, nil); err != nil {
			t.Fatalf("insert single: %v", err)
		}

		got, err := repo.SelectByID(ctx, want)
		if err != nil {
			t.Fatalf("select by id: %v", err)
		}
		if got.ID != want {
			t.Fatalf("expected %s, got %s", want, got.ID)
		}
	})

	t.Run("a NULL nullable uuid column scans as the zero UUID", func(t *testing.T) {
		// OwnerID is a non-pointer, nullable uuid column, so it is served by desc's
		// nullableScanner. pgx hands that sql.Scanner the *canonical string form* of a uuid,
		// which is neither assignable nor convertible to a [16]byte-based type - it only works
		// via the encoding.TextUnmarshaler fallback.
		id := uuid.NewV7()
		if err := repo.InsertSingle(ctx, stdUUIDWidget{ID: id, Name: "no-owner"}, nil); err != nil {
			t.Fatalf("insert single: %v", err)
		}

		got, err := repo.SelectByID(ctx, id)
		if err != nil {
			t.Fatalf("select by id: %v", err)
		}
		if got.OwnerID != (uuid.UUID{}) {
			t.Fatalf("expected a NULL owner_id to scan as the zero UUID, got %s", got.OwnerID)
		}
	})

	t.Run("a set nullable uuid column scans back", func(t *testing.T) {
		id, owner := uuid.NewV7(), uuid.NewV7()
		if err := repo.InsertSingle(ctx, stdUUIDWidget{ID: id, Name: "owned", OwnerID: owner}, nil); err != nil {
			t.Fatalf("insert single: %v", err)
		}

		got, err := repo.SelectByID(ctx, id)
		if err != nil {
			t.Fatalf("select by id: %v", err)
		}
		if got.OwnerID != owner {
			t.Fatalf("expected owner %s, got %s", owner, got.OwnerID)
		}
	})
}

// TestUUIDCopyFrom covers the COPY path, which decides per *batch* rather than per row whether a
// defaulted column can be omitted: desc.BuildCopyPlan omits a default-bearing column only when
// it is zero in every row, so a batch mixing set and zero UUIDs must send the column and would
// otherwise try to insert a zero UUID.
func TestUUIDCopyFrom(t *testing.T) {
	connString := pgtest.ConnString(t)
	ctx := context.Background()

	db := pgtest.New(t, stdUUIDSchema(), connString)
	repo := pg.NewRepository[stdUUIDWidget](db)

	first, second := uuid.NewV7(), uuid.NewV7()
	rows := []stdUUIDWidget{
		{ID: first, Name: "copy-one"},
		{ID: second, Name: "copy-two"},
	}

	n, err := repo.CopyFrom(ctx, rows)
	if err != nil {
		t.Fatalf("copy from: %v", err)
	}
	if n != int64(len(rows)) {
		t.Fatalf("expected %d rows copied, got %d", len(rows), n)
	}

	for _, want := range rows {
		got, err := repo.SelectByID(ctx, want.ID)
		if err != nil {
			t.Fatalf("select by id %s: %v", want.ID, err)
		}
		if got.ID != want.ID || got.Name != want.Name {
			t.Fatalf("expected %+v, got %+v", want, got)
		}
	}
}
