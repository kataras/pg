# Chapter 16: Testing

pg's entire value proposition is the SQL it generates from your
structs and tags: the quoting, the placeholder numbering, the
`ON CONFLICT` clause, the generated column list. A mock of `*pg.DB`
tests none of that; it tests that your code calls a method with the
arguments you told the mock to expect, which is a fact about your
code, not about whether the resulting query is correct PostgreSQL. This
chapter is about testing against a real PostgreSQL server instead, and
about the `pgtest` package this library ships specifically to make
that fast and safe: `pgtest.ConnString` for skipping cleanly when no
database is configured, and `pgtest.New` for an ephemeral, per-test
schema created and torn down automatically. It then covers the
patterns worth building on top of that foundation: table-driven tests
over a repository, testing rollback with `ErrIntentionalRollback`,
asserting on typed failures with `AsConstraintError`, and running all
of it in CI against a service container, the same way this library's
own test suite runs.

## Table of Contents

- [Background](#background)
- [Why a Real Database, Not a Mock](#why-a-real-database-not-a-mock)
- [The pgtest Package](#the-pgtest-package)
- [ConnString and Skipping Cleanly](#connstring-and-skipping-cleanly)
- [New: an Ephemeral Schema per Test](#new-an-ephemeral-schema-per-test)
- [The Shared-Schema Constraint](#the-shared-schema-constraint)
- [A Full Test](#a-full-test)
- [Table-Driven Tests Over a Repository](#table-driven-tests-over-a-repository)
- [Testing Transaction Rollback](#testing-transaction-rollback)
- [Asserting on Typed Errors](#asserting-on-typed-errors)
- [Running the Suite in CI](#running-the-suite-in-ci)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Background

If you are comfortable with Go's `testing` package and table-driven
tests, skip to [Why a Real Database, Not a
Mock](#why-a-real-database-not-a-mock). A Go test is an ordinary
function named `TestXxx(t *testing.T)` that `go test` discovers and
runs; failing an expectation inside it (`t.Fatalf`, `t.Errorf`) marks
that test failed without crashing the rest of the suite. `t.Run` opens
a named subtest, most often used to drive one test function over a
table of cases (a *table-driven test*): a slice of structs, one per
scenario, looped over with `t.Run(tt.name, func(t *testing.T) {
... })` so each case reports independently and adding a new one is
adding a row rather than a new function. `t.Cleanup` registers a
function that runs when the current test (or subtest) finishes,
success or failure, the mechanism `pgtest.New` uses to drop its
schema and close its pool automatically. `testing.TB` is the common
interface `*testing.T` and `*testing.B` both satisfy, which is why a
helper written against `testing.TB` works from either a test or a
benchmark.

## Why a Real Database, Not a Mock

**A mock proves your code called something; it cannot prove the SQL
was correct.** A mock of the query interface pg uses under the hood
can only assert that your code issued *a* call; it cannot tell you
whether the SQL
that call would have produced is valid, whether the `ON CONFLICT`
target you configured actually matches your unique constraint, whether
a `CHECK` constraint you wrote in a struct tag is satisfied, or
whether a generated column's expression even parses. All of that logic
lives in `desc/insert_query.go`, `desc/on_conflict.go`,
`desc/create_table_query.go`, and it only proves itself correct by
actually running against PostgreSQL. Given that, a mock's only honest
job in a pg-based test suite is standing in for something *other* than
the database, an HTTP client to a third-party service, a clock, and
even then the repository layer itself, the part that talks to
PostgreSQL, is exactly the part worth exercising for real.

The practical objection to a real database in tests, that it is slow
and stateful and tests interfere with each other, is what `pgtest`
exists to remove: a schema per test, created and dropped
automatically, so tests are as isolated as a mock would have been, and
fast enough to run on every save against a local PostgreSQL or a CI
service container.

## The pgtest Package

`pgtest` provides two functions: `ConnString`, which reads where to
connect from an environment variable and skips cleanly when it is
unset, and `New`, which creates an isolated schema, materializes your
registered tables in it, and cleans up automatically.

```go
import "github.com/kataras/pg/pgtest"
```

Import it from your own test files (`_test.go`), never from
production code; it depends on `testing.TB` and is meant exclusively
for test setup.

## ConnString and Skipping Cleanly

```go
func TestSomething(t *testing.T) {
    connString := pgtest.ConnString(t)
    // ...
}
```

`ConnString` reads the `PG_CONNSTRING` environment variable. If it is
unset or empty, it calls `t.Skipf` with an explanatory message instead
of guessing a default, so the test reports as skipped, not failed, in
an environment with no database configured. This is a deliberate
difference from pg's own internal test suite (`getTestConnString` in
`db_example_test.go`), which falls back to a hardcoded local
connection string so its own CI job always has something to run
against. `pgtest` has no such fallback, since it is meant to be
imported by downstream projects whose database might live on a
different host, user or password entirely; a hardcoded default there
would be meaningless at best and silently wrong at worst.

## New: an Ephemeral Schema per Test

```go
db := pgtest.New(t, schema, connString)
```

`New` takes the calling test, a `*pg.Schema` with your tables
registered, and a connection string, and does the following, in
order: generates a random schema name (`pgtest_<16 hex chars>`),
opens a fresh `pgxpool.Pool` whose `search_path` defaults to that
schema, issues `CREATE SCHEMA`, registers a `t.Cleanup` that drops the
schema (`CASCADE`, so every table inside it goes too) and closes the
pool, then, if `schema` has any tables registered, calls
`DB.CreateSchema` to materialize them and returns a ready-to-use
`*pg.DB`. Cleanup is registered immediately after the schema itself
exists, specifically so that a later failure (a bad tag, an
unsupported type, a foreign key to a table that does not exist yet
while you are iterating) still drops the schema instead of leaking it
into the target database.

An empty schema (`pg.NewSchema()`, nothing registered) is a valid
argument too: `New` still creates and cleans up the isolated schema,
leaving table creation to you, useful for tests that want a scratch
schema for hand-written DDL rather than the struct-driven path.

## The Shared-Schema Constraint

`New` mutates the `*pg.Schema` you pass it: it repoints every
registered table's `SearchPath` field at the new ephemeral schema name,
in place, because `desc.Table`'s query builders schema-qualify every
`INSERT`/`UPSERT` using that field, captured once at
`Schema.MustRegister` time, rather than the live connection's
`search_path`. Left alone, every insert would keep targeting whatever
schema was in effect when you *registered* the table, `"public"` by
default, not the ephemeral one `New` just created.

Because `schema.Tables()` returns the `Schema`'s own live `*desc.Table`
pointers rather than copies, two `New` calls sharing one `*pg.Schema`
at the same time would be an unsynchronized write to the same memory
from two goroutines, a genuine data race, not merely a source of
occasional flaky assertions. `New` detects this and fails fast: a
second `New` call given a `*pg.Schema` that an earlier, still-active
`New` call already holds calls `t.Fatalf` immediately with a message
naming the reuse, rather than letting the two calls race silently.

The fix is simple and cheap: build a fresh `*pg.Schema` inside each
test, as every example in this chapter does, rather than sharing one
package-level `*pg.Schema` across tests. Registering the same struct
types on a fresh `Schema` repeatedly costs nothing meaningful.

## A Full Test

```go
// widget_test.go
package store_test

import (
    "context"
    "testing"

    "github.com/kataras/pg"
    "github.com/kataras/pg/pgtest"
)

type widget struct {
    ID   string `pg:"type=uuid,primary"`
    Name string `pg:"type=varchar(64)"`
}

func widgetSchema() *pg.Schema {
    schema := pg.NewSchema()
    schema.MustRegister("widgets", widget{})
    return schema
}

func TestWidgetInsertAndSelect(t *testing.T) {
    connString := pgtest.ConnString(t)
    ctx := context.Background()

    db := pgtest.New(t, widgetSchema(), connString)
    repo := pg.NewRepository[widget](db)

    inserted := widget{Name: "gear"}
    var id string
    if err := repo.InsertSingle(ctx, inserted, &id); err != nil {
        t.Fatalf("insert single: %v", err)
    }
    if id == "" {
        t.Fatal("expected a generated uuid primary key")
    }

    got, err := repo.SelectByID(ctx, id)
    if err != nil {
        t.Fatalf("select by id: %v", err)
    }
    if got.Name != "gear" {
        t.Fatalf("expected name %q, got %q", "gear", got.Name)
    }
}
```

Run it with the environment variable set, against any reachable
PostgreSQL:

```sh
PG_CONNSTRING="postgres://postgres:pass@localhost:5432/test_db?sslmode=disable" go test ./... -v
```

Run it with `PG_CONNSTRING` unset and the test reports `SKIP`, not
`FAIL`, so a contributor without a local database still gets a clean
`go test ./...` for everything else in the package.

## Table-Driven Tests Over a Repository

The table-driven shape composes directly with `pgtest.New`: build one
fresh schema and repository per subtest (or once per top-level test,
if the cases do not interfere with each other's rows) and loop over
the cases as usual.

```go
func TestWidgetValidation(t *testing.T) {
    connString := pgtest.ConnString(t)
    ctx := context.Background()

    tests := []struct {
        name    string
        widget  widget
        wantErr bool
    }{
        {"valid name", widget{Name: "gear"}, false},
        // No NOT NULL/CHECK constraint on Name in this table.
        {"empty name", widget{Name: ""}, false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            db := pgtest.New(t, widgetSchema(), connString)
            repo := pg.NewRepository[widget](db)

            var id string
            err := repo.InsertSingle(ctx, tt.widget, &id)
            if (err != nil) != tt.wantErr {
                t.Fatalf("InsertSingle() error = %v, wantErr %v",
                    err, tt.wantErr)
            }
        })
    }
}
```

Building a new schema per subtest costs one `CREATE SCHEMA` and, for
this table, one `CREATE TABLE`, both cheap operations; it buys
complete isolation between cases, so an earlier case's rows can never
change a later case's assertions, and `t.Parallel()` is safe to add to
each subtest if the suite grows large enough to want it. (`pgtest.New`
itself has no built-in concurrency limit beyond the shared-schema
constraint described above: two subtests running in parallel are fine
as long as each builds its own `*pg.Schema`, which this pattern
already does.)

## Testing Transaction Rollback

`ErrIntentionalRollback` ([Chapter 9](09-transactions.md)) is a
sentinel your `InTransaction` callback can return to roll back on
purpose, useful in tests that want to verify a sequence of writes
without persisting any of them, or that want to prove a rollback
genuinely discards work rather than merely returning an error:

```go
func TestInsertRollsBackCleanly(t *testing.T) {
    connString := pgtest.ConnString(t)
    ctx := context.Background()

    db := pgtest.New(t, widgetSchema(), connString)

    err := db.InTransaction(ctx, func(tx *pg.DB) error {
        repo := pg.NewRepository[widget](tx)

        var id string
        toInsert := widget{Name: "should not persist"}
        if err := repo.InsertSingle(ctx, toInsert, &id); err != nil {
            return err
        }

        return pg.ErrIntentionalRollback
    })
    if err != nil {
        t.Fatalf("expected nil after an intentional rollback, got: %v", err)
    }

    repo := pg.NewRepository[widget](db)
    count, err := repo.Count(ctx, "SELECT COUNT(*) FROM widgets")
    if err != nil {
        t.Fatalf("count: %v", err)
    }
    if count != 0 {
        t.Fatalf("expected 0 rows after rollback, got %d", count)
    }
}
```

`InTransaction` returns `nil` when `fn` returns
`pg.ErrIntentionalRollback`, since the rollback itself succeeded and
was requested, not an error condition; asserting `err == nil` here is
therefore the correct check, and the row count query is what actually
proves the insert never committed. This is the same pattern the
library's own suite uses to verify `InTransaction`'s rollback
behavior end to end, against a real transaction rather than an
assumption about what `ROLLBACK` does.

## Asserting on Typed Errors

`AsConstraintError` ([Chapter 10](10-errors.md), and see
[Chapter 14](14-security.md) for why you should prefer it over parsing
error text) turns a PostgreSQL constraint violation into a typed value
you can assert on directly, instead of matching against a driver error
string that could change wording between versions:

```go
type product struct {
    ID  string `pg:"type=uuid,primary"`
    SKU string `pg:"type=varchar(32),unique"`
}

func productSchema() *pg.Schema {
    schema := pg.NewSchema()
    schema.MustRegister("products", product{})
    return schema
}

func TestDuplicateSKURejected(t *testing.T) {
    connString := pgtest.ConnString(t)
    ctx := context.Background()

    db := pgtest.New(t, productSchema(), connString)
    repo := pg.NewRepository[product](db)

    var firstID string
    first := product{SKU: "WIDGET-1"}
    if err := repo.InsertSingle(ctx, first, &firstID); err != nil {
        t.Fatalf("first insert: %v", err)
    }

    var secondID string
    err := repo.InsertSingle(ctx, product{SKU: "WIDGET-1"}, &secondID)
    if err == nil {
        t.Fatal("expected a unique constraint violation on the duplicate SKU")
    }

    cerr, ok := pg.AsConstraintError(err)
    if !ok {
        t.Fatalf("expected a *pg.ConstraintError, got: %v", err)
    }
    if cerr.Kind != pg.ConstraintUnique {
        t.Fatalf("expected ConstraintUnique, got: %s", cerr.Kind)
    }
}
```

Asserting on `cerr.Kind` rather than on `err.Error()`'s text keeps the
test tied to what PostgreSQL actually reports (the SQLSTATE class),
not to incidental message wording, so it stays correct across
PostgreSQL versions and across whether the violated constraint was
declared `unique` or `unique_index` in the struct tag.

## Running the Suite in CI

pg's own test suite is a working example of every pattern in this
chapter, run against a real PostgreSQL service container rather than
any form of mock. Its GitHub Actions workflow starts a
`postgres:16-alpine` service alongside the test job:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    env:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: admin!123
      POSTGRES_DB: test_db
    ports:
      - 5432:5432
    options: >-
      --health-cmd pg_isready
      --health-interval 10s
      --health-timeout 5s
      --health-retries 5

steps:
  - uses: actions/setup-go@v6
    with:
      go-version: 1.26.x
  - uses: actions/checkout@v6
  - run: go test -v ./...
```

The service container's health check (`pg_isready`, polled every 10
seconds up to 5 times) ensures the job's `go test` step never starts
racing a database that has not finished starting up. The library's own
`getTestConnString` helper falls back to a hardcoded connection string
matching these exact service credentials when `PG_CONNSTRING` is
unset, which is why `go test -v ./...` works unmodified in this CI
job with no extra environment configuration; a downstream project
using `pgtest` instead sets `PG_CONNSTRING` explicitly as a workflow
environment variable, pointing at its own service container, and gets
the clean-skip behavior described earlier for any environment where
that variable is absent (a contributor's laptop with no local
PostgreSQL, for instance).

## Summary

- Mock `*pg.DB` tests your code's calls, not the SQL pg generates from
  your structs; a real PostgreSQL is the only thing that proves the
  query itself is correct, which is the entire value this library
  provides.
- `pgtest.ConnString(t)` reads `PG_CONNSTRING` and skips cleanly (not
  a failure) when it is unset, with no hardcoded fallback, unlike the
  library's own internal test helper.
- `pgtest.New(t, schema, connString)` creates a randomly named,
  isolated schema, materializes the registered tables in it via
  `DB.CreateSchema`, and drops it (plus closes the pool) automatically
  on cleanup.
- `New` mutates the `*pg.Schema` you pass it (retargeting every
  table's `SearchPath`), so build a fresh `*pg.Schema` per test rather
  than sharing one; `New` detects and fails fast on a shared,
  still-active `*pg.Schema` instead of racing.
- `pg.ErrIntentionalRollback` lets a transaction test prove a rollback
  genuinely discarded its writes, not merely that an error was
  returned.
- `pg.AsConstraintError` and `ConstraintKind` let a test assert on the
  SQLSTATE class of a failure directly, immune to message wording
  changes across PostgreSQL versions.
- The library's own suite runs this same way in CI: a
  `postgres:16-alpine` service container, gated by a health check,
  with a connection string sourced from `PG_CONNSTRING` (or, for the
  library's own tests only, a matching hardcoded default).

## Further Reading

- [Go: Testing package](https://pkg.go.dev/testing): `*testing.T`,
  `t.Run` subtests, `t.Cleanup`, `t.Skipf`, and the `testing.TB`
  interface `pgtest` is written against.
- [Go: Table-driven tests](https://go.dev/wiki/TableDrivenTests): the
  idiom behind every table-driven example in this chapter.
- [GitHub Actions: Service containers](https://docs.github.com/en/actions/using-containerized-services/about-service-containers):
  the mechanism behind the `postgres:16-alpine` service in this
  chapter's CI example.
- [PostgreSQL: Error Codes](https://www.postgresql.org/docs/current/errcodes-appendix.html):
  the full SQLSTATE table `ConstraintKind` and `AsConstraintError`
  classify against.
- [PostgreSQL: CREATE SCHEMA](https://www.postgresql.org/docs/current/sql-createschema.html):
  the statement `pgtest.New` issues to create each test's isolated
  namespace.

---

**Next**: [Epilogue](epilogue.md)
