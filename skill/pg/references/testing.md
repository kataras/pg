# Testing

## What needs a live PostgreSQL, and what does not

| Package | DB-free? | Verified by |
| --- | --- | --- |
| `desc/*_test.go` | Yes, entirely | No file under `desc/` references `pgxpool`, `pg.Open` or a connection string; every test asserts either a golden SQL string or a struct/reflection result |
| `gen/*_test.go` | Mostly | `export_options_test.go`, `schema_columns_gen_test.go`, `validate_test.go` are DB-free; `db_schema_gen_example_test.go` is an `Example` that opens a real connection |
| Root package (`pg`) | No, mostly | Most files are `Example*` functions or live `Test*` functions that open a connection. A handful of plain `_test.go` files (`where_test.go`, `errors_test.go`, `retry_test.go`, `donothing_test.go`, `null_test.go`, `pagination_test.go`, `schema_test.go`, `db_crud_test.go`, `db_transaction_test.go`) are DB-free unit tests of pure logic (query-fragment building, error classification, backoff math, validation). Files named `*_live_test.go` always need a server; several plain `_test.go` files (`migrate_test.go`, `scan_test.go`, `tx_test.go`, `db_options_test.go`, `db_quoting_test.go`, `db_secrets_test.go`, `db_table_listener_test.go`) also do, despite the name. Do not assume "no `_live_test` suffix" means "DB-free" in the root package. When in doubt, `grep -l pgxpool\\.\|pg\\.Open\\( <file>` |

Run `go test ./desc/... ./gen/...` while iterating: fast, no server needed. Run the full
suite (`go test ./...`, or `.claude/scripts/live-test.sh`/`.ps1`) before claiming done, not
after every edit: it takes roughly five minutes against a remote server.

## PG_CONNSTRING

The root package's own test helper, `getTestConnString` (`db_example_test.go`), reads
`PG_CONNSTRING` and falls back to a **hardcoded** connection string matching this
repository's CI service container (`host=localhost port=5432 user=postgres
password=admin!123 dbname=test_db sslmode=disable search_path=public`) when it is unset -
so an unset `PG_CONNSTRING` does not skip these tests, it just points them at a local
default that fails loudly if nothing is listening there. This fallback exists only for this
repository's own CI job; never rely on it locally without a matching PostgreSQL running.

`pgtest.ConnString(tb)` (`pgtest/pgtest.go`), used by *downstream* projects that import
`pgtest`, behaves differently and deliberately: it has **no fallback**, and calls
`tb.Skipf` when `PG_CONNSTRING` is empty, so a contributor with no local database still
gets a clean, skipped (not failed) `go test ./...`. Never hardcode a connection string into
a tracked file; it belongs in the environment only.

```sh
PG_CONNSTRING="host=... port=5432 user=... password=... search_path=public dbname=... sslmode=require" go test ./...
```

## pgtest

`pgtest.New(tb, schema, connString) *pg.DB` creates a randomly named schema
(`pgtest_<16 hex chars>`), points a fresh pool's `search_path` at it, materializes
`schema`'s registered tables via `DB.CreateSchema` if any are registered, and registers a
`tb.Cleanup` that drops the schema (`CASCADE`) and closes the pool.

**Shared-`Schema` constraint.** `New` mutates the `*pg.Schema` it is given: it retargets
every registered table's `SearchPath` field at the new ephemeral schema, in place, because
`desc.Table`'s INSERT/UPSERT builders schema-qualify using that field (captured once at
`Register` time), not the live connection's `search_path`. Because `schema.Tables()`
returns the `Schema`'s own live `*desc.Table` pointers, two concurrent `New` calls sharing
one `*pg.Schema` would be an unsynchronized write to the same memory from two goroutines -
a genuine data race. `New` detects this (`inUseSchemas sync.Map`) and calls `tb.Fatalf`
immediately on the second overlapping call instead of racing. Build a fresh `*pg.Schema`
inside every test (or subtest), never share one at package level:

```go
func widgetSchema() *pg.Schema {
    schema := pg.NewSchema()
    schema.MustRegister("widgets", widget{})
    return schema
}

func TestWidget(t *testing.T) {
    connString := pgtest.ConnString(t)
    db := pgtest.New(t, widgetSchema(), connString)
    repo := pg.NewRepository[widget](db)
    // ...
}
```

## The golden tests in desc are the safety net

`desc/insert_query_test.go`, `desc/create_table_query_test.go`, `desc/on_conflict_test.go`
and their siblings assert the **exact SQL string** each builder emits. This library's
entire value is the SQL it generates, and a refactor that quietly changes a query is
otherwise invisible. If a golden test fails after touching a builder, the default
assumption is that the change broke something, not that the golden is stale. State the
before/after SQL explicitly when a change genuinely must alter emitted SQL; do not "fix"
a failing golden by updating its expected string without that justification.

## The race detector on windows/arm64

`-race` is **unsupported on windows/arm64**. CI runs it (`go test -race -count=1 ./...`,
linux/amd64) so it is exercised there; locally on an unsupported platform the same tests
still run and still exercise the locking, just without the detector attached.

## CI

`.github/workflows/ci.yml` runs two jobs on push/PR to `main`:

| Job | What it does |
| --- | --- |
| `test` | `postgres:16-alpine` service container (`POSTGRES_USER=postgres`, `POSTGRES_PASSWORD=admin!123`, `POSTGRES_DB=test_db`, health-checked via `pg_isready`), then `go vet ./...` and `go test -race -count=1 ./...` on Go 1.27.x |
| `lint` | `golangci-lint-action@v9` pinned to `golangci-lint` v2.13 (the v2 config schema in `.golangci.yml`). v2.13 is the floor, not a preference: it is the first release built with Go 1.27, and older builds cannot parse generic methods. See the comment block at the top of `ci.yml` for why the action is pinned to v9 |

`_examples/` and `_benchmarks/` are separate Go modules (their own `go.mod`, pinned older
dependencies via `replace ../../`) excluded from CI and from `golangci-lint` via
`linters.exclusions.paths`; do not run `go mod tidy` in them and do not treat their failure to
build as something a change here broke.

## Example* tests

Several root-package files (`schema_example_test.go`, `repository_example_test.go`,
`listener_example_test.go`, `db_information_example_test.go`,
`db_table_listener_example_test.go`, `repository_table_listener_example_test.go`,
`gen/db_schema_gen_example_test.go`) are Go `Example` functions: executable documentation
whose `// Output:` comment is checked against real stdout, run against a real connection
via the same `getTestConnString` fallback described above. Treat them as both tests and as
the canonical usage examples for the APIs they cover.
