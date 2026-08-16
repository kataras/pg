# Chapter 3: Connections and Configuration

A pg `*DB` is a thin wrapper around exactly one `*pgxpool.Pool`, and
almost everything about how that pool behaves, how it authenticates,
how many connections it keeps open, whether it encrypts traffic, is
decided once, at `Open` time. This chapter covers both entry points
(`Open` and `OpenPool`), the connection string format pg delegates to
pgx to parse, every `ConnectionOption` the library defines, the pool
settings you reach through connection string parameters rather than
Go code, `search_path`, `sslmode` (what each mode actually protects
against, and what it does not), running behind PgBouncer in
transaction-pooling mode, and the health and pool-statistics calls you
would wire into a readiness endpoint. Every function signature and
default value below is checked against `db.go`, `db_options.go`,
`db_health.go`, `db_stat.go` and the installed `pgx/v5`/`pgxpool`
source.

## Table of Contents

- [Open and OpenPool](#open-and-openpool)
- [Connection Strings](#connection-strings)
- [Connection Options](#connection-options)
- [Pool Settings](#pool-settings)
- [Search Path](#search-path)
- [SSL Modes](#ssl-modes)
- [PgBouncer and Transaction Pooling](#pgbouncer-and-transaction-pooling)
- [Closing a DB](#closing-a-db)
- [Health Checks and Pool Statistics](#health-checks-and-pool-statistics)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Open and OpenPool

```go
func Open(ctx context.Context, schema *Schema, connString string,
    opts ...ConnectionOption) (*DB, error)

func OpenPool(schema *Schema, pool *pgxpool.Pool) *DB
```

`Open` parses `connString` with `pgxpool.ParseConfig`, applies every
`ConnectionOption` in order, builds a `*pgxpool.Pool` with
`pgxpool.NewWithConfig`, and pings it once before returning; a failed
ping (or a failed config parse, or a failed option) returns an error
and no `*DB`. `OpenPool` skips all of that and wraps a
`*pgxpool.Pool` you already built and configured yourself, copying its
connection config and resolving `search_path` the same way `Open`
does. Reach for `OpenPool` when you need pool construction options
`ConnectionOption` does not expose, or when you are sharing one pool
across more than one `Schema`.

Both return the same `*DB`:

```go
type DB struct {
    Pool              *pgxpool.Pool
    ConnectionOptions *pgx.ConnConfig
    // unexported: searchPath, tx, schema, ...
}
```

`ConnectionOptions` is a copy of the pool's `pgx.ConnConfig` as it
stood right after `Open`/`OpenPool` ran; it should not be mutated, and
changing it after the fact has no effect on the live pool.

## Connection Strings

pg does not parse connection strings itself. `Open` hands the string
straight to `pgxpool.ParseConfig`, so both forms pgx accepts work
unchanged:

```go
// Keyword/value (DSN) form.
connString := "host=localhost port=5432 user=postgres " +
    "password=admin!123 dbname=test_db sslmode=verify-full " +
    "search_path=public pool_max_conns=10"

// URL form.
connString := "postgres://postgres:admin!123@localhost:5432/test_db" +
    "?sslmode=verify-full&search_path=public&pool_max_conns=10"

db, err := pg.Open(context.Background(), schema, connString)
```

Both forms carry the same parameters; which one you reach for is a
matter of taste (the URL form composes naturally from a single
`DATABASE_URL` environment variable, the keyword form reads well when
you are building the string field by field). Both accept every
standard `libpq`-style parameter (`host`, `port`, `user`, `password`,
`dbname`, `sslmode` and the rest), plus the pg-specific ones pgxpool
adds on top for pool sizing (covered [below](#pool-settings)) and the
ones covered under [Connection Options](#connection-options)
(`default_query_exec_mode`, `statement_cache_capacity`,
`description_cache_capacity`).

## Connection Options

A `ConnectionOption` is `func(*pgxpool.Config) error`, applied by
`Open` in the order given, after the connection string is parsed and
before the pool is built:

```go
type ConnectionOption func(*pgxpool.Config) error
```

| Function | Signature | What it sets |
| --- | --- | --- |
| `WithLogger` | `(logger tracelog.Logger) ConnectionOption` | Installs a `tracelog.TraceLog` tracer hardcoded to `tracelog.LogLevelTrace`, the most verbose level. |
| `WithLoggerLevel` | `(logger tracelog.Logger, level tracelog.LogLevel) ConnectionOption` | Same as `WithLogger`, with the level you choose instead of a hardcoded `LogLevelTrace`. |
| `WithQueryTracer` | `(tracers ...pgx.QueryTracer) ConnectionOption` | Appends one or more `pgx.QueryTracer` implementations (an OpenTelemetry tracer, custom metrics), composing with any tracer already installed via pgx's `multitracer` package. |
| `WithDefaultQueryExecMode` | `(mode pgx.QueryExecMode) ConnectionOption` | Sets pgx's default query execution mode. Equivalent to `default_query_exec_mode` in the connection string. See [PgBouncer](#pgbouncer-and-transaction-pooling). |
| `WithStatementCacheCapacity` | `(n int) ConnectionOption` | Sets the automatic prepared-statement cache size (`0` disables it). Equivalent to `statement_cache_capacity` in the connection string. |
| `WithDescriptionCacheCapacity` | `(n int) ConnectionOption` | Sets the statement-description cache size used by pgx's describe exec mode. Equivalent to `description_cache_capacity` in the connection string. |

`WithLogger` logs every SQL statement pgx executes together with all
of its bind arguments, plaintext passwords passed to
`SelectByUsernameAndPassword` included. It exists for local
development; do not point it at a production logger. Use
`WithLoggerLevel` with a lower level (`tracelog.LogLevelWarn` or
`tracelog.LogLevelError`) to reduce verbosity, or
`tracelog.LogLevelNone` to disable pgx's tracer logging outright. Note
that pgx still logs the statement and its bind arguments for a
*failed* query at every level down to and including
`tracelog.LogLevelError`; only `LogLevelNone` guarantees sensitive
bind arguments never reach the logger.

Two ordering caveats matter when combining options in one `Open` call.
`WithLogger`/`WithLoggerLevel` overwrite the pool's tracer slot
outright rather than composing with whatever is already there, so pass
them *before* `WithQueryTracer` in the option list, the reverse order
silently drops the tracer(s) `WithQueryTracer` installed. And
`WithQueryTracer` with zero tracers is a no-op that never clears an
already-installed tracer.

```go
db, err := pg.Open(ctx, schema, connString,
    pg.WithLoggerLevel(myLogger, tracelog.LogLevelWarn),
    pg.WithQueryTracer(otelTracer),
    pg.WithStatementCacheCapacity(0), // see PgBouncer, below
)
```

## Pool Settings

Pool sizing and lifecycle are not `ConnectionOption` values; they are
connection string parameters that `pgxpool.ParseConfig` reads
directly (both DSN and URL forms accept them):

| Parameter | Meaning | Default |
| --- | --- | --- |
| `pool_max_conns` | Maximum number of pool connections. | `4`, or `runtime.NumCPU()` if larger |
| `pool_min_conns` | Minimum number of connections the pool tries to keep open. | `0` |
| `pool_min_idle_conns` | Minimum number of idle connections kept ready. | `0` |
| `pool_max_conn_lifetime` | Maximum age of a connection before it is closed and replaced. | `1h` |
| `pool_max_conn_lifetime_jitter` | Random jitter subtracted from `pool_max_conn_lifetime`, so connections do not all expire at once. | `0` |
| `pool_max_conn_idle_time` | Maximum time a connection may sit idle before being closed. | `30m` |
| `pool_health_check_period` | How often the pool checks idle connections. | `1m` |

```go
connString := "postgres://postgres:admin!123@localhost:5432/test_db" +
    "?sslmode=verify-full" +
    "&pool_max_conns=25&pool_min_conns=5" +
    "&pool_max_conn_lifetime=1h30m&pool_max_conn_idle_time=15m"
```

Size `pool_max_conns` against what your PostgreSQL server (or
PgBouncer in front of it) actually allows, not against how many
goroutines your program might run concurrently: PostgreSQL's own
`max_connections` is a hard, shared ceiling across every client
connected to it, and an oversized pool from one service can starve
every other client of the server.

## Search Path

`OpenPool` (and therefore `Open`, which delegates to it) resolves the
schema's search path from the connection config's `search_path`
runtime parameter; if that is empty or unset, it falls back to
`desc.DefaultSearchPath`, `"public"` by default (`SetDefaultSearchPath`
changes the package-wide default before you call `Open`):

```go
pg.SetDefaultSearchPath("app") // before Open, if you never pass
                                // search_path in the connection string
```

`db.SearchPath()` returns whatever was resolved, and it is what
`CreateSchema` uses for its `CREATE SCHEMA IF NOT EXISTS <path>;`
statement and what every generated query qualifies table names with.
Passing `search_path=` explicitly in the connection string is
preferable to relying on the package-wide default whenever more than
one `*DB` in the same process might target different schemas.

## SSL Modes

`sslmode` is an ordinary connection string parameter, handled entirely
by pgx (specifically `pgconn`'s TLS configuration), not by pg. pgx's
default, when `sslmode` is omitted, is `prefer`, matching `libpq`'s
own default; pg does not override it. Every example in this book (and
in `Open`'s own godoc) uses `sslmode=disable` for a local database
with no TLS listener at all; that is a development convenience, not a
recommendation.

| Mode | What it actually guarantees |
| --- | --- |
| `disable` | No encryption. Traffic, including the password in the initial handshake, is plaintext on the wire. |
| `allow` | Tries a plaintext connection first, only upgrading to TLS if the server demands it. |
| `prefer` (default) | Tries TLS first, falling back to plaintext if the server does not offer it. Encrypted when possible, but silently not when the server (or a network intermediary) does not cooperate. |
| `require` | Always encrypts, but does not verify the server's certificate against a root CA, so it protects against passive eavesdropping only, not against a man-in-the-middle presenting any certificate. (pgx follows `libpq` here: if `sslrootcert` is also set, `require` behaves like `verify-ca` instead.) |
| `verify-ca` | Encrypts and verifies the server's certificate chain against a trusted root CA, but does not check that the certificate's hostname matches the server you connected to. |
| `verify-full` | Encrypts, verifies the certificate chain, and verifies the hostname. The only mode that defends against a man-in-the-middle presenting a validly-signed certificate for a different host. |

For a production connection, prefer `sslmode=verify-full` (pointing
`sslrootcert` at the certificate authority that signed your database
server's certificate, if it is not one your operating system already
trusts), or `sslmode=require` at an absolute minimum when
`verify-full` is not achievable and you accept the man-in-the-middle
exposure that implies. `Open`'s own doc comment carries the same
recommendation.

## PgBouncer and Transaction Pooling

pgx defaults to `QueryExecModeCacheStatement`
(`default_query_exec_mode=cache_statement`), which prepares and caches
statements server-side, keyed by SQL text, and reuses that prepared
statement on the same physical connection. That assumption breaks
under PgBouncer's transaction pooling mode, where a client's logical
connection can be handed a *different* physical server connection
between transactions: a statement prepared on one backend connection
may simply not exist on the next one PgBouncer hands you.

`WithDefaultQueryExecMode` (or `default_query_exec_mode` in the
connection string) is how you tell pgx not to rely on server-side
prepared statements surviving across queries, and it is the setting
that actually fixes the connection: with it set, pgx never populates
either its statement cache or its description cache during normal
operation.

```go
db, err := pg.Open(ctx, schema, connString,
    pg.WithDefaultQueryExecMode(pgx.QueryExecModeSimpleProtocol),
    pg.WithStatementCacheCapacity(0),
    pg.WithDescriptionCacheCapacity(0),
)
```

`default_query_exec_mode` accepts `cache_statement` (the default),
`cache_describe`, `describe_exec`, `exec` or `simple_protocol` as a
connection string value. `QueryExecModeSimpleProtocol` (or
`QueryExecModeExec`) avoids server-side prepared statements
altogether, at the cost of client-side parameter interpolation for the
simple protocol case. `WithStatementCacheCapacity(0)` and
`WithDescriptionCacheCapacity(0)` are not required for that default
behavior, but `*DB`'s `Query`/`Exec`/`QueryRow` pass their `args`
straight through to pgx, which recognizes a `pgx.QueryExecMode` value
among those args as a per-call override. Setting both capacities to
zero (or `statement_cache_capacity=0`/`description_cache_capacity=0`
in the connection string) means a call that explicitly opts back into
`QueryExecModeCacheStatement` or `QueryExecModeCacheDescribe` fails
immediately with an explicit error instead of silently caching a
statement handle, which is exactly as unsafe to reuse across a pooled
connection swap as a server-prepared one. Set all three together.

## Closing a DB

```go
func (db *DB) Close()
```

`Close` closes the underlying `*pgxpool.Pool` (and, transitively,
every connection it holds open); call it once, typically with `defer`
right after a successful `Open`, for the lifetime of your program or
service. A `*DB` returned from `Begin`/`InTransaction` (transaction
scoped) shares the same pool and does not need, and should not
receive, its own `Close` call.

## Health Checks and Pool Statistics

```go
func (db *DB) Ping(ctx context.Context) error
func (db *DB) Health(ctx context.Context) (Health, error)
func (db *DB) PoolStat() PoolStat
```

`Ping` acquires a connection (if necessary) and verifies the database
is reachable; it always goes through `db.Pool`, even when `db` is
transaction-scoped, since a liveness check is about the database as a
whole, not the specific connection a given transaction happens to be
pinned to, and it never touches or invalidates an in-flight
transaction.

`Health` builds on `Ping`: it pings, then reads the server version
(`DB.GetVersion`) and a `PoolStat` snapshot, returning both in one
`Health` value, or the ping/version error if the database is
unreachable:

```go
type Health struct {
    ServerVersion string   `json:"server_version"`
    Pool          PoolStat `json:"pool"`
}
```

`PoolStat` mirrors `pgxpool`'s own `*pgxpool.Stat`, translated into a
plain, JSON-serializable struct: `AcquireCount`, `AcquireDuration`,
`AcquiredConns`, `CanceledAcquireCount`, `ConstructingConns`,
`EmptyAcquireCount`, `IdleConns`, `MaxConns` and `TotalConns`. Wiring
`Health` (or `PoolStat` alone) into a `/healthz` or `/readyz` endpoint
gives you both the database's reachability and, over time, an early
signal that `pool_max_conns` is undersized (`EmptyAcquireCount`
climbing, `AcquiredConns` pinned at `MaxConns`) before requests start
timing out waiting for a connection.

## Summary

- `Open(ctx, schema, connString, opts...)` parses a connection
  string with `pgxpool.ParseConfig`, applies every `ConnectionOption`,
  builds a pool and pings it; `OpenPool(schema, pool)` wraps a pool
  you already built yourself.
- Both DSN (`key=value ...`) and URL (`postgres://...`) connection
  string forms work; pg does not parse them itself.
- `WithLogger`/`WithLoggerLevel` install a tracer, `WithQueryTracer`
  composes additional `pgx.QueryTracer`s, and
  `WithDefaultQueryExecMode`/`WithStatementCacheCapacity`/
  `WithDescriptionCacheCapacity` mirror connection string parameters
  of the same purpose.
- `pool_max_conns` (default 4, or `NumCPU()` if larger),
  `pool_min_conns`, `pool_min_idle_conns`,
  `pool_max_conn_lifetime`(`_jitter`), `pool_max_conn_idle_time` and
  `pool_health_check_period` are connection string parameters, not
  Go options.
- `search_path` resolves from the connection string, falling back to
  `desc.DefaultSearchPath` (`"public"`); `db.SearchPath()` reads it
  back.
- Prefer `sslmode=verify-full` in production; `require` still
  encrypts but does not defend against a man-in-the-middle, and
  `disable`/`allow`/`prefer` may hand you an unencrypted connection
  outright.
- Behind PgBouncer's transaction pooling, pair
  `WithDefaultQueryExecMode(pgx.QueryExecModeSimpleProtocol)` (or
  `QueryExecModeExec`) with `WithStatementCacheCapacity(0)`.
- `db.Close()` closes the pool; `db.Ping`, `db.Health` and
  `db.PoolStat()` are the building blocks for a readiness endpoint.

## Further Reading

- [pgxpool godoc](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool):
  `ParseConfig`'s full parameter list, including every pool setting
  in this chapter.
- [PostgreSQL: Connection Strings](https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING):
  the canonical DSN/URL syntax pgx implements.
- [PostgreSQL: SSL Support](https://www.postgresql.org/docs/current/libpq-ssl.html#LIBPQ-SSL-PROTECTION):
  the authoritative description of what each `sslmode` value
  protects against.
- [PgBouncer: Pooling Modes](https://www.pgbouncer.org/features.html#pooling-modes):
  the transaction pooling behavior that makes server-side prepared
  statements unsafe.
- [pgx QueryExecMode godoc](https://pkg.go.dev/github.com/jackc/pgx/v5#QueryExecMode):
  the exec modes `WithDefaultQueryExecMode` chooses between.

---

**Next Chapter**: [Repositories and CRUD](04-repositories-and-crud.md)
