# Chapter 15: Observability and Operations

Getting a query to run correctly is only part of running pg in
production. This chapter covers the rest: telling your orchestrator
whether the database is actually reachable, watching the connection
pool for the counters that predict an outage before it happens,
choosing how much of your SQL ends up in your logs, wiring in a
tracer without pg ever depending on one, sizing the pool and picking
an exec mode for the deployment you actually have (including the one
where PgBouncer forces your hand), and reasoning about timeouts and
retries in terms an on-call engineer can act on at 3 a.m. Every API
named here is read directly from `db_health.go`, `db_stat.go`,
`db_options.go`, `db.go`, `db_information.go` and `retry.go`.

## Table of Contents

- [Readiness and Liveness](#readiness-and-liveness)
- [Pool Statistics](#pool-statistics)
- [Logging](#logging)
- [Query Tracers and OpenTelemetry](#query-tracers-and-opentelemetry)
- [The WithLogger/WithQueryTracer Ordering Caveat](#the-withloggerwithquerytracer-ordering-caveat)
- [Connection Pool Sizing](#connection-pool-sizing)
- [Exec Modes and Prepared-Statement Caching](#exec-modes-and-prepared-statement-caching)
- [PgBouncer and Transaction Pooling](#pgbouncer-and-transaction-pooling)
- [Vacuum and Table Sizes](#vacuum-and-table-sizes)
- [Timeouts](#timeouts)
- [Retrying Transient Failures](#retrying-transient-failures)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Readiness and Liveness

`DB.Ping` acquires a connection from the pool (opening one if
necessary) and reports whether the database answered:

```go
if err := db.Ping(ctx); err != nil {
    // not reachable.
}
```

`Ping` always goes through the pool directly, even when called on a
transaction-scoped `*DB` (one returned inside `InTransaction`'s
callback): `pgx.Tx` has no `Ping` of its own, and a liveness check is
about whether the database is reachable at all, not about the specific
connection a given transaction happens to be pinned to. Calling it
inside a transaction therefore never touches, and cannot invalidate,
that transaction.

`DB.Health` builds on `Ping` for a richer readiness response: it pings,
then reports the server version and a pool snapshot together as a
`Health` value:

```go
type Health struct {
    ServerVersion string   `json:"server_version"`
    Pool          PoolStat `json:"pool"`
}
```

```go
func healthHandler(db *pg.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        health, err := db.Health(r.Context())
        if err != nil {
            http.Error(w, err.Error(), http.StatusServiceUnavailable)
            return
        }

        json.NewEncoder(w).Encode(health)
    }
}
```

Use `Ping` for a minimal liveness probe (is the process still able to
talk to the database at all) and `Health` for a readiness endpoint
where you also want to surface the server version and pool pressure to
whatever is polling it, a deploy script, a dashboard, an on-call
runbook. Like `Ping`, `Health` always checks the pool directly and
never disturbs an in-flight transaction.

## Pool Statistics

`DB.PoolStat` returns a point-in-time snapshot of the connection
pool's counters as a plain, JSON-serializable `PoolStat` struct, built
directly from `pgxpool.Pool.Stat()`:

| Field | Meaning |
| --- | --- |
| `AcquireCount` | Cumulative count of successful acquires. |
| `AcquireDuration` | Cumulative time spent acquiring connections. |
| `AcquiredConns` | Connections currently checked out. |
| `CanceledAcquireCount` | Acquires canceled by a context before completing. |
| `ConstructingConns` | Connections currently being established. |
| `EmptyAcquireCount` | Acquires that had to wait because the pool was empty. |
| `IdleConns` | Connections currently idle in the pool. |
| `MaxConns` | The pool's configured maximum size. |
| `TotalConns` | `ConstructingConns + AcquiredConns + IdleConns`. |

Two of these are worth alerting on directly rather than only eyeballing
on a dashboard. `EmptyAcquireCount` climbing means requests are
regularly waiting for a connection because none were free, the first
sign your pool is undersized (or a slow query, or a leak, is holding
connections longer than it should) for the load you are actually
serving. `AcquiredConns` sitting at or near `MaxConns` for sustained
periods is the same symptom from a different angle: the pool has no
headroom left, and the next burst of traffic queues instead of
proceeding. `CanceledAcquireCount` rising tells you requests are timing
out while waiting for a connection, which is what an undersized pool
looks like from the caller's side, a context deadline expiring during
`Acquire` rather than during the query itself.

```go
stat := db.PoolStat()
if stat.AcquiredConns >= stat.MaxConns {
    log.Warn("connection pool saturated", "max", stat.MaxConns)
}
```

## Logging

`WithLogger` and `WithLoggerLevel` install a `pgx` `tracelog.TraceLog`
tracer as a `ConnectionOption` passed to `pg.Open`. `WithLogger` is
hardcoded to `tracelog.LogLevelTrace`, the most verbose level pgx has:
every statement and every bind argument, useful while developing,
unsafe as a default in production because it logs plaintext values,
including passwords (see [Chapter 14](14-security.md) for the full
argument). `WithLoggerLevel(logger, level)` is the same tracer with a
level you choose:

```go
db, err := pg.Open(ctx, schema, connString,
    pg.WithLoggerLevel(logger, tracelog.LogLevelWarn),
)
```

`logger` is anything satisfying `tracelog.Logger`
(`Log(ctx, level, msg, data)`), so it adapts to whatever structured
logging library your service already uses. Choose the level for the
volume and sensitivity you are willing to accept: `LogLevelError`
(or `LogLevelWarn`, one step more verbose) for routine production
traffic, `LogLevelDebug` or `LogLevelTrace` for a short-lived,
deliberately narrow debugging window, `LogLevelNone` to disable pgx's
tracer logging entirely.

## Query Tracers and OpenTelemetry

`WithQueryTracer` composes one or more `pgx.QueryTracer`
implementations into the pool, using pgx's own `multitracer` package
underneath. This is how pg supports distributed tracing without ever
importing a tracing library itself: `pg`'s `go.mod` carries no
OpenTelemetry dependency, and adding tracing is entirely something you
opt into from your own module.

```go
import "github.com/exaring/otelpgx"

db, err := pg.Open(ctx, schema, connString,
    pg.WithQueryTracer(otelpgx.NewTracer()),
)
```

[otelpgx](https://github.com/exaring/otelpgx) is one such tracer,
maintained outside this project, that turns every query into an
OpenTelemetry span. `WithQueryTracer` composes rather than replaces:
calling it more than once, or passing more than one tracer in a single
call, wraps every tracer already installed (including one set by an
earlier `ConnectionOption` in the same `Open` call) inside a single
`multitracer.Tracer` that fans calls out to all of them, so a metrics
tracer and a tracing tracer can coexist. Calling `WithQueryTracer` with
zero tracers is a documented no-op: it never clears whatever tracer
was already installed.

## The WithLogger/WithQueryTracer Ordering Caveat

`WithLogger` and `WithLoggerLevel` do not compose the way
`WithQueryTracer` does: both overwrite `poolConfig.ConnConfig.Tracer`
outright, discarding anything already installed there. Combine logging
with tracing by passing `WithLogger`/`WithLoggerLevel` *before*
`WithQueryTracer` in the same `Open` call:

```go
db, err := pg.Open(ctx, schema, connString,
    pg.WithLoggerLevel(logger, tracelog.LogLevelWarn), // first.
    pg.WithQueryTracer(otelpgx.NewTracer()),            // second.
)
```

Written in the reverse order, `WithQueryTracer` installs its tracer
first, and the following `WithLoggerLevel` call silently replaces it,
so the otelpgx spans simply stop appearing with no error telling you
why. `ConnectionOption` values run in the order you list them in
`Open`'s call, so this is a plain ordering rule to hold onto, not a
race condition: get the order right once, in one place, and it stays
right.

## Connection Pool Sizing

Every pool parameter `pgxpool.ParseConfig` recognizes can be set
directly in the connection string passed to `pg.Open`, with no
`ConnectionOption` required:

```go
connString := fmt.Sprintf(
    "host=%s port=%d user=%s password=%s dbname=%s sslmode=%s "+
        "pool_max_conns=%d pool_min_conns=%d "+
        "pool_max_conn_lifetime=%s pool_max_conn_idle_time=%s "+
        "pool_health_check_period=%s",
    host, port, user, password, dbname, sslMode,
    maxConns, minConns, maxConnLifetime, maxConnIdleTime, healthCheckPeriod,
)
```

`pool_max_conns` is the ceiling `PoolStat.MaxConns` reports and the
number your `EmptyAcquireCount`/`AcquiredConns` alerting above is
measured against; there is no universally correct value; it depends on
how many concurrent queries your workload actually issues and how many
connections PostgreSQL itself is configured to accept
(`max_connections`) across every client sharing that server.
`pool_min_conns` keeps that many connections warm even when idle, at
the cost of holding them open against the server's own connection
budget. `pool_max_conn_lifetime` and `pool_max_conn_idle_time` bound
how long a connection can live or sit idle before the pool recycles
it, useful for rotating connections behind a load balancer or a
database that periodically restarts. `pool_health_check_period` sets
how often the pool proactively checks idle connections rather than
discovering a dead one only when a caller tries to use it.

## Exec Modes and Prepared-Statement Caching

Three more `ConnectionOption` values, and their connection-string
equivalents, control how pgx sends queries and how much it caches
about them:

| Option | Connection-string equivalent | Purpose |
| --- | --- | --- |
| `WithDefaultQueryExecMode(mode)` | `default_query_exec_mode` | Selects pgx's query execution protocol, e.g. `pgx.QueryExecModeSimpleProtocol`. |
| `WithStatementCacheCapacity(n)` | `statement_cache_capacity` | Size of the automatic prepared-statement cache; `0` disables it. |
| `WithDescriptionCacheCapacity(n)` | `description_cache_capacity` | Size of the statement-description cache pgx's describe exec mode uses. |

Under pgx's default exec mode, a query pgx has seen before is prepared
once and reused, which saves a parse/plan round trip on every
repeated query shape. That optimization assumes a stable, long-lived
connection: the server remembers the prepared statement for the life
of that specific connection.

## PgBouncer and Transaction Pooling

That assumption breaks under PgBouncer configured for transaction
pooling (the mode most deployments actually use, since it is the one
that lets many application connections share a small number of real
server connections): a prepared statement created on one physical
connection may not exist on the next one your session gets handed
after the transaction ends, because PgBouncer, not pgx, decides which
underlying connection you get next. The fix is to stop relying on
server-side prepared statements at all:

```go
db, err := pg.Open(ctx, schema, connString,
    pg.WithDefaultQueryExecMode(pgx.QueryExecModeSimpleProtocol),
    pg.WithStatementCacheCapacity(0),
    pg.WithDescriptionCacheCapacity(0),
)
```

or, equivalently, entirely as connection-string parameters, with no Go
code involved:

```
postgres://user:pass@pgbouncer-host:6432/mydb?sslmode=verify-full&default_query_exec_mode=simple_protocol&statement_cache_capacity=0&description_cache_capacity=0
```

If your deployment fronts PostgreSQL with PgBouncer (or any pooler
that multiplexes application connections onto a smaller set of server
connections) in transaction-pooling mode,
`WithDefaultQueryExecMode(pgx.QueryExecModeSimpleProtocol)` is not
optional tuning, it is what makes the connection work at all; without
it, queries intermittently fail with errors about a prepared statement
that does not exist on the connection pgx happened to be handed.
`WithStatementCacheCapacity(0)` and `WithDescriptionCacheCapacity(0)`
are not strictly required for that default behavior, since pgx never
populates either cache while the default exec mode stays
`QueryExecModeSimpleProtocol`. Set them anyway: `*DB`'s `Query`/`Exec`/
`QueryRow` pass their `args` straight through to pgx, which recognizes
a `pgx.QueryExecMode` value among those args as a per-call override, so
disabling both capacities makes a call that explicitly opts back into
`QueryExecModeCacheStatement` or `QueryExecModeCacheDescribe` fail
immediately with an explicit error instead of silently caching a
statement handle across a pooled connection swap.

## Vacuum and Table Sizes

[Chapter 13](13-introspection-and-code-generation.md) covers the
introspection API in full; three of its methods are specifically
operational tools worth revisiting here. `DB.IsAutoVacuumEnabled`
reports whether autovacuum is running at all (`SHOW autovacuum;`).
`DB.ListTableSizes` returns every table's on-disk size, largest first,
as `[]TableSizeInfo`, and `DB.GetSize` sums the same figures across
the whole search path into one `SizeInfo`. Wire these into a periodic
job, not just a one-off diagnostic: a table growing faster than
expected, or an autovacuum setting that quietly drifted to `off`, is
exactly the kind of slow-burn problem a dashboard catches long before
it becomes an incident, and both are a handful of lines against an API
you already have open for schema introspection.

## Timeouts

**There is exactly one timeout mechanism.** Every `*DB` method that
talks to PostgreSQL takes a `context.Context` as its first argument,
and pg does not impose a timeout of its own on top of it: the context
you pass is the only deadline in effect. A
`context.WithTimeout` around a single query bounds that query; a
shorter deadline passed into `InTransaction`'s outer `ctx` bounds the
whole transaction, since every statement inside it shares that same
context. Choose deadlines deliberately rather than opening every
handler with a blanket five-second `context.WithTimeout` and hoping it
fits every code path: a bulk `CopyFrom` and a single-row
`SelectByID` have very different legitimate durations, and a timeout
tuned for one starves the other.

A canceled or expired context surfaces as a plain Go error from
whatever call was in flight, `context.DeadlineExceeded` or
`context.Canceled`, wrapped the way pgx wraps it; nothing about pg
changes that shape, so the same `errors.Is` checks you would already
write against `context` apply unchanged.

## Retrying Transient Failures

Some PostgreSQL failures are not really failures of your query, they
are the server telling you a transaction cannot be allowed to
complete as if it ran alone and must be retried from scratch:
SQLSTATE `40001` (`serialization_failure`) and `40P01`
(`deadlock_detected`). `DB.InTransactionRetry` (and its
`Repository[T]` counterpart), introduced in
[Chapter 9](09-transactions.md), automates exactly that retry with
exponential backoff and full jitter:

```go
err := db.InTransactionRetry(ctx, pg.RetryOptions{
    TxOptions: pgx.TxOptions{IsoLevel: pgx.Serializable},
}, func(tx *pg.DB) error {
    // statements that might race another transaction under SERIALIZABLE.
    return nil
})
```

Pair it with `pgx.Serializable` deliberately: under the default read
committed isolation level, `40001` mostly cannot happen in the first
place, so `RetryOptions.TxOptions` is where you opt into the isolation
level that makes the retry logic meaningful. `RetryOptions.MaxAttempts`
(default 3), `BaseDelay` (default 50ms) and `MaxDelay` (default 1s)
bound how hard and how long it tries before giving up and returning the
last attempt's error as-is. Every attempt runs in its own, brand-new
transaction, so `fn` must re-read whatever state it needs from the
database on each call rather than assuming it only runs once; nothing
carries over between attempts except what `fn` itself re-derives.

Operationally, treat a rising count of retried transactions the same
way you would treat rising `EmptyAcquireCount`: it is a leading
indicator, here of contention on a hot row or a serialization
conflict pattern in your workload, worth graphing even though
`InTransactionRetry` is, by design, absorbing the symptom for you
before it becomes a user-visible error.

## Summary

- `DB.Ping` is a minimal liveness check; `DB.Health` adds server
  version and `PoolStat` for a richer readiness endpoint. Both always
  check the pool directly, never an in-flight transaction.
- `PoolStat`'s `EmptyAcquireCount`, `AcquiredConns` near `MaxConns`,
  and `CanceledAcquireCount` are the counters that predict pool
  exhaustion before it becomes an outage.
- `WithLogger` is trace-level and unsafe for production logs;
  `WithLoggerLevel` lets you choose the verbosity, down to
  `LogLevelNone` for a hard guarantee against logging sensitive bind
  arguments.
- `WithQueryTracer` composes `pgx.QueryTracer` implementations (such
  as otelpgx) with no OpenTelemetry dependency in pg itself; pass
  `WithLogger`/`WithLoggerLevel` before it in the same `Open` call, or
  it silently overwrites the tracer slot.
- Pool size, lifetime and health-check parameters are all available
  directly in the connection string (`pool_max_conns`,
  `pool_min_conns`, `pool_max_conn_lifetime`,
  `pool_max_conn_idle_time`, `pool_health_check_period`).
- `WithDefaultQueryExecMode`, `WithStatementCacheCapacity` and
  `WithDescriptionCacheCapacity` (or their connection-string
  equivalents) are required, not optional, behind PgBouncer in
  transaction-pooling mode.
- `DB.IsAutoVacuumEnabled`, `DB.ListTableSizes` and `DB.GetSize` from
  [Chapter 13](13-introspection-and-code-generation.md) double as
  operational monitoring tools.
- Timeouts are entirely `context.Context`-driven; pg adds no timeout
  of its own. `DB.InTransactionRetry` handles the one class of failure
  (`40001`/`40P01`) that is meant to be retried rather than surfaced.

## Further Reading

- [pgxpool: Config](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#Config):
  the full set of pool parameters, including every `pool_*`
  connection-string key referenced in this chapter.
- [pgx: QueryExecMode](https://pkg.go.dev/github.com/jackc/pgx/v5#QueryExecMode):
  the exec modes `WithDefaultQueryExecMode` selects between.
- [otelpgx](https://github.com/exaring/otelpgx): the OpenTelemetry
  tracer used as this chapter's `WithQueryTracer` example.
- [PgBouncer: Documentation](https://www.pgbouncer.org/config.html):
  pooling modes, including transaction pooling and its prepared
  statement caveat.
- [PostgreSQL: Transaction Isolation](https://www.postgresql.org/docs/current/transaction-iso.html):
  the SERIALIZABLE isolation level and the `40001`/`40P01` failure
  modes `InTransactionRetry` targets.
- [Go: Context package](https://pkg.go.dev/context): `WithTimeout`,
  `WithDeadline` and cancellation propagation, the mechanism behind
  every timeout in this chapter.

---

**Next Chapter**: [Testing](16-testing.md)
