package pg_test

// These tests require a live PostgreSQL server; they skip themselves (via pgtest.ConnString)
// when the PG_CONNSTRING environment variable is not set. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test . -run NotNull -v

import (
	"context"
	"testing"

	"github.com/kataras/pg"
	"github.com/kataras/pg/desc"
	"github.com/kataras/pg/pgtest"
)

// notNullWidget has NOT NULL columns next to a primary key, a unique column, a check and a
// nullable column. On PostgreSQL 18 every NOT NULL column also gets a pg_constraint row with
// contype 'n', which DB.ListConstraints used to scan and reject.
type notNullWidget struct {
	ID       string `pg:"type=uuid,primary"`
	Name     string `pg:"type=varchar(64),unique"`
	Quantity int    `pg:"type=integer,check=quantity >= 0"`
	Note     string `pg:"type=text,nullable"`
}

func TestCheckSchemaNotNullColumns(t *testing.T) {
	connString := pgtest.ConnString(t)
	ctx := context.Background()

	schema := pg.NewSchema()
	schema.MustRegister("not_null_widgets", notNullWidget{})

	db := pgtest.New(t, schema, connString)

	if err := db.CheckSchema(ctx); err != nil {
		t.Fatalf("CheckSchema: %v", err)
	}

	constraints, err := db.ListConstraints(ctx, "not_null_widgets")
	if err != nil {
		t.Fatalf("ListConstraints: %v", err)
	}
	for _, c := range constraints {
		if c.ConstraintType == desc.NoneConstraintType {
			t.Fatalf("ListConstraints returned a NOT NULL row %q on %s.%s; it must filter contype 'n'", c.ConstraintName, c.TableName, c.ColumnName)
		}
	}

	columns, err := db.ListColumns(ctx, "not_null_widgets")
	if err != nil {
		t.Fatalf("ListColumns: %v", err)
	}
	nullable := make(map[string]bool, len(columns))
	for _, col := range columns {
		nullable[col.Name] = col.Nullable
	}
	for name, want := range map[string]bool{"id": false, "name": false, "quantity": false, "note": true} {
		got, ok := nullable[name]
		if !ok {
			t.Fatalf("column %q missing from ListColumns", name)
		}
		if got != want {
			t.Fatalf("column %q: Nullable = %v, want %v", name, got, want)
		}
	}
}
