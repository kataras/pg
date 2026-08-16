// Package pgtest provides ephemeral, per-test PostgreSQL schemas for tests that run against
// any real database, including CI service containers. Each call to New creates a randomly
// named schema, points a fresh connection pool's search_path at it, and registers cleanup
// that drops the schema and closes the pool once the test finishes, so tests can run
// concurrently (even across packages, even against the same shared database) without
// stepping on each other's tables.
//
// ConnString reads the server to test against from the PG_CONNSTRING environment variable
// and skips the calling test when it is unset. Unlike the pg package's own test suite, which
// falls back to a hardcoded local connection string so its CI job has a default to run
// against, pgtest has no such fallback: it is meant to be imported by downstream projects'
// test suites too, where a hardcoded host, user and password would be meaningless (or worse,
// silently wrong). A missing PG_CONNSTRING therefore means "skip", not "guess".
//
// # Usage
//
//	func TestSomething(t *testing.T) {
//		connString := pgtest.ConnString(t)
//
//		schema := pg.NewSchema()
//		schema.MustRegister("widgets", Widget{})
//
//		db := pgtest.New(t, schema, connString)
//		repo := pg.NewRepository[Widget](db)
//		// ... use repo/db; the schema is dropped automatically on test completion.
//	}
//
// Build the *pg.Schema fresh inside each test, as above, rather than sharing one across
// tests. New mutates the Schema's registered tables in place (see New's doc), so passing the
// same *pg.Schema to two overlapping New calls is an unsynchronized concurrent write to
// shared state: a genuine data race that "go test -race" will flag, not just a source of
// occasional flaky assertions. New detects the overlap and fails the second call fast instead
// of letting it race silently, but the fix is still to give each New call its own Schema.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kataras/pg"
)

// envConnString is the environment variable ConnString reads the connection string from.
const envConnString = "PG_CONNSTRING"

// ConnString returns the connection string to test against, read from the PG_CONNSTRING
// environment variable. If it is unset or empty, ConnString skips the calling test with an
// explanatory message instead of falling back to a default: see the package doc for why.
func ConnString(tb testing.TB) string {
	tb.Helper()

	connString := os.Getenv(envConnString)
	if connString == "" {
		tb.Skipf("pgtest: %s is not set; skipping test that requires a live PostgreSQL server", envConnString)
	}

	return connString
}

// cleanupTimeout bounds the DROP SCHEMA/pool-close cleanup New registers via tb.Cleanup.
// A fresh context is required there (see New) because tb's own context is already canceled
// by the time Cleanup callbacks run, and cleanup must not be allowed to hang forever.
const cleanupTimeout = 30 * time.Second

// inUseSchemas tracks which *pg.Schema values currently have an active, unreleased New call
// in flight. schema.Tables() (in the pg package) returns the Schema's own live *desc.Table
// pointers, not copies, and New mutates their SearchPath field in place (see the comment
// where that happens, below), so two New calls sharing one Schema at the same time would
// write to those same structs from two goroutines with no synchronization at all: a genuine
// data race, not merely a logical mix-up. LoadOrStore lets New detect that overlap and refuse
// the second call outright instead of letting it happen. Entries are removed by the same
// tb.Cleanup that releases everything else New set up.
var inUseSchemas sync.Map // key: *pg.Schema, value: struct{}{}

// New opens a *pg.DB against connString whose search_path is a freshly created, randomly
// named schema (pgtest_<16 hex chars>) private to the calling test, and registers a
// tb.Cleanup function that drops the schema (CASCADE, so any table created inside it goes
// too) and closes the connection pool once the test (or, called from inside t.Run, the
// subtest) finishes.
//
// If schema has any tables registered, New also calls DB.CreateSchema to materialize them
// inside the new schema before returning, so the caller can start using pg.Repository right
// away. An empty schema (pg.NewSchema(), nothing registered) is fine too: New still creates
// and cleans up the isolated schema, leaving table creation to the caller.
//
// New also repoints every table registered on schema at the new schema name (see the
// SearchPath comment in New's body for why that is necessary for Insert/Upsert to work at
// all). Because schema.Tables() returns the Schema's own live *desc.Table pointers rather
// than copies, doing that from two overlapping New calls sharing one Schema would be an
// unsynchronized write to the same memory from two goroutines: a genuine data race (the kind
// "go test -race" reports), not just a logical mix-up. New refuses to let that happen: a
// second New call given a *pg.Schema that an earlier, still-active New call already holds
// fails immediately via tb.Fatalf instead of racing. Build a fresh *pg.Schema per call to New
// instead of sharing one across concurrent tests; registering the same struct types on it
// repeatedly is cheap.
//
// Every setup step (parsing connString, opening the pool, creating the schema, creating its
// tables) fails the test immediately via tb.Fatalf on error. Once the schema itself has been
// created, cleanup is registered before anything else can fail, so a later failure (such as
// CreateSchema rejecting one of schema's tables) still drops the schema instead of leaking it.
func New(tb testing.TB, schema *pg.Schema, connString string) *pg.DB {
	tb.Helper()

	if _, alreadyInUse := inUseSchemas.LoadOrStore(schema, struct{}{}); alreadyInUse {
		tb.Fatalf("pgtest: this *pg.Schema is already in use by another, still-active New call; " +
			"New mutates its tables in place, so pass a fresh *pg.Schema to each New call instead of sharing one")
	}
	tb.Cleanup(func() { inUseSchemas.Delete(schema) })

	name, err := randomSchemaName()
	if err != nil {
		tb.Fatalf("pgtest: generate schema name: %v", err)
	}

	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		tb.Fatalf("pgtest: parse connection string: %v", err)
	}
	// Force every connection this pool opens to default to our ephemeral schema, overriding
	// whatever search_path (if any) connString itself carried.
	config.ConnConfig.RuntimeParams["search_path"] = name

	// tb.Context() is canceled as soon as the test (or subtest) finishes, which is exactly
	// the lifetime setup should run under, unlike the cleanup below, which needs to survive
	// past that cancellation.
	ctx := tb.Context()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		tb.Fatalf("pgtest: open connection pool: %v", err)
	}

	quotedName := pg.QuoteIdentifier(name)

	if _, err = pool.Exec(ctx, "CREATE SCHEMA "+quotedName); err != nil {
		pool.Close()
		tb.Fatalf("pgtest: create schema %s: %v", name, err)
	}

	db := pg.OpenPool(schema, pool)

	// The schema itself now exists, so register its drop/close cleanup right away, before
	// attempting table creation below, which can fail (a bad struct tag, an unsupported type,
	// a foreign key to a table that doesn't exist, easy to hit while iterating). Without
	// this, a CreateSchema failure would leak the pgtest_<hex> schema into the target
	// database, since nothing would ever have been registered to drop it.
	tb.Cleanup(func() {
		// Deliberately not tb.Context(): by the time Cleanup callbacks run, that context is
		// already canceled, so DROP SCHEMA/Close need a context of their own.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		if _, err := db.Exec(cleanupCtx, "DROP SCHEMA "+quotedName+" CASCADE"); err != nil {
			tb.Errorf("pgtest: drop schema %s: %v", name, err)
		}

		db.Close()
	})

	// pg's INSERT/UPSERT query builder schema-qualifies every statement with the table's own
	// desc.Table.SearchPath field rather than the live connection's search_path. That
	// field is captured once, from the process-wide pg.SetDefaultSearchPath ("public" unless
	// changed), at Schema.Register time. Left alone, Repository.InsertSingle/Insert/Upsert
	// would keep targeting "public" (or whatever the default was) instead of our ephemeral
	// schema, even though SELECT/UPDATE/DELETE and CREATE TABLE (which rely on the session's
	// actual search_path instead) would correctly land here. Retarget every registered
	// table's SearchPath at our schema so all of it agrees.
	tables := schema.Tables()
	for _, td := range tables {
		td.SearchPath = name
	}

	if len(tables) > 0 {
		if err = db.CreateSchema(ctx); err != nil {
			tb.Fatalf("pgtest: create tables in schema %s: %v", name, err)
		}
	}

	return db
}

// randomSchemaName returns a name of the form pgtest_<16 hex chars>, random enough that
// concurrent test runs (even separate processes pointed at the same database) don't
// collide on it.
func randomSchemaName() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	return "pgtest_" + hex.EncodeToString(buf), nil
}
