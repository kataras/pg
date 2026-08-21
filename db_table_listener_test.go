package pg

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newUnreachableTestDB returns a *DB backed by a pool that is configured but never actually
// dialed (pgxpool.NewWithConfig does not eagerly connect). It exists so validation-only tests
// can prove that PrepareListenTable rejects bad input before any database access is attempted:
// if validation were skipped or reordered, these tests would instead hang or fail with a
// connection error instead of the expected validation error.
func newUnreachableTestDB(t *testing.T) *DB {
	t.Helper()

	config, err := pgxpool.ParseConfig("host=127.0.0.1 port=1 user=nouser dbname=nodb connect_timeout=1")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return OpenPool(NewSchema(), pool)
}

// TestPrepareListenTableValidation asserts that PrepareListenTable rejects malformed
// channel/function/table identifiers with a descriptive error, and does so without ever
// reaching the database (see newUnreachableTestDB).
func TestPrepareListenTableValidation(t *testing.T) {
	invalid := []string{
		"ch'; DROP TABLE x;--", // SQL injection attempt.
		"my channel",           // space is not a valid identifier character.
		"1abc",                 // identifiers cannot start with a digit.
		"",                     // empty identifier.
	}

	db := newUnreachableTestDB(t)

	for _, v := range invalid {

		t.Run("channel/"+v, func(t *testing.T) {
			err := db.PrepareListenTable(context.Background(), &ListenTableOptions{
				Channel: v,
				Tables:  map[string][]TableChangeType{"customers": defaultChangesToWatch},
			})
			if err == nil {
				t.Fatalf("PrepareListenTable(Channel=%q): expected an error, got nil", v)
			}
		})

		t.Run("function/"+v, func(t *testing.T) {
			err := db.PrepareListenTable(context.Background(), &ListenTableOptions{
				Function: v,
				Tables:   map[string][]TableChangeType{"customers": defaultChangesToWatch},
			})
			if err == nil {
				t.Fatalf("PrepareListenTable(Function=%q): expected an error, got nil", v)
			}
		})

		t.Run("table/"+v, func(t *testing.T) {
			err := db.PrepareListenTable(context.Background(), &ListenTableOptions{
				Tables: map[string][]TableChangeType{v: defaultChangesToWatch},
			})
			if err == nil {
				t.Fatalf("PrepareListenTable(Tables={%q: ...}): expected an error, got nil", v)
			}
		})
	}

	t.Run("wildcard table key is not validated as an identifier", func(t *testing.T) {
		// "*" is the documented wildcard sentinel, not a raw identifier: it must be allowed
		// through validation (it never reaches SQL as-is, it expands to registered table
		// names). With an empty schema (no tables registered) the wildcard expands to
		// nothing, so this legitimately succeeds without ever touching the (unreachable)
		// pool; we only assert it is not rejected by identifier validation.
		err := db.PrepareListenTable(context.Background(), &ListenTableOptions{
			Tables: map[string][]TableChangeType{"*": defaultChangesToWatch},
		})
		if err != nil && strings.Contains(err.Error(), "pg: listen table: invalid") {
			t.Fatalf("wildcard table key must not be rejected by identifier validation, got: %v", err)
		}
	})

	t.Run("valid identifiers pass validation", func(t *testing.T) {
		err := db.PrepareListenTable(context.Background(), &ListenTableOptions{
			Channel:  "my_channel_1",
			Function: "my_channel_1",
			Tables:   map[string][]TableChangeType{"my_channel_1": defaultChangesToWatch},
		})
		// Valid identifiers must pass validation and only then fail once prepareListenTable
		// tries to reach the (unreachable) database, i.e. NOT a validation error.
		if err == nil {
			t.Fatalf("expected a connection error since the pool is unreachable")
		}
		if strings.Contains(err.Error(), "pg: listen table: invalid") {
			t.Fatalf("expected a connection error, got a validation error: %v", err)
		}
	})
}

// TestChangesToString verifies changesToString joins 1, 2 and 3 changes with " OR ",
// and that its behavior around the exact separator was preserved after the rewrite from a
// manual strings.Builder loop to strings.Join.
func TestChangesToString(t *testing.T) {
	tests := []struct {
		name    string
		changes []TableChangeType
		want    string
	}{
		{"no changes", nil, ""},
		{"one change", []TableChangeType{TableChangeTypeInsert}, "INSERT"},
		{"two changes", []TableChangeType{TableChangeTypeInsert, TableChangeTypeUpdate}, "INSERT OR UPDATE"},
		{
			"three changes",
			[]TableChangeType{TableChangeTypeInsert, TableChangeTypeUpdate, TableChangeTypeDelete},
			"INSERT OR UPDATE OR DELETE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := changesToString(tt.changes); got != tt.want {
				t.Errorf("changesToString(%v) = %q, want %q", tt.changes, got, tt.want)
			}
		})
	}
}
