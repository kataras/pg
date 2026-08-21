# Chapter 9: Transactions

A transaction groups several statements so that either all of them
take effect or none do. pg wraps pgx's transaction primitives with a
closure-based API, `DB.InTransaction`, that handles begin, commit,
rollback and panic recovery for you, plus a retry variant for the
transient errors PostgreSQL's SERIALIZABLE isolation level produces
under contention. This chapter covers the full contract of
`InTransaction`: what happens when the function you pass it returns an
error, returns `ErrIntentionalRollback`, panics, or succeeds but the
commit itself fails. It also covers a real bug this library once had
in that last case, because the fix teaches something true about how Go
defers and named results interact. Then it covers nesting (joining an
outer transaction versus opening a savepoint), the generic
`DB.InTransactionWrap[R]` method, concurrent access to a single
transaction, and the retry machinery for SQLSTATE 40001 and 40P01.
Every signature below was checked against `tx.go`, `db.go`,
`retry.go`, `concurrent_tx.go`, `db_exec.go` and `repository.go`.

## Table of Contents

- [Background](#background)
- [DB.InTransaction](#dbintransaction)
- [Why Commit Errors Must Propagate](#why-commit-errors-must-propagate)
- [Nesting: Join Versus Savepoint](#nesting-join-versus-savepoint)
- [Manual Control: Begin, Commit, Rollback, IsTransaction](#manual-control-begin-commit-rollback-istransaction)
- [Repository InTransaction](#repository-intransaction)
- [DB.InTransactionWrap and Typed Wrappers](#dbintransactionwrap-and-typed-wrappers)
- [Concurrent Access to One Transaction](#concurrent-access-to-one-transaction)
- [Retrying Transient Failures](#retrying-transient-failures)
- [RetryOptions](#retryoptions)
- [InTransactionRetry](#intransactionretry)
- [Full Jitter Backoff](#full-jitter-backoff)
- [Nested Retry Calls Run Once](#nested-retry-calls-run-once)
- [ExecMany](#execmany)
- [SetConstraintsDeferred](#setconstraintsdeferred)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Background

If you have used SQL transactions before (`BEGIN`, `COMMIT`,
`ROLLBACK`) and know what SERIALIZABLE isolation buys you, skip to
[DB.InTransaction](#dbintransaction). PostgreSQL, like every SQL
database, lets you bracket a sequence of statements between `BEGIN`
and `COMMIT` so they either all become visible to other connections at
once or, on `ROLLBACK`, none of them do. This matters the moment a
unit of work touches more than one row or more than one table: moving
money between two accounts is two `UPDATE` statements, and a crash
between them must not leave the database with money that appeared or
vanished. Isolation levels control what a transaction is allowed to
see of other, concurrently running transactions. PostgreSQL's default,
READ COMMITTED, lets a transaction see rows committed by others since
it began, which is usually fine but permits a class of anomaly called
write skew: two transactions each read a value, each decide it is
still safe to act on, and both commit, producing a result neither
would have permitted alone. SERIALIZABLE isolation closes this gap by
guaranteeing the outcome is equivalent to some serial (one-at-a-time)
ordering of the concurrent transactions, and it does so by aborting
one of the conflicting transactions rather than blocking, which is why
retry logic (covered later in this chapter) exists at all.

## DB.InTransaction

`DB.InTransaction(ctx context.Context, fn func(*DB) error) error` runs
`fn` inside a transaction and decides what to do based on what `fn`
returns:

```go
const debit = `UPDATE accounts SET balance = balance - $1 WHERE id = $2`
const credit = `UPDATE accounts SET balance = balance + $1 WHERE id = $2`

err := db.InTransaction(ctx, func(tx *pg.DB) error {
    if _, err := tx.Exec(ctx, debit, amount, from); err != nil {
        return err
    }

    _, err := tx.Exec(ctx, credit, amount, to)
    return err
})
```

The contract, exactly as implemented:

- `fn` returns `nil`: the transaction commits, and **any error the
  commit itself returns is passed back to the caller**. A successful
  `fn` does not guarantee a successful `InTransaction`.
- `fn` returns `pg.ErrIntentionalRollback`: the transaction rolls
  back, and `InTransaction` returns `nil` on a successful rollback, or
  the rollback error otherwise. This is the documented way to abort a
  transaction deliberately without treating it as a failure, useful
  for a dry-run code path that wants every side effect of `fn` undone.
- `fn` returns any other non-nil error: the transaction rolls back and
  that error is returned. If the rollback itself also fails, both
  errors are combined with `errors.Join`, so `errors.Is` and
  `errors.As` still reach either one.
- `fn` panics: the transaction rolls back and the panic is re-thrown
  after the rollback completes. `InTransaction` does not swallow
  panics; it only makes sure the connection is not left in an
  in-transaction state before the panic continues unwinding.

The context argument governs `Begin`, `Commit` and `Rollback`
directly, so a context that is already canceled or expired when
`InTransaction` is called returns promptly (from the failing `Begin`)
without ever invoking `fn`.

## Why Commit Errors Must Propagate

The first bullet above, that a successful `fn` can still fail overall,
used to be broken in this library, and the fix is worth understanding
because it is a general Go lesson, not a pg-specific quirk. Here is
the shape of the bug, reconstructed:

```go
// The buggy version (illustrative, not the real signature).
func (db *DB) InTransaction(ctx context.Context, fn func(*DB) error) error {
    tx, err := db.Begin(ctx)
    if err != nil {
        return err
    }

    defer func() {
        if err != nil {
            _ = tx.Rollback(ctx)
        } else {
            err = tx.Commit(ctx) // writes to the WRONG "err"
        }
    }()

    err = fn(tx)
    return err // the return VALUE was already copied here
}
```

`return err` copies the current value of `err` into the function's
unnamed result at the moment it executes. The deferred closure then
runs, and it can still assign to the variable named `err` (the same
one `fn`'s result was stored in), but that assignment happens after
the copy was already made. If `fn` succeeded, `err` was `nil` at
`return err` time, the (already-decided) return value became `nil`,
and the later `err = tx.Commit(ctx)` inside the deferred closure
changed a variable nobody was going to read again. A commit failure,
including a serialization failure detected only at commit time (see
[Retrying Transient Failures](#retrying-transient-failures)), looked
identical to success from the caller's point of view: the row changes
were never actually persisted, and nothing said so.

The fix is the named result in the real signature:

```go
func (db *DB) InTransaction(
    ctx context.Context, fn func(*DB) error,
) (err error) {
```

With a named result, `err` inside the function body and `err` in the
deferred closure are the identical variable that Go returns to the
caller: there is no intermediate copy for `return err` to freeze. The
deferred closure's `err = tx.Commit(ctx)` now writes directly to the
value the caller receives. This is precisely why Go lets a `defer`
mutate a named result in the first place: it is the sanctioned pattern
for exactly this kind of "clean up, and possibly override the outcome
based on what cleanup found" logic. Any function that commits,
releases a resource, or otherwise needs its deferred cleanup to be
able to fail the call should use a named result for the same reason.

## Nesting: Join Versus Savepoint

Two different pg operations both look like "start another
transaction," and they behave differently on purpose. Getting this
distinction backwards either silently merges work you meant to keep
separate, or fails to give you the independent rollback you needed.

**Calling `InTransaction` again on a `*DB` that is already inside a
transaction joins the existing transaction.** No `SAVEPOINT` is taken,
no new transaction begins. `fn` runs directly against the same `*DB`,
and any error it returns (including `ErrIntentionalRollback`)
propagates out to whichever `InTransaction` call is actually managing
the transaction (opened the real `BEGIN`), rather than being contained
to the inner call:

```go
err := db.InTransaction(ctx, func(tx *pg.DB) error {
    // tx.IsTransaction() is already true here.
    return tx.InTransaction(ctx, func(tx2 *pg.DB) error {
        // tx2 IS tx. No savepoint. An error here rolls back
        // everything the outer call did too.
        return doSomething(ctx, tx2)
    })
})
```

The check that makes this happen is the first line of `InTransaction`:

```go
func (db *DB) InTransaction(
    ctx context.Context, fn func(*DB) error,
) (err error) {
    if db.IsTransaction() {
        return fn(db)
    }
    // ... Begin / defer commit-or-rollback / fn(tx) ...
}
```

**Calling `DB.Begin` on a `*DB` that is already inside a transaction
opens a savepoint-backed subtransaction instead.** `Begin` detects the
same condition (`db.tx != nil`) but takes the opposite action: it
calls `db.tx.Begin(ctx)`, which is pgx's own nested-transaction
support, implemented as `SAVEPOINT sp_N`. The returned `*DB` has its
own `Commit` (`RELEASE SAVEPOINT sp_N`) and `Rollback` (`ROLLBACK TO
SAVEPOINT sp_N`), independent of the outer transaction:

```go
tx, err := db.Begin(ctx)
if err != nil {
    return err
}
defer tx.Rollback(ctx) // no-op after a successful Commit below.

sub, err := tx.Begin(ctx) // SAVEPOINT sp_1
if err != nil {
    return err
}

if err := riskyStep(ctx, sub); err != nil {
    sub.Rollback(ctx) // ROLLBACK TO SAVEPOINT sp_1; tx is unaffected.
} else {
    sub.Commit(ctx) // RELEASE SAVEPOINT sp_1
}

return tx.Commit(ctx)
```

Reach for `Begin` (not `InTransaction`) when you specifically need an
inner unit of work that can fail and roll back on its own without
undoing everything the outer transaction already did, for example
attempting several independent, best-effort side effects inside one
larger transaction. `BeginConcurrent` (below) follows the identical
join-versus-savepoint rule.

## Manual Control: Begin, Commit, Rollback, IsTransaction

`InTransaction` is a closure over the pattern of Begin, run, then
Commit-or-Rollback. When you need to hold a transaction open across
multiple function calls instead of one closure, use the pieces
directly:

- `DB.Begin(ctx context.Context) (*DB, error)`: starts a transaction
  (or, per the previous section, a savepoint if `db` is already
  transactional) and returns a new `*DB` bound to it. The default
  `pgx.TxOptions{}` is used, meaning PostgreSQL's default isolation
  level, read committed.
- `DB.Commit(ctx context.Context) error`: commits. Calling it a second
  time (or calling it on a `*DB` whose transaction already closed
  through a prior `Commit` or `Rollback`) returns `nil` without
  issuing SQL, rather than an error, because the transaction is
  already closed. Calling it on a `*DB` with no transaction at all
  returns an error: `commit outside of a transaction`.
- `DB.Rollback(ctx context.Context) error`: the same idempotent
  behavior, mirrored for rollback.
- `DB.IsTransaction() bool`: reports whether this `*DB` is bound to a
  transaction (`db.tx != nil`). Both `InTransaction` and `Begin`
  consult it to decide whether to join, start fresh, or open a
  savepoint.

The `defer tx.Rollback(ctx)` idiom used in the savepoint example above
works identically at the top level: it is safe to defer immediately
after a successful `Begin` precisely because `Rollback` is a no-op
once the transaction is already closed, so a later, successful
`Commit` leaves the deferred `Rollback` nothing to do.

## Repository InTransaction

`Repository[T].InTransaction(ctx, fn func(*Repository[T]) error)
error` mirrors `DB.InTransaction` at the typed level: it commits or rolls
back based on `fn`'s return value using the exact same rules, and it
applies the exact same nesting short-circuit, checked against the
repository's own `*DB` before delegating:

```go
func (repo *Repository[T]) InTransaction(
    ctx context.Context, fn func(*Repository[T]) error,
) error {
    if repo.db.IsTransaction() {
        return fn(repo)
    }

    return repo.db.InTransaction(ctx, func(db *DB) error {
        txRepo := &Repository[T]{db: db, td: repo.td}
        return fn(txRepo)
    })
}
```

`Repository[T].InsertMany` and `UpsertMany` build on this same method:
each batches its rows into multi-row statements internally and calls
`InTransaction` so a later batch's failure rolls back every earlier
batch from the same call.

## DB.InTransactionWrap and Typed Wrappers

A real application usually has more than one `Repository[T]`, and code
often groups several of them into a small struct so a service layer
can depend on one thing instead of five:

```go
type Repositories struct {
    Customers *pg.Repository[Customer]
    Orders    *pg.Repository[Order]
}

func NewRepositories(db *pg.DB) *Repositories {
    return &Repositories{
        Customers: pg.NewRepository[Customer](db),
        Orders:    pg.NewRepository[Order](db),
    }
}
```

Without help, giving `*Repositories` its own transactional method
means hand-writing the same three lines every such wrapper needs:
open a transaction on the underlying `*DB`, reconstruct the wrapper
type around that transactional `*DB`, and call the caller's function
with it. `DB.InTransactionWrap[R]` is exactly that boilerplate,
generalized once:

```go
func (db *DB) InTransactionWrap[R any](
    ctx context.Context, wrap func(*DB) R, fn func(R) error,
) error {
    return db.InTransaction(ctx, func(tx *DB) error { return fn(wrap(tx)) })
}
```

**The two `InTransaction` names on `*DB` are different things.**
`db.InTransaction(ctx, fn func(*DB) error)` is the plain closure form
covered earlier in this chapter. `db.InTransactionWrap(ctx, wrap, fn)`
is this one: it takes a constructor and calls `fn` with the
constructed `R`, never with the `*DB`. The second name exists because
a method name must be unique per receiver type, so the generic form
could not also be called `InTransaction`; in earlier releases of pg it
was the package-level function `pg.InTransaction[R](ctx, db, wrap,
fn)`, and moving it onto `*DB` (Go 1.27 allows a method to declare its
own type parameters) forced the rename.

Using it, `Repositories` needs one line instead of a hand-written
method:

```go
func (r *Repositories) InTransaction(
    ctx context.Context, fn func(*Repositories) error,
) error {
    return r.Customers.DB().InTransactionWrap(ctx, NewRepositories, fn)
}

// Usage:
err := repos.InTransaction(ctx, func(tx *Repositories) error {
    if err := tx.Customers.Insert(ctx, customer); err != nil {
        return err
    }

    return tx.Orders.Insert(ctx, order)
})
```

`wrap` is typically the wrapper's own constructor, exactly as
`NewRepositories` is above: `InTransactionWrap` calls `db.InTransaction`
once, calls `wrap(tx)` to rebuild `*Repositories` (or whatever `R` is)
around the now-transactional `*DB`, then calls `fn` with that rebuilt
value. If `db` is already inside a transaction, the nesting rule from
[Nesting: Join Versus Savepoint](#nesting-join-versus-savepoint)
still applies through the inner `db.InTransaction` call: `fn` joins
the existing transaction, and `wrap` still runs so `fn` receives a
wrapper built from that same, already-transactional `*DB`.

## Concurrent Access to One Transaction

A single PostgreSQL connection processes one statement at a time. A
`pgx.Tx` is not safe to call from multiple goroutines concurrently,
because each call sends bytes on the same connection and reads back a
response before the next call may begin. `BeginConcurrent` and
`ConcurrentTx` exist for the case where several goroutines need to
issue statements against the same transaction without each hand-rolling
a mutex:

```go
tx, err := db.BeginConcurrent(ctx)
if err != nil {
    return err
}
defer tx.Rollback(ctx)

var wg sync.WaitGroup
for _, id := range ids {
    wg.Add(1)
    go func(id int) {
        defer wg.Done()
        tx.Exec(ctx, "UPDATE items SET touched = true WHERE id = $1", id)
    }(id)
}
wg.Wait()

return tx.Commit(ctx)
```

`BeginConcurrent` follows the same join/fresh rule as `Begin` (a
nested call on an already-transactional `*DB` opens a savepoint via
`db.tx.Begin`), but when it starts a brand new transaction, it does so
through `NewConcurrentTx`, which wraps the returned `pgx.Tx` in a
`*ConcurrentTx`: a `sync.Mutex`-guarded decorator implementing every
`pgx.Tx` method by locking, delegating to the embedded `pgx.Tx`, and
unlocking. This makes concurrent use **safe, not parallel**: goroutines
calling methods on the same `*ConcurrentTx` never corrupt the
connection or race each other, but they still execute one at a time,
serialized by the mutex, exactly as a single PostgreSQL connection
requires. It solves the "conn busy" panic pgx raises when two
goroutines write to the same connection at once; it does not make your
queries run any faster than a single connection can. Its `LargeObjects()`
and `Conn()` methods are the one exception: each returns a value that
is itself unsynchronized, so a `pgx.LargeObjects` or `*pgx.Conn`
obtained through a `*ConcurrentTx` needs its own locking if shared
further.

## Retrying Transient Failures

Under SERIALIZABLE isolation, PostgreSQL sometimes cannot allow a
transaction to commit even though every individual statement in it
succeeded, because doing so would produce a result inconsistent with
any one-at-a-time ordering of the transactions that ran concurrently
with it. When PostgreSQL detects this, it reports SQLSTATE `40001`
(`serialization_failure`), and the correct response is not to inspect
the error, but to retry the whole transaction from scratch. A related
code, `40P01` (`deadlock_detected`), is reported when PostgreSQL picks
one of two mutually blocked transactions as the victim to break a
deadlock; it too is safe to retry.

`IsErrRetryableTx(err error) bool` recognizes both:

```go
func IsErrRetryableTx(err error) bool {
    pgErr, ok := asPgError(err)
    if !ok {
        return false
    }

    switch pgErr.Code {
    case "40001", "40P01":
        return true
    default:
        return false
    }
}
```

It checks the driver's structured `*pgconn.PgError.Code` (via
`errors.As`, so it still matches an error wrapped several layers
deep, including one that surfaced from `Commit` rather than from one
of the transaction's own statements), never English message text.
This is the same SQLSTATE-first approach [Chapter 10](10-errors.md)
covers for every other error classifier in the package. No other
member of PostgreSQL's `40` (`transaction_rollback`) class is treated
as retryable; retrying `40000` or `40003` blindly is not generally
safe, so a caller who wants to retry on more conditions supplies their
own classifier that also calls `IsErrRetryableTx`, rather than this
function growing an ever-expanding list.

## RetryOptions

`RetryOptions` configures `InTransactionRetry`. Every field's zero
value falls back to a documented default:

| Field | Type | Zero-value default |
| --- | --- | --- |
| `MaxAttempts` | `int` | 3. A caller-supplied negative value (not zero) is treated as 1, an explicit "no retries," rather than silently upgraded to 3. |
| `BaseDelay` | `time.Duration` | 50ms. The upper bound for the first retry's jittered wait. |
| `MaxDelay` | `time.Duration` | 1s. The ceiling the doubling backoff bound saturates at. |
| `TxOptions` | `pgx.TxOptions` | zero value (read committed). Reused unchanged on every attempt. |
| `IsRetryable` | `func(error) bool` | `IsErrRetryableTx`. A non-nil value fully replaces the default; it is not combined with it. |

The `TxOptions` field deserves a specific callout: PostgreSQL's
default isolation level is read committed, under which SQLSTATE
`40001` mostly cannot occur in the first place, since read committed
does not perform the snapshot-based conflict detection SERIALIZABLE
does. If the whole point of calling `InTransactionRetry` is to retry
around serialization failures, set `TxOptions: pgx.TxOptions{IsoLevel:
pgx.Serializable}`, or the classifier will rarely have anything to
classify:

```go
opts := pg.RetryOptions{
    MaxAttempts: 5,
    TxOptions:   pgx.TxOptions{IsoLevel: pgx.Serializable},
}
```

## InTransactionRetry

`DB.InTransactionRetry(ctx, opts RetryOptions, fn func(*DB) error)
error` and its typed counterpart
`Repository[T].InTransactionRetry(ctx, opts RetryOptions, fn
func(*Repository[T]) error) error` run `fn` in a fresh transaction,
and if it fails with an error
`opts.IsRetryable` (or `IsErrRetryableTx` by default) accepts, retry
the whole thing, waiting out a backoff between attempts:

```go
err := db.InTransactionRetry(ctx, pg.RetryOptions{
    TxOptions: pgx.TxOptions{IsoLevel: pgx.Serializable},
}, func(tx *pg.DB) error {
    // Re-read whatever this needs on every attempt: nothing from a
    // failed attempt carries over except what fn re-derives here.
    return transferFunds(ctx, tx, from, to, amount)
})
```

Every attempt opens a brand new transaction and runs `fn` from
scratch, internally reusing the exact commit/rollback/panic machinery
`InTransaction` uses (including the same named-return trick from
[Why Commit Errors Must Propagate](#why-commit-errors-must-propagate),
reimplemented in `runInTransactionOnce` specifically because
`InTransactionRetry` needs `RetryOptions.TxOptions` to reach `BeginTx`
on each attempt, which the plain `Begin`/`InTransaction` path has no
way to accept). Nothing a failed attempt wrote to variables `fn`
closes over survives into the next attempt except what `fn` itself
re-reads from the database, so `fn` must not assume it only runs once.
`fn` returning `ErrIntentionalRollback` is never retried, the same as
for `InTransaction`: the transaction rolls back and the call returns
`nil` (or the rollback error) immediately.

If the context is canceled or expires while waiting out a backoff
between attempts, `InTransactionRetry` returns promptly with
`errors.Join(ctx.Err(), lastAttemptErr)` instead of continuing to
retry against a context nobody wants to wait on anymore.

## Full Jitter Backoff

Between a failed, retryable attempt and the next one,
`InTransactionRetry` waits a randomized duration following the "full
jitter" algorithm from AWS's exponential backoff article: the bound
doubles with each failed attempt, capped at `MaxDelay`, and the actual
wait is chosen uniformly at random between zero and that bound.

```
bound = min(MaxDelay, BaseDelay << (attempt-1))
delay = random value in [0, bound)
```

With the defaults (`BaseDelay` 50ms, `MaxDelay` 1s), the bound for the
wait before the second attempt is 50ms, before the third is 100ms, and
so on, doubling until it saturates at 1s. Choosing uniformly at random
within the bound, rather than always waiting the full bound, is the
"jitter" part: it keeps several clients that failed at the same
instant (a common case, since a serialization failure often means
they were genuinely contending for the same rows) from retrying in
lockstep and immediately colliding again. The left shift that computes
the doubling bound is guarded against overflowing `time.Duration`'s
underlying `int64`, saturating at `math.MaxInt64` nanoseconds rather
than wrapping around to a small or negative duration after enough
attempts.

## Nested Retry Calls Run Once

`InTransactionRetry` applies the same `if db.IsTransaction() { return
fn(db) }` nesting short-circuit `InTransaction` uses, but for a
stronger reason than mere convenience: if `db.IsTransaction()` is
already true, `InTransactionRetry` runs `fn` exactly once, with no
retry, full stop. A transaction that is already a subtransaction of
some outer
transaction cannot be meaningfully retried in isolation: rolling it
back and reopening it (there is no "sub-BEGIN" a nested call could
redo) would still leave the outer transaction, and everything it
already did, exactly where it was. Retrying only makes sense for a
transaction that owns its own `BEGIN`/`COMMIT`, so nesting
`InTransactionRetry` calls is not an optimization that happens to skip
redundant retries: it would be wrong to retry here, and the
short-circuit exists to make that impossible rather than merely
unlikely. To get real retries, call `InTransactionRetry` on a `*DB`
that is not already inside a transaction.

`Repository[T].InTransactionRetry` does not gate on
`repo.IsReadOnly()` the way the write-capable `Insert`/`Update`/`Delete`
family does, because `fn` may legitimately be read-only: SQLSTATE
`40001` is raised for read-write conflicts under SERIALIZABLE
isolation, not exclusively for writes, so a read-only, repeatable-read-
sensitive query is just as valid a candidate for retry.

## ExecMany

`DB.ExecMany(ctx context.Context, queries ...string) error` runs each
statement in `queries`, in order, inside a single transaction (joining
the current one if `db` is already transactional, per the usual
`InTransaction` nesting rule):

```go
err := db.ExecMany(ctx,
    `ALTER TABLE orders ADD COLUMN priority int DEFAULT 0`,
    `UPDATE orders SET priority = 1 WHERE rush = true`,
)
```

Statements are sent one `Exec` call at a time, not concatenated into
one string, because pgx's extended query protocol cannot prepare a
string containing multiple statements. If any statement fails, the
whole transaction rolls back (or, if `ExecMany` joined an
already-open transaction, that transaction is left aborted for
whoever started it to roll back, the same as any other error surfaced
from a function passed to `InTransaction`), and none of the earlier
statements in the same call persist. Given zero queries, `ExecMany`
does nothing and returns `nil` without opening a transaction at all.

## SetConstraintsDeferred

`DB.SetConstraintsDeferred(ctx context.Context, constraints ...string) error`
issues PostgreSQL's `SET CONSTRAINTS ... DEFERRED`, which postpones
checking the named deferrable constraints (or every deferrable
constraint, with no arguments) until the transaction commits instead
of checking them after each statement. This matters when a batch of
statements is only individually valid at the end: for example,
swapping two rows' unique keys requires, for one moment mid-batch,
that both rows hold the other's key, which a normal per-statement
unique check would reject.

```go
const swapCode = `UPDATE items SET code = $1 WHERE id = $2`

err := db.InTransaction(ctx, func(tx *pg.DB) error {
    if err := tx.SetConstraintsDeferred(ctx); err != nil {
        return err
    }

    // Both updates would violate the unique constraint if checked
    // individually; deferring lets PostgreSQL check it once, at
    // COMMIT, after both have run.
    if _, err := tx.Exec(ctx, swapCode, codeB, idA); err != nil {
        return err
    }

    _, err := tx.Exec(ctx, swapCode, codeA, idB)
    return err
})
```

`SetConstraintsDeferred` checks `DB.IsTransaction()` itself and
returns a descriptive error, `set constraints deferred: not inside a
transaction`, before issuing any SQL if `db` is not transactional,
since PostgreSQL would otherwise reject the statement anyway (there is
no "remainder of the current transaction" outside one) with a less
specific error. Naming a constraint that exists but is not itself
declared `DEFERRABLE`, or one that does not exist at all, is still
rejected by PostgreSQL: `SetConstraintsDeferred` does not cross-check
constraint names against the registered `Schema`, since constraints
are not modeled there the way tables and columns are. Every
constraint name passed is quoted with `QuoteIdentifier` before being
embedded in the generated statement.

## Summary

- `DB.InTransaction` commits on `nil`, rolls back and returns `nil`
  on `ErrIntentionalRollback`, rolls back and returns the error
  otherwise (joined with any rollback error), and rolls back and
  re-panics on a panic. A commit failure is returned to the caller
  even when `fn` succeeded.
- The commit-error propagation depends on `InTransaction`'s named
  `(err error)` result: a deferred closure can only override what the
  caller receives when it is writing to the same variable the
  function returns, not a copy already frozen by an unnamed `return
  err`.
- Calling `InTransaction` again on an already-transactional `*DB`
  joins the existing transaction (no savepoint); calling `Begin` on
  one opens a real, independently committable/rollback-able
  savepoint-backed subtransaction via pgx's nested `Tx.Begin`.
- `Repository[T].InTransaction` mirrors `DB.InTransaction` at the
  typed level; `DB.InTransactionWrap[R]` generalizes the "open a
  transaction, rebuild my typed wrapper around it" pattern so a
  multi-repository aggregate needs one line instead of a hand-written
  method. It is named `InTransactionWrap`, not `InTransaction`,
  because `DB.InTransaction` already exists with a different
  signature.
- `BeginConcurrent`/`ConcurrentTx` make a transaction safe to touch
  from several goroutines by serializing every call through a mutex.
  Safe, not parallel: PostgreSQL connections process one statement at
  a time regardless.
- `IsErrRetryableTx` recognizes SQLSTATE `40001`
  (`serialization_failure`) and `40P01` (`deadlock_detected`).
  `InTransactionRetry` retries a fresh transaction per attempt with
  full-jitter exponential backoff (`RetryOptions`), because a
  serialization failure is frequently only detectable when the
  transaction tries to commit. A nested `InTransactionRetry` call
  runs `fn` exactly once: retrying a subtransaction in isolation
  cannot undo what its outer transaction already did.
- `ExecMany` runs several statements in one transaction;
  `SetConstraintsDeferred` postpones deferrable constraint checks to
  commit time, for batches that are only valid once every statement
  in them has run.

## Further Reading

- [PostgreSQL: Transaction Isolation](https://www.postgresql.org/docs/current/transaction-iso.html):
  read committed, repeatable read and serializable, and the anomalies
  each one permits or forbids.
- [PostgreSQL: Explicit Locking, Advisory Locks](https://www.postgresql.org/docs/current/explicit-locking.html#ADVISORY-LOCKS):
  background for the advisory lock `DB.Migrate` takes, covered in
  [Chapter 11](11-schema-management-and-migrations.md).
- [PostgreSQL: SAVEPOINT](https://www.postgresql.org/docs/current/sql-savepoint.html):
  the statement pgx issues under the hood for a nested `Begin`.
- [PostgreSQL: SET CONSTRAINTS](https://www.postgresql.org/docs/current/sql-set-constraints.html):
  the statement `SetConstraintsDeferred` builds.
- [PostgreSQL Error Codes: Class 40](https://www.postgresql.org/docs/current/errcodes-appendix.html#ERRCODES-TABLE):
  the full `transaction_rollback` class `IsErrRetryableTx` picks two
  members out of.
- [AWS Architecture Blog: Exponential Backoff and Jitter](https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/):
  the "full jitter" algorithm `backoffDelay` implements.
- [Go: Defer, Panic and Recover](https://go.dev/blog/defer-panic-and-recover):
  the mechanics behind the named-result fix in this chapter.

---

**Next Chapter**: [Errors](10-errors.md)
