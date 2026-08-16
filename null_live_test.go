package pg

import (
	"context"
	"testing"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestNullIfZeroRoundTrip' -v .

// TestNullIfZeroRoundTrip verifies that NullIfZero("") (a nil *string) is bound by pgx as a
// genuine SQL NULL rather than an empty string, confirming the pgtype.Text{Valid:false}-style
// escape hatch documented on NullIfZero is unnecessary.
func TestNullIfZeroRoundTrip(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	var isNull bool
	if err = db.QueryRow(ctx, "SELECT $1::text IS NULL", NullIfZero("")).Scan(&isNull); err != nil {
		t.Fatalf(`SELECT $1::text IS NULL with NullIfZero(""): %v`, err)
	}
	if !isNull {
		t.Fatal(`expected NullIfZero("") to bind as SQL NULL, got a non-NULL text value`)
	}

	// Sanity check the non-zero side too: a non-empty string must NOT round-trip as NULL.
	isNull = true
	if err = db.QueryRow(ctx, "SELECT $1::text IS NULL", NullIfZero("hello")).Scan(&isNull); err != nil {
		t.Fatalf(`SELECT $1::text IS NULL with NullIfZero("hello"): %v`, err)
	}
	if isNull {
		t.Fatal(`expected NullIfZero("hello") to NOT bind as SQL NULL`)
	}
}
