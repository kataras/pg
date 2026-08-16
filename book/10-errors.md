# Chapter 10: Errors

PostgreSQL does not report failures as English sentences you are meant
to parse. Every error the server sends back carries a five-character
SQLSTATE code, defined by the SQL standard and extended by PostgreSQL,
and that code is the one thing about an error PostgreSQL guarantees
will not change. The message text attached to it can, and does: it
changes with `lc_messages`, with the server's language settings, and
occasionally between PostgreSQL versions, so code that matches on a
substring of the English message is one `LC_MESSAGES=de_DE` deployment
away from silently breaking. This chapter covers how pg classifies
PostgreSQL errors: `ErrNoRows`, the constraint-violation classifiers
(`IsErrDuplicate`, `IsErrForeignKey`, `IsErrInputSyntax`,
`IsErrColumnNotExists`), the typed `ConstraintError`/`AsConstraintError`
pair, and the package's other exported sentinel errors. Every function
signature below is read from `errors.go`, `db.go`, `listener.go` and
`repository.go`.

## Table of Contents

- [Background](#background)
- [ErrNoRows](#errnorows)
- [The Classifiers](#the-classifiers)
- [ConstraintKind](#constraintkind)
- [ConstraintError](#constrainterror)
- [AsConstraintError Is Extraction-Only](#asconstrainterror-is-extraction-only)
- [Mapping Errors to an HTTP Response](#mapping-errors-to-an-http-response)
- [Other Exported Sentinels](#other-exported-sentinels)
- [SQLSTATE Lookup Table](#sqlstate-lookup-table)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Background

If you already know what SQLSTATE is and why matching on error message
text is fragile, skip to [ErrNoRows](#errnorows). A SQLSTATE is a
five-character code (letters and digits, e.g. `23505`) that PostgreSQL
attaches to every error and notice it produces. The first two
characters name a broad class (`23` is integrity constraint violation,
`40` is transaction rollback, `42` is syntax error or access rule
violation), and the full five characters name a specific condition
within that class. Client drivers, including pgx, expose this code on
the structured error value they return (`*pgconn.PgError.Code` in
pgx's case) alongside a human-readable `Message` meant for logs and
terminals, not for `if strings.Contains(err.Error(), "...")` checks.
The code is part of PostgreSQL's wire protocol contract and is stable
across locales, versions and even other PostgreSQL-compatible
databases that implement the same protocol; the message text is not
part of that contract at all. Every classifier in this chapter checks
the code first, and its fallback to message-text checks exists purely
for errors that never made it through the driver's typed error value
in the first place.

## ErrNoRows

```go
var ErrNoRows = pgx.ErrNoRows
```

`ErrNoRows` is a direct alias for pgx's own sentinel, returned by
`QueryRow(...).Scan(...)` and by every `Repository[T]`/`DB` method
that expects exactly one row and found none. `IsErrNoRows(err error)
bool` wraps the standard-library check:

```go
func IsErrNoRows(err error) bool {
    return errors.Is(err, ErrNoRows)
}
```

Because it is `errors.Is`, `IsErrNoRows` still recognizes `ErrNoRows`
under any number of layers of `fmt.Errorf("...: %w", err)` wrapping, so
a repository method that adds context to the error before returning it
does not break callers checking for "no rows" further up the stack.
`DB.Count` swallows `ErrNoRows` itself and returns `(0, nil)` instead,
since a `COUNT` wrapped in a `GROUP BY` that matches nothing is a
legitimate zero, not a failure a caller should have to special-case.

## The Classifiers

Four package-level functions turn a PostgreSQL error into a specific,
actionable answer instead of an opaque `error`. All four follow the
same shape: check whether `err` wraps a `*pgconn.PgError` (via the
unexported `asPgError` helper, itself `errors.As` under the hood, so
it matches an error wrapped several layers deep); if so, classify by
SQLSTATE `Code`; if not, fall back to the original substring checks
against `err.Error()`, kept only for compatibility with errors that
never carried a structured `*pgconn.PgError` in the first place
(already-formatted strings, or an older driver).

`IsErrDuplicate(err error) (string, bool)` reports whether an insert
or update failed a unique constraint, returning the constraint's name:

```go
if name, ok := pg.IsErrDuplicate(err); ok {
    log.Printf("unique constraint %q violated", name)
}
```

SQLSTATE path: delegates to `AsConstraintError` and reports true only
when the resulting `Kind` is `ConstraintUnique` (`23505`), returning
its `ConstraintName` field directly.

`IsErrForeignKey(err error) (string, bool)` reports whether an insert
or update referenced a row that does not exist, or deleted/updated a
row still referenced elsewhere, returning the constraint's name.
SQLSTATE path: `AsConstraintError` again, checking for
`ConstraintForeignKey` (`23503`).

`IsErrInputSyntax(err error) (string, bool)` reports whether a value
could not be parsed as its column's PostgreSQL type, returning the
offending value (extracted from the quoted substring in the server's
message) or the generic string `"invalid input syntax"` when no quoted
value is present. SQLSTATE path: `22P02`
(`invalid_text_representation`) is the primary signal, but PostgreSQL
reports a malformed `tsquery` under the much more generic `42601`
(`syntax_error`), which does not distinguish a tsquery problem from
any other syntax error by code alone; for that case,
`IsErrInputSyntax` also checks the structured `Message` field for
`"syntax error in tsquery"` or `"no operand in tsquery"` even when a
`*pgconn.PgError` is available, since the code alone is not specific
enough here.

`IsErrColumnNotExists(err error, col string) bool` reports whether a
query referenced a column, named `col`, that does not exist. SQLSTATE
path: `42703` (`undefined_column`), cross-checked against the
structured `Message` containing `column "<col>" does not exist` so
the function only matches the specific column asked about, not any
undefined-column error.

```go
if pg.IsErrColumnNotExists(err, "email") {
    // the query referenced a column that was renamed or dropped.
}
```

## ConstraintKind

`ConstraintKind` is a `string` type classifying which family of
integrity-constraint violation (SQLSTATE class `23`) occurred:

| Constant | SQLSTATE | Meaning |
| --- | --- | --- |
| `ConstraintUnique` | `23505` (`unique_violation`) | a row would duplicate an existing value in a unique index or constraint. |
| `ConstraintForeignKey` | `23503` (`foreign_key_violation`) | a row references a value absent from the referenced table, or a referenced row was deleted/updated while still referenced. |
| `ConstraintNotNull` | `23502` (`not_null_violation`) | a `NOT NULL` column was given a null value. |
| `ConstraintCheck` | `23514` (`check_violation`) | a row failed a `CHECK` constraint. |
| `ConstraintExclusion` | `23P01` (`exclusion_violation`) | a row conflicted with an existing row under an `EXCLUDE` constraint. |

`Kind` is left empty (`""`) for a class-`23` SQLSTATE that is not one
of these five, such as the generic `23000`
(`integrity_constraint_violation`); `AsConstraintError` still reports
`ok=true` for it, since it is still, unambiguously, an integrity
violation, just not one of the five PostgreSQL commonly names more
specifically.

## ConstraintError

`ConstraintError` is a typed view over a PostgreSQL class-`23` error,
built from the driver's `*pgconn.PgError` so a caller stops parsing
`DETAIL` text by hand:

```go
type ConstraintError struct {
    Kind           ConstraintKind
    ConstraintName string
    TableName      string
    ColumnName     string
    Detail         string
    Code           string
    // unexported cause *pgconn.PgError
}
```

- `Kind`: one of the five `ConstraintKind` constants, or empty.
- `ConstraintName`: the violated constraint or index name, e.g.
  `"customers_email_key"`. May be empty if PostgreSQL did not report
  one.
- `TableName`: the table the violation occurred on. May be empty.
- `ColumnName`: the offending column. PostgreSQL only populates this
  for not-null violations (`ConstraintNotNull`); it is empty for
  every other kind.
- `Detail`: the server's `DETAIL` line, e.g. `Key (email)=(x@y.com)
  already exists.` for a unique violation. May be empty.
- `Code`: the raw SQLSTATE, e.g. `"23505"`.

`(*ConstraintError).Error() string` renders a compact one-line
summary, e.g. `unique constraint "customers_email_key" on table
"customers": Key (email)=(x@y.com) already exists.`, and
`(*ConstraintError).Unwrap() error` returns the underlying
`*pgconn.PgError`, so `errors.As(err, &pgErr)` still reaches it through
a returned `*ConstraintError`.

## AsConstraintError Is Extraction-Only

```go
func AsConstraintError(err error) (*ConstraintError, bool)
```

`AsConstraintError` reports whether `err` (or anything it wraps, per
`errors.As`) is a PostgreSQL integrity-constraint violation, a
`*pgconn.PgError` whose `Code` begins with `"23"`, and if so returns
its typed form.

The important design decision is what `AsConstraintError` is *not*:
pg never wraps the errors it returns from `Insert`, `Update`, `Delete`
and friends in a `ConstraintError` itself. A failed `Insert` still
returns the plain, driver-level error, exactly as pgx produced it.
`AsConstraintError` is purely an extraction function you call
yourself, at whichever layer in your application actually needs to
turn a database failure into a decision: an HTTP handler choosing a
status code, a GraphQL resolver choosing an error type, a background
job deciding whether to retry. This keeps the typed view out of the
data path entirely: a repository method's returned `error` is always
just `error`, checkable with the plain classifiers above or unwrapped
manually, and only the layer that actually maps errors to responses
pays for constructing a `ConstraintError`.

```go
err := customers.Insert(ctx, newCustomer)
if err != nil {
    if constraintErr, ok := pg.AsConstraintError(err); ok {
        // constraintErr.Kind, .ConstraintName, .TableName, .Detail
        // are all populated here; err itself is untouched.
    }
}
```

## Mapping Errors to an HTTP Response

A handler layer typically wants one switch that turns any error a
repository call might return into the right HTTP status and a message
safe to show a client. `AsConstraintError` plus the classifiers above
are enough to build it without ever inspecting `err.Error()` text:

```go
func writeError(w http.ResponseWriter, err error) {
    switch {
    case pg.IsErrNoRows(err):
        http.Error(w, "not found", http.StatusNotFound)
    case func() bool {
        ce, ok := pg.AsConstraintError(err)
        return ok && ce.Kind == pg.ConstraintUnique
    }():
        http.Error(w, "already exists", http.StatusConflict)
    case func() bool {
        ce, ok := pg.AsConstraintError(err)
        return ok && ce.Kind == pg.ConstraintForeignKey
    }():
        http.Error(w, "invalid reference", http.StatusBadRequest)
    case func() bool {
        _, ok := pg.IsErrInputSyntax(err)
        return ok
    }():
        http.Error(w, "malformed input", http.StatusBadRequest)
    default:
        log.Printf("unhandled db error: %v", err) // detail stays server-side.
        http.Error(w, "internal error", http.StatusInternalServerError)
    }
}
```

The inline closures above exist only to keep the `switch` flat; a real
handler would more naturally call `AsConstraintError` once up front
and branch on `constraintErr.Kind`:

```go
if constraintErr, ok := pg.AsConstraintError(err); ok {
    switch constraintErr.Kind {
    case pg.ConstraintUnique:
        http.Error(w, "already exists", http.StatusConflict)
    case pg.ConstraintForeignKey:
        http.Error(w, "invalid reference", http.StatusBadRequest)
    case pg.ConstraintNotNull, pg.ConstraintCheck:
        http.Error(w, "invalid input", http.StatusBadRequest)
    default:
        http.Error(w, "constraint violation", http.StatusConflict)
    }
    return
}
```

Either way, the client only ever sees a stable status code and a
generic message; `constraintErr.Detail` and `constraintErr.TableName`
(which can reveal schema shape) stay in the server log, never in the
response body.

## Other Exported Sentinels

Four more exported sentinel errors live outside `errors.go`, each
tied to the specific feature it signals:

| Sentinel | Declared in | Returned when |
| --- | --- | --- |
| `ErrIntentionalRollback` | `db.go` | returned from the function passed to `InTransaction`/`InTransactionRetry` to force a rollback without treating it as a failure; see [Chapter 9](09-transactions.md). |
| `ErrIsReadOnly` | `repository.go` | `Repository[T].Insert`/`InsertSingle` is called against a table registered as read-only (e.g. a view). |
| `ErrEmptyPayload` | `listener.go` | `Listener.Accept` receives a notification whose payload is empty; see [Chapter 12](12-listen-notify.md). |
| `ErrListenerClosed` | `listener.go` | `Listener.Accept` is called on a closed listener, or `Close` interrupts a wait already in progress; see [Chapter 12](12-listen-notify.md). |

All four are plain `errors.New` values, compared with `errors.Is`
like any other sentinel; none of them carries a SQLSTATE, since none
of them represents a PostgreSQL-side condition. `ErrIntentionalRollback`
and `ErrIsReadOnly` are pg's own control-flow signals, `ErrEmptyPayload`
reports a client-side observation about a notification payload, and
`ErrListenerClosed` marks an orderly shutdown; none of them is
something the server itself rejected.

## SQLSTATE Lookup Table

Every SQLSTATE this chapter (and [Chapter 9](09-transactions.md)'s
retry classifier) checks by code, gathered in one place:

| SQLSTATE | Name | Recognized by |
| --- | --- | --- |
| `22P02` | invalid_text_representation | `IsErrInputSyntax` |
| `23502` | not_null_violation | `AsConstraintError` (`ConstraintNotNull`) |
| `23503` | foreign_key_violation | `IsErrForeignKey`, `AsConstraintError` (`ConstraintForeignKey`) |
| `23505` | unique_violation | `IsErrDuplicate`, `AsConstraintError` (`ConstraintUnique`) |
| `23514` | check_violation | `AsConstraintError` (`ConstraintCheck`) |
| `23P01` | exclusion_violation | `AsConstraintError` (`ConstraintExclusion`) |
| `40001` | serialization_failure | `IsErrRetryableTx` (Chapter 9) |
| `40P01` | deadlock_detected | `IsErrRetryableTx` (Chapter 9) |
| `42601` | syntax_error | `IsErrInputSyntax` (tsquery fallback, message-text) |
| `42703` | undefined_column | `IsErrColumnNotExists` |

Any other class-`23` code (e.g. the generic `23000`) still resolves
through `AsConstraintError` with an empty `Kind`; every other SQLSTATE
outside this table has no dedicated classifier in pg and surfaces as a
plain error carrying a `*pgconn.PgError` you can inspect with
`errors.As` yourself.

## Summary

- PostgreSQL errors carry a stable SQLSTATE code; matching on English
  message text breaks under a different `lc_messages` or a driver
  upgrade. Every classifier in this chapter checks the code first and
  only falls back to message text for errors without a structured
  `*pgconn.PgError`.
- `ErrNoRows` (`= pgx.ErrNoRows`) and `IsErrNoRows` cover the no-rows
  case; `DB.Count` swallows it and reports zero instead.
- `IsErrDuplicate`, `IsErrForeignKey`, `IsErrInputSyntax` and
  `IsErrColumnNotExists` classify the common cases and return the
  relevant name/value alongside a `bool`.
- `ConstraintKind` names five class-`23` violations by SQLSTATE;
  `ConstraintError` is the typed, extraction-only view
  `AsConstraintError` builds from a `*pgconn.PgError`. pg never wraps
  its own returned errors in a `ConstraintError`; you call
  `AsConstraintError` at the layer that turns a database error into a
  response.
- `ErrIntentionalRollback`, `ErrIsReadOnly`, `ErrEmptyPayload` and
  `ErrListenerClosed` are pg's own control-flow sentinels, unrelated
  to any SQLSTATE.

## Further Reading

- [PostgreSQL: Error Codes](https://www.postgresql.org/docs/current/errcodes-appendix.html):
  the full, authoritative SQLSTATE table this chapter's lookup table
  is a subset of.
- [PostgreSQL: Reporting Errors and Messages (PL/pgSQL)](https://www.postgresql.org/docs/current/plpgsql-errors-and-messages.html):
  how SQLSTATE and message text relate on the server side that
  produces the errors this chapter classifies.
- [pgconn.PgError](https://pkg.go.dev/github.com/jackc/pgx/v5/pgconn#PgError):
  the driver type every classifier in this chapter reads `Code`,
  `Message`, `ConstraintName`, `TableName`, `ColumnName` and `Detail`
  from.
- [Go: errors.Is and errors.As](https://pkg.go.dev/errors#Is):
  the wrapping-aware comparisons `IsErrNoRows` and every classifier in
  this chapter build on.
- [RFC 9110: HTTP Semantics, Status Codes](https://www.rfc-editor.org/rfc/rfc9110#name-status-codes):
  background for choosing a status code in the HTTP mapping example.

---

**Next Chapter**: [Schema Management and Migrations](11-schema-management-and-migrations.md)
