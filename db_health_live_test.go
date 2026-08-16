package pg

import (
	"context"
	"testing"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestDBPing|TestDBHealth' -v .

// TestDBPing verifies that DB.Ping succeeds against a live, reachable database.
func TestDBPing(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err = db.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: expected nil error against a live database, got: %v", err)
	}
}

// TestDBHealth verifies that DB.Health returns a non-empty ServerVersion (as parsed by
// GetVersion) together with pool statistics reflecting at least one connection. This is
// guaranteed, not just likely: Health's Ping and GetVersion calls each acquire and release a
// pooled connection immediately before PoolStat() runs, in the same goroutine, and pgxpool only
// destroys idle connections via a background reaper keyed to MaxConnIdleTime/MaxConnLifetime
// (minutes), so TotalConns cannot have dropped back to zero by the time PoolStat() is called.
func TestDBHealth(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	health, err := db.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: expected nil error against a live database, got: %v", err)
	}

	if health.ServerVersion == "" {
		t.Fatal("Health: expected a non-empty ServerVersion")
	}

	if health.Pool.MaxConns <= 0 {
		t.Fatalf("Health: expected Pool.MaxConns > 0, got %d", health.Pool.MaxConns)
	}
	if health.Pool.TotalConns <= 0 {
		t.Fatalf("Health: expected Pool.TotalConns > 0 after acquiring a connection to ping, got %d", health.Pool.TotalConns)
	}
}
