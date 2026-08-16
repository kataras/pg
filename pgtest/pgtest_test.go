package pgtest_test

// These tests require a live PostgreSQL server; they skip themselves (via pgtest.ConnString)
// when the PG_CONNSTRING environment variable is not set. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test ./pgtest/... -v

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/kataras/pg"
	"github.com/kataras/pg/pgtest"
)

// widget is a minimal registered entity used to exercise New's table-creation path and a
// Repository round trip through it.
type widget struct {
	ID   string `pg:"type=uuid,primary"`
	Name string `pg:"type=varchar(64)"`
}

func widgetSchema() *pg.Schema {
	schema := pg.NewSchema()
	schema.MustRegister("widgets", widget{})
	return schema
}

// TestNew verifies the whole happy path: New creates and switches to a private schema,
// materializes the registered "widgets" table inside it, a Repository can insert and read a
// row through it, and the active search_path (both as reported locally by DB.SearchPath and
// as reported by the server itself via current_schemas) is the ephemeral schema New created,
// not "public".
func TestNew(t *testing.T) {
	connString := pgtest.ConnString(t)
	ctx := context.Background()

	db := pgtest.New(t, widgetSchema(), connString)

	searchPath := db.SearchPath()
	if searchPath == "" || searchPath == "public" {
		t.Fatalf("expected an ephemeral, non-public search path, got %q", searchPath)
	}

	repo := pg.NewRepository[widget](db)

	inserted := widget{Name: "gear"}
	var id string
	if err := repo.InsertSingle(ctx, inserted, &id); err != nil {
		t.Fatalf("insert single: %v", err)
	}
	if id == "" {
		t.Fatal("expected InsertSingle to populate the generated uuid primary key")
	}

	got, err := repo.SelectByID(ctx, id)
	if err != nil {
		t.Fatalf("select by id: %v", err)
	}
	if got.Name != "gear" {
		t.Fatalf("expected name %q, got %q", "gear", got.Name)
	}

	// Confirm, from the server's own point of view, that the connection's active schema is
	// the one New created, not just that DB.SearchPath (a local Go field) says so.
	var currentSchemas []string
	if err := db.QueryRow(ctx, "SELECT current_schemas(false)").Scan(&currentSchemas); err != nil {
		t.Fatalf("select current_schemas: %v", err)
	}
	if !slices.Contains(currentSchemas, searchPath) {
		t.Fatalf("expected %q among the server's current_schemas, got %v", searchPath, currentSchemas)
	}
}

// TestNewIsolation verifies that two sequential New calls against the same connection string
// land in two different, mutually invisible schemas: a table created through one is not
// resolvable (via an unqualified name) from the other.
func TestNewIsolation(t *testing.T) {
	connString := pgtest.ConnString(t)
	ctx := context.Background()

	db1 := pgtest.New(t, pg.NewSchema(), connString)
	db2 := pgtest.New(t, pg.NewSchema(), connString)

	if db1.SearchPath() == db2.SearchPath() {
		t.Fatalf("expected two New calls to get different schemas, both got %q", db1.SearchPath())
	}

	if _, err := db1.Exec(ctx, "CREATE TABLE only_in_db1 (id INT)"); err != nil {
		t.Fatalf("create table in db1: %v", err)
	}

	// db1 can see its own table by its unqualified name.
	if _, err := db1.Exec(ctx, "SELECT * FROM only_in_db1"); err != nil {
		t.Fatalf("expected only_in_db1 to be visible from db1, got: %v", err)
	}

	// db2's search_path does not include db1's schema, so the same unqualified name must not
	// resolve to anything there.
	var regclass *string
	if err := db2.QueryRow(ctx, "SELECT to_regclass('only_in_db1')").Scan(&regclass); err != nil {
		t.Fatalf("to_regclass lookup from db2: %v", err)
	}
	if regclass != nil {
		t.Fatalf("expected only_in_db1 to be invisible from db2, but to_regclass resolved it to %q", *regclass)
	}
}

// TestNewCleanupDropsSchema verifies that the cleanup New registers actually runs and
// actually drops the schema, rather than merely closing the pool.
//
// New's schema is only known once New has returned, and its cleanup only runs once the test
// that called New has finished, so this drives New from inside a non-parallel subtest and
// inspects pg_namespace afterwards, from the parent test. t.Run blocks until the subtest,
// including any cleanup functions it registered, has fully completed, so by the time t.Run
// returns here the DROP SCHEMA has already happened (or the subtest itself already failed).
func TestNewCleanupDropsSchema(t *testing.T) {
	connString := pgtest.ConnString(t)

	var schemaName string
	t.Run("use and finish", func(t *testing.T) {
		db := pgtest.New(t, pg.NewSchema(), connString)
		schemaName = db.SearchPath()

		ctx := context.Background()
		exists, err := db.QueryBoolean(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", schemaName)
		if err != nil {
			t.Fatalf("check schema exists while subtest is running: %v", err)
		}
		if !exists {
			t.Fatalf("expected schema %q to exist while its subtest is still running", schemaName)
		}
	})

	// The subtest above has returned, so New's cleanup (DROP SCHEMA ... CASCADE + pool
	// Close) has already run. Verify it with an independent connection, not one obtained
	// through pgtest.New, since that would just create yet another ephemeral schema.
	db, err := pg.Open(context.Background(), pg.NewSchema(), connString)
	if err != nil {
		t.Fatalf("open verification connection: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	exists, err := db.QueryBoolean(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", schemaName)
	if err != nil {
		t.Fatalf("check schema exists after cleanup: %v", err)
	}
	if exists {
		t.Fatalf("expected schema %q to be dropped after its test finished, but it still exists", schemaName)
	}
}

// countPgtestSchemas reports how many schemas named like New's own pgtest_<16 hex chars>
// currently exist in the target database. It exists so
// TestNewCleanupDropsSchemaAfterSetupFailure can prove no schema leaked without knowing its
// exact generated name. New has no way to report that name once it has already failed via
// tb.Fatalf.
func countPgtestSchemas(t *testing.T, connString string) int64 {
	t.Helper()

	db, err := pg.Open(context.Background(), pg.NewSchema(), connString)
	if err != nil {
		t.Fatalf("open verification connection: %v", err)
	}
	defer db.Close()

	count, err := db.Count(context.Background(),
		`SELECT COUNT(*) FROM pg_namespace WHERE nspname ~ '^pgtest_[0-9a-f]{16}$'`)
	if err != nil {
		t.Fatalf("count pgtest_ schemas: %v", err)
	}

	return count
}

// brokenFKWidget is a registered entity whose foreign key references a table that will never
// exist. Registering it succeeds (the "ref" tag option isn't validated against a live
// database), but the ALTER TABLE .../REFERENCES statement db.CreateSchema issues for it fails
// against the server: a reliable stand-in for "a bad struct tag or unsupported type, only
// caught once CreateSchema actually runs", the scenario TestNewCleanupDropsSchemaAfterSetupFailure
// exercises.
type brokenFKWidget struct {
	ID    string `pg:"type=uuid,primary"`
	RefID string `pg:"type=uuid,ref=pgtest_table_that_does_not_exist(id)"`
}

// runInSubprocess re-invokes the current test binary running only the top-level test named
// testName, with envVar=1 set in its environment, and returns its combined output and whether
// it exited non-zero.
//
// It exists because two of the tests below need to observe New actually calling tb.Fatalf -
// but tb.Fatalf halts the calling goroutine via runtime.Goexit and marks every ancestor test,
// and ultimately this package's own "go test" process exit code, as failed. There is no
// supported way to catch or ignore that from within the same test run (testing.TB also can't
// be faked from outside the testing package: its interface has an unexported method
// specifically to prevent that). Running the failing scenario in a disposable child process
// and asserting on its exit code/output instead keeps this package's own test run green while
// still exercising the real code path in New.
func runInSubprocess(t *testing.T, testName, envVar string) (output string, failed bool) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$", "-test.v")
	cmd.Env = append(os.Environ(), envVar+"=1")

	out, err := cmd.CombinedOutput()
	return string(out), err != nil
}

// childCreateSchemaFailureEnv selects the child branch of TestNewCleanupDropsSchemaAfterSetupFailure.
const childCreateSchemaFailureEnv = "PGTEST_CHILD_CREATE_SCHEMA_FAILURE"

// TestNewCleanupDropsSchemaAfterSetupFailure verifies that when db.CreateSchema fails after
// CREATE SCHEMA has already succeeded, New's cleanup still drops the schema instead of
// leaking it into the target database. Earlier, cleanup was only registered after table
// creation succeeded, so a CreateSchema failure (easy to hit while iterating on a schema,
// e.g. a foreign key to a table that doesn't exist yet) left the pgtest_<hex> schema behind
// with nothing left to drop it.
func TestNewCleanupDropsSchemaAfterSetupFailure(t *testing.T) {
	if os.Getenv(childCreateSchemaFailureEnv) == "1" {
		// Child process branch: actually exercises the failing path. This call is expected to
		// fail via tb.Fatalf (the foreign key target table does not exist), which is exactly
		// what the parent branch below checks for via this process's exit code.
		connString := pgtest.ConnString(t)
		schema := pg.NewSchema()
		schema.MustRegister("broken_fk_widgets", brokenFKWidget{})
		pgtest.New(t, schema, connString)
		return
	}

	connString := pgtest.ConnString(t) // skip here (before spawning a child) if unset.
	before := countPgtestSchemas(t, connString)

	output, childFailed := runInSubprocess(t, "TestNewCleanupDropsSchemaAfterSetupFailure", childCreateSchemaFailureEnv)
	if !childFailed {
		t.Fatalf("expected the child process's New call to fail (foreign key target table does not exist), but it exited 0:\n%s", output)
	}
	if !strings.Contains(output, "create tables in schema") {
		t.Fatalf("expected the failure output to mention table creation failing, got:\n%s", output)
	}

	after := countPgtestSchemas(t, connString)
	if after != before {
		t.Fatalf("expected no leaked pgtest_ schemas after a failed New (before=%d, after=%d): "+
			"the schema New created before CreateSchema failed should still have been dropped by cleanup", before, after)
	}
}

// childSharedSchemaEnv selects the child branch of TestNewRejectsSharedSchema.
const childSharedSchemaEnv = "PGTEST_CHILD_SHARED_SCHEMA"

// TestNewRejectsSharedSchema verifies that a second New call given a *pg.Schema that an
// earlier, still-active New call already holds fails fast (via tb.Fatalf) instead of
// proceeding to race on that Schema's tables (see the concurrency caveat in New's doc).
//
// Both New calls run sequentially in the same child process, on the same *testing.T: the
// first call's cleanup (which releases the reuse guard) is registered on that same T, so it
// cannot have run yet by the time the second call runs immediately afterwards. That makes
// this deterministic rather than a timing-dependent race between the two New calls.
func TestNewRejectsSharedSchema(t *testing.T) {
	if os.Getenv(childSharedSchemaEnv) == "1" {
		connString := pgtest.ConnString(t)
		schema := pg.NewSchema()

		pgtest.New(t, schema, connString) // succeeds; holds the guard until this process exits.
		pgtest.New(t, schema, connString) // must fail via tb.Fatalf: schema already in use.
		return
	}

	pgtest.ConnString(t) // skip here (before spawning a child) if unset.

	output, childFailed := runInSubprocess(t, "TestNewRejectsSharedSchema", childSharedSchemaEnv)
	if !childFailed {
		t.Fatalf("expected a second New call sharing an in-use *pg.Schema to fail, but the child process exited 0:\n%s", output)
	}
	if !strings.Contains(output, "already in use by another") {
		t.Fatalf("expected the failure output to mention the schema reuse guard, got:\n%s", output)
	}
}
