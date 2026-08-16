# Chapter 14: Security

This chapter is not a checklist of things to enable. It is an account
of the decisions already built into pg, so you know which threats they
close, which ones remain your responsibility, and where the seams
between the two run. It covers how SQL injection is prevented for
values and, separately, for identifiers; the trust boundary between
developer-controlled schema and request-controlled input, and exactly
which knobs sit on the wrong side of it if you feed them user data; how
passwords are hashed, on the server or in Go; what ends up in your logs
at each log level; what each `sslmode` actually guarantees; and how
`ConstraintError` lets a failure reach a client without leaking your
schema. Every claim below is checked against the source in
`errors.go`, `where.go`, `desc/insert_query.go`, `desc/on_conflict.go`,
`desc/struct_table.go`, `db.go` and `db_information.go`.

## Table of Contents

- [Background](#background)
- [Values Are Bind Parameters](#values-are-bind-parameters)
- [Identifiers: Validate, Then Quote](#identifiers-validate-then-quote)
- [Why Quoting Alone Is Not Enough](#why-quoting-alone-is-not-enough)
- [The Trust Boundary](#the-trust-boundary)
- [Developer-Authored SQL in Tags and Builders](#developer-authored-sql-in-tags-and-builders)
- [Passwords](#passwords)
- [Secrets in Logs](#secrets-in-logs)
- [TLS](#tls)
- [Least-Privilege Database Roles](#least-privilege-database-roles)
- [Error Messages Without Leaking Schema](#error-messages-without-leaking-schema)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Background

Every SQL injection defense in this library rests on one distinction:
a **value** and an **identifier** are protected by entirely different
mechanisms, because PostgreSQL's wire protocol only has a slot for
one of them. A value (a string, a number, a UUID you are comparing a
column against) can be sent as a bind parameter: the driver ships it
to the server separately from the query text, tagged with its type,
so there is no query text for it to corrupt no matter what bytes it
contains. An identifier (a table name, a column name, a sort
direction target) cannot: PostgreSQL only accepts `$1`, `$2`, and so
on where a *value* is expected, never where a *name* is expected (see
[jackc/pgx#885](https://github.com/jackc/pgx/issues/885)). An
identifier that needs to vary at runtime has no choice but to become
part of the query text itself.

That single fact explains almost every API decision in this chapter.
Wherever pg builds a query from data you control at compile time (a
struct's field, a literal string in your own code), it does the
concatenation for you and moves on. Wherever a name would have to come
from something you do not fully control at compile time, either the
library gives you a validating helper to call first, or it explicitly
documents that you are handling developer-authored SQL and must not
let request data anywhere near it. Knowing which case you are in for
any given API is the whole game.

## Values Are Bind Parameters

Every `Insert`, `Update`, `Select` and `Delete` path in this library
sends your struct's field values as `$1`, `$2`, ... bind parameters,
never as interpolated text. This is true of `Repository.InsertSingle`,
of `Conditions`' fragment arguments, of `db.QueryRow(ctx, query,
args...)`, and of every generated `WHERE`, `SET` and `VALUES` clause
you have seen in earlier chapters. A customer's email address, a
comment's body text, a page size someone typed into a search box: all
of it is safe to pass as an argument, in whatever form it arrives in,
because the driver keeps it out of the SQL text entirely. This is the
part of injection defense you do not have to think about; it is also
the part that is easy to accidentally bypass by hand-formatting a
query string yourself instead of using placeholders, so the discipline
is still worth stating rather than assuming.

## Identifiers: Validate, Then Quote

Table names, column names and unique index names cannot travel as bind
parameters, so pg defends them with a two-step discipline instead:
validate the name against a strict allowlist pattern, then quote it
before writing it into SQL.

Validation happens once, at `Schema.MustRegister` time, in
`desc.ConvertStructToTable`. Every table name, every resolved column
name (after tag or name-mapper resolution) and every unique index name
is checked against:

```go
var identifierRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)
```

a bare, unquoted PostgreSQL identifier: a letter or underscore, then
any number of letters, digits, underscores or dollar signs. Anything
else, quotes, dots, whitespace, non-ASCII bytes, fails registration
outright with a descriptive error naming the offending identifier. A
struct with a column tagged to a name like `id; DROP TABLE users;--`
never becomes a working `*pg.Schema` in the first place: registration
fails before a single query is ever built from it.

Quoting happens every time a validated name is written into SQL, via
`QuoteIdentifier`:

```go
func QuoteIdentifier(identifier string) string {
    return pgx.Identifier{identifier}.Sanitize()
}
```

`pgx.Identifier.Sanitize` double-quotes the identifier and doubles any
embedded `"` character, which is the standard SQL escaping rule for a
quoted identifier. `desc.Table.OrderBy`, the table-name CRUD in
`db_crud.go`, `DB.DeleteSchema`, `DB.DisableAutoVacuum`,
`DB.DisableTableAutoVacuum`, and `DB.SelectByUsernameAndPassword`'s
column references all go through this same call (either directly or
via the equivalent `pgx.Identifier{...}.Sanitize()`), so identifiers
that reach SQL text are consistently escaped, not merely assumed safe
because they passed validation once somewhere upstream.

## Why Quoting Alone Is Not Enough

It is tempting to think quoting alone (skip the regex, just call
`QuoteIdentifier` on whatever string shows up) would be sufficient:
`Sanitize` doubles embedded quotes, so nothing can break out of the
identifier position. That reasoning is incomplete, and the library's
own source has one place that demonstrates exactly why: the schema
search path.

`db.searchPath` is written, unquoted, into `CREATE SCHEMA IF NOT
EXISTS <search path>;`. It is deliberately left unquoted so that
PostgreSQL's normal identifier case-folding applies the same way it
would for any other unquoted identifier a caller might have typed by
hand elsewhere, matching how existing callers already expect the
search path to behave. An unquoted identifier in PostgreSQL is folded
to lowercase before it is resolved; a *quoted* identifier is taken
literally, case and all. Wrapping the search path in `QuoteIdentifier`
would make `MyApp` and `myapp` two different schemas instead of the
same one, silently changing which schema every later query resolves
against, for a caller who never asked for that distinction. Because
quoting was not an option here, the library falls back to strict
validation instead:

```go
// validateSearchPath reports an error if searchPath is not a safe,
// bare SQL identifier. It exists because db.searchPath is
// interpolated, unquoted, into "CREATE SCHEMA IF NOT EXISTS
// <search path>;". It is left unquoted so that Postgres' normal
// identifier case-folding applies for existing callers, which also
// means it cannot be defended with QuoteIdentifier the way the
// other identifier sinks in this package are.
func validateSearchPath(searchPath string) error {
    if !searchPathRegex.MatchString(searchPath) {
        return fmt.Errorf("invalid search path: %q", searchPath)
    }
    return nil
}
```

The lesson generalizes: quoting is an escaping transform, not a
meaning-preserving one. It stops an identifier from breaking out of
its syntactic slot, but it also changes what that identifier addresses
whenever case sensitivity matters to the caller. Validation constrains
which bytes are allowed through at all; quoting constrains how the
allowed bytes are interpreted once they arrive. pg uses both together
everywhere it safely can (registered table and column names), and
falls back to validation alone in the one place quoting would change
existing behavior (the search path). Keep that distinction in mind if
you ever build your own identifier-accepting helper on top of this
library: reach for `QuoteIdentifier` by default, and stop to ask
whether case-folding matters before you do.

## The Trust Boundary

Two categories of string end up inside a query pg builds for you, and
they come from opposite sides of your application's trust boundary.

**Developer-controlled.** Struct tags, registered schema names, and
literal strings in your own source code. These are written by you,
reviewed in code review, and fixed at compile time (or at process
start, for `Schema.MustRegister`). pg treats these as trusted: it
validates their shape (see above) but does not, and cannot, second-
guess their content.

**Request-controlled.** Anything that arrives over the wire: a query
parameter, a JSON body field, a path segment, a header. This data must
either arrive as a bind parameter (the normal case for values, see
above) or pass through a validating helper before it can influence
identifier position in a query. pg gives you exactly two such helpers:

- **`Repository.OrderBy`** (and its lower-level counterpart
  `desc.Table.OrderBy`), covered in
  [Chapter 6](06-filtering-and-pagination.md). It takes a
  caller-supplied sort column, matches it case-insensitively against
  the table's own columns (plus an explicit `extraColumns` allowlist
  you provide), and returns a quoted `"column" ASC|DESC` fragment, or
  a descriptive error naming just the rejected column, never the full
  allowlist, so the error itself is safe to show a client.
- **The schema-validated table-name CRUD** in `db_crud.go`
  (`DB.DeleteByID`, `DB.DeleteBy`, `DB.ExistsBy`, `DB.CountBy`). These
  take a table name as a plain string, convenient for generic,
  table-agnostic code, and close the reintroduced risk the same way
  every time: the table name is resolved through
  `db.schema.GetByTableName` (an unregistered name fails before any
  SQL is built) and every column name in a `colValPairs` filter is
  resolved through that table's own `*desc.Table` (an unknown column
  fails the same way). Only names that already exist in your
  registered `*pg.Schema` ever reach a query, and even then they are
  quoted with `QuoteIdentifier`.

If a caller-chosen name needs to reach identifier position and neither
of those two helpers fits your case, you are building your own
allowlist-and-quote helper, not skipping the step. Never format a
caller-supplied string directly into a query, "just this once", even
if the immediate context looks like it only accepts safe input; that
assumption is exactly what regresses six months later when the caller
changes.

## Developer-Authored SQL in Tags and Builders

A handful of pg's most flexible features work by taking a raw string
and writing it into SQL verbatim, with no escaping, no validation
beyond a bare syntax check, and no attempt to understand what it says.
This is a deliberate, documented design: these strings are meant to be
literal SQL fragments only you can write, not user input in disguise.

- The `default=`, `check=` and `generated=` struct tag options
  (`desc.Column.Default`, `CheckConstraint`, `GeneratedExpression`)
  are copied straight from the tag value into `CREATE TABLE`'s
  `DEFAULT`, `CHECK (...)` and `GENERATED ALWAYS AS (...) STORED`
  clauses.
- `desc.OnConflict.SetWhere` is appended, unescaped, as `" WHERE
  <SetWhere>"` after a generated `DO UPDATE SET` list; its own godoc
  says so directly: "developer-authored SQL, written verbatim with no
  escaping, never build it from end-user input."
- Every fragment passed to `Where`, `And`, `AndIf` and the rest of
  `Conditions` (`where.go`, [Chapter 6](06-filtering-and-pagination.md))
  is SQL text the library only renumbers placeholders in; it neither
  parses nor sanitizes it. The `column` and `elemType` arguments to
  helpers like `AndAnyOf` are the same kind of literal: `elemType` is
  even validated against a strict pattern and *panics* on a mismatch,
  the same fail-fast behavior `NewRepository` uses for a coding
  mistake, precisely because a malformed `elemType` here is a bug in
  your code, not a runtime condition to recover from.

The rule for all of these is the same, and it is worth internalizing
rather than re-deriving each time: if the string is one you wrote and
committed, it is fine, no matter how it reads. If any part of it is
assembled from a request (`"status = '" + r.URL.Query().Get("status")
+ "'"`), you have reintroduced exactly the injection surface bind
parameters exist to close, just one layer further from the query call
site than usual, and consequently easier to miss in review.

## Passwords

Marking a struct field `pg:"...,password"` gets you PostgreSQL-side
hashing with no application code: on insert or update, pg wraps the
value in `crypt($N, gen_salt('<PasswordAlg>'))`, a `pgcrypto` function
that generates a random salt and returns a salted hash; on
`Repository.SelectByUsernameAndPassword`, it compares the stored
column against `crypt($plainPassword, storedColumn)`, which reuses the
stored value's own salt to recompute the same hash and lets PostgreSQL
itself do the comparison. The plaintext password is never returned
from a successful lookup; only whether it matched.

`desc.PasswordAlg` (default `"bf"`, blowfish-based, PostgreSQL's
recommended `crypt()` scheme) selects the algorithm `gen_salt` uses.
Because this value is interpolated directly into the SQL fragment,
every code path that emits it validates it first against a fixed
allowlist:

```go
var passwordAlgAllowlist = map[string]bool{
    "bf": true, "md5": true, "xdes": true, "des": true,
}
```

`validatePasswordAlg` rejects anything outside that set, including a
value engineered to break out of the surrounding SQL string literal.
`PasswordAlg` is a package-level variable: set it once, in an `init`
function or before your first query touches a password column, and
never mutate it concurrently with query building.

For anything `pgcrypto` cannot express, or when hashing needs to
happen application-side (to match an existing user store, to use a
non-`crypt()` scheme such as bcrypt or Argon2 from Go), install a
`desc.PasswordHandler` via `Schema.HandlePassword`:

```go
type PasswordHandler struct {
    Encrypt func(tableName, plainPassword string) (
        encryptedPassword string, err error)
    Decrypt func(tableName, encryptedPassword string) (
        plainPassword string, err error)
}
```

Setting only `Encrypt` is a meaningful, common configuration; the
library's own example (`_examples/password/main.go`) recommends
exactly that, leaving `Decrypt` unset unless a caller genuinely needs
to read passwords back on select. Consider that recommendation
carefully before you implement `Decrypt` at all. A `Decrypt` function
only compiles if your
`Encrypt` function is reversible, which means the stored value is not
a one-way hash but a two-way encryption of the plaintext, recoverable
by anyone who obtains your encryption key alongside your database. A
proper password hash (bcrypt, Argon2, or PostgreSQL's own
`crypt()`/`gen_salt()`) is one-way by design: verification recomputes
the hash from a candidate password and compares, it never reconstructs
the original. If you find yourself writing a working `Decrypt`, that
is a signal to reconsider whether the field should be a password at
all, or a secret you are choosing to store reversibly for a different
reason, in which case say so explicitly rather than routing it through
the `password` tag.

## Secrets in Logs

`WithLogger` installs a `pgx` `tracelog.TraceLog` tracer hardcoded to
`tracelog.LogLevelTrace`, the most verbose level pgx has. At that
level, pgx logs every SQL statement it executes together with every
one of its bind arguments, including a plaintext password passed to
`SelectByUsernameAndPassword`. The library's own godoc is explicit
about this: "Do not use `WithLogger` against a production logger."

`WithLoggerLevel(logger, level)` is the production-safe alternative:
it installs the same tracer, but lets you choose the `tracelog.LogLevel`
instead of accepting the hardcoded `LogLevelTrace`. Lower it to
`tracelog.LogLevelWarn` or `tracelog.LogLevelError` to log substantially
less, or to `tracelog.LogLevelNone` to disable pgx's tracer logging
entirely. One caveat worth keeping in mind at every level except
`LogLevelNone`: pgx still logs the statement and its bind arguments for
a *failed* query, down to and including `LogLevelError`, so a query
that fails while carrying a password argument can still log it even
under a level chosen to be quiet in the normal case. `LogLevelNone` is
the only level that guarantees a sensitive bind argument never reaches
the logger.

```go
db, err := pg.Open(ctx, schema, connString,
    pg.WithLoggerLevel(logger, tracelog.LogLevelWarn),
)
```

## TLS

`pg.Open` accepts any connection string `pgxpool.ParseConfig`
understands, including the standard `sslmode` parameter. Its own
example connection string in `db.go`'s godoc uses
`sslmode=disable` for a local walkthrough, and immediately notes:
"For production connections, prefer `sslmode=verify-full` (or, at a
minimum, `sslmode=require`)." Each value guarantees a different, and
easy to conflate, subset of "secure":

| sslmode | Encrypts the connection | Verifies the server's certificate | Verifies the hostname matches |
| --- | --- | --- | --- |
| `disable` | No | No | No |
| `allow` | Only if the server demands it | No | No |
| `prefer` | Yes, if available (falls back to plaintext) | No | No |
| `require` | Yes | No | No |
| `verify-ca` | Yes | Yes, against a trusted CA | No |
| `verify-full` | Yes | Yes | Yes |

`require` stops a passive eavesdropper from reading traffic on the
wire, but it does not confirm you are actually talking to your
database server rather than an attacker positioned to intercept the
connection: without certificate verification, an active
man-in-the-middle can present its own certificate and `require` will
still connect. `verify-ca` closes that gap for the certificate itself,
but a certificate issued to the wrong hostname (by a CA you otherwise
trust) still passes. `verify-full` is the only mode that defends
against both, and it is the one to run in production. It needs a CA
certificate available to the client (`sslrootcert` in the connection
string, or the system trust store, depending on your provider); most
managed PostgreSQL providers publish theirs specifically for this
purpose.

## Least-Privilege Database Roles

TLS protects the connection; the role your application connects as
protects everything after that connection is established. Create a
dedicated role per application (never a shared superuser, never the
role you use for migrations) and grant it exactly the privileges the
application needs: typically `SELECT`, `INSERT`, `UPDATE`, `DELETE` on
its own tables, and nothing on tables it has no business touching.
Schema-management operations, `DB.CreateSchema`, `DB.Migrate`, `DDL`
you run by hand, belong to a separate, more privileged role used only
at deploy time, not to the role your running service holds open in its
connection pool around the clock. This is standard PostgreSQL role
hygiene rather than anything pg enforces for you, but it composes
directly with everything above: even a successful injection or a
compromised credential is bounded by what the connected role is
actually allowed to do.

## Error Messages Without Leaking Schema

A raw PostgreSQL error is not safe to hand back to a client. Its
message text can include table names, column names, constraint names
and the literal offending value (`Key (email)=(x@y.com) already
exists.`), all of which are internal details a client has no business
seeing, and some of which (the value itself) may be the user's own
input reflected back in a way that looks like a bug even when it is
not.

`AsConstraintError` (`errors.go`, introduced in
[Chapter 10](10-errors.md)) exists to break that coupling. It extracts
a typed `*ConstraintError` from a PostgreSQL SQLSTATE class-23
integrity-constraint violation, giving you a structured `Kind`
(`ConstraintUnique`, `ConstraintForeignKey`, `ConstraintNotNull`,
`ConstraintCheck`, `ConstraintExclusion`) to switch on, instead of the
raw message text to parse:

```go
err := repo.InsertSingle(ctx, customer, &customer.ID)
if cerr, ok := pg.AsConstraintError(err); ok {
    switch cerr.Kind {
    case pg.ConstraintUnique:
        http.Error(w, "already exists", http.StatusConflict)
    case pg.ConstraintForeignKey:
        http.Error(w, "referenced row does not exist",
            http.StatusUnprocessableEntity)
    case pg.ConstraintNotNull, pg.ConstraintCheck:
        http.Error(w, "invalid request", http.StatusBadRequest)
    default:
        http.Error(w, "constraint violation", http.StatusConflict)
    }
    return
}
```

The pattern that keeps this safe is choosing the client-facing message
from `Kind` (a small, closed set you wrote yourself) rather than
echoing `cerr.Error()` or `cerr.Detail` back verbatim: those two still
carry the constraint name and the offending value straight from
PostgreSQL, exactly the internal detail you are trying to keep off the
wire. Log the full `cerr` (or the original `err`) server-side, where
that detail is genuinely useful for debugging, and return only the
curated message to the caller. `AsConstraintError` is extraction-only:
pg never wraps its own returned errors in a `ConstraintError` for you,
so this mapping only happens where you explicitly call it, at whatever
layer in your application is responsible for turning database failures
into a response.

## Summary

- Values always travel as bind parameters (`$1`, `$2`, ...); this is
  automatic everywhere in the library and is not something you opt
  into.
- Identifiers cannot be bind parameters, so pg validates every table,
  column and unique index name at registration
  (`^[A-Za-z_][A-Za-z0-9_$]*$`) and quotes it with
  `QuoteIdentifier` (`pgx.Identifier.Sanitize`) wherever it is written
  into SQL.
- Quoting and validating solve different problems: quoting escapes an
  identifier so it cannot break out of its syntactic slot, but it also
  makes the identifier case-sensitive, which is why `db.searchPath` is
  validated but deliberately never quoted.
- Struct tags and registered schema names are developer-controlled and
  trusted; anything from a request must arrive as a bind parameter or
  pass through `Repository.OrderBy`/`desc.Table.OrderBy` or the
  schema-validated table-name CRUD before it can influence identifier
  position.
- `default=`, `check=`, `generated=` tags, `OnConflict.SetWhere` and
  every `Conditions` fragment are developer-authored SQL written
  verbatim: never build any of them from user input.
- Password columns can be hashed server-side via `crypt()`/`gen_salt()`
  (algorithm restricted to an allowlist) or client-side via a
  `PasswordHandler`; a working `Decrypt` implies reversible storage,
  which is a design smell for anything called a password.
- `WithLogger` logs full SQL and bind arguments, including plaintext
  passwords, at trace level; use `WithLoggerLevel` in production, down
  to `LogLevelNone` if you need a hard guarantee against a leaked
  secret in a failed-query log line.
- Prefer `sslmode=verify-full` in production; `require` alone does not
  verify the server's identity.
- Run your application under a least-privilege database role, separate
  from whatever role applies migrations.
- Map database failures to client responses through `AsConstraintError`
  and `Kind`, not by echoing `Error()` or `Detail` back to the caller.

## Further Reading

- [PostgreSQL: SQL Injection Prevention](https://www.postgresql.org/docs/current/sql-syntax-lexical.html):
  the lexical rules behind quoted versus unquoted identifiers and
  case-folding referenced in this chapter.
- [PostgreSQL: pgcrypto](https://www.postgresql.org/docs/current/pgcrypto.html):
  the extension behind `crypt()` and `gen_salt()`, including the
  algorithm identifiers `PasswordAlg` accepts.
- [PostgreSQL: SSL Support](https://www.postgresql.org/docs/current/libpq-ssl.html):
  the full definition of every `sslmode` value in the table above.
- [OWASP SQL Injection Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/SQL_Injection_Prevention_Cheat_Sheet.html):
  the general defense-in-depth framing this chapter's identifier
  discipline fits into.
- [OWASP Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html):
  current guidance on one-way password hashing, the property a
  reversible `Decrypt` gives up.
- [pgx: tracelog](https://pkg.go.dev/github.com/jackc/pgx/v5/tracelog):
  the `Logger` interface and `LogLevel` values `WithLogger` and
  `WithLoggerLevel` configure.

---

**Next Chapter**: [Observability and Operations](15-observability-and-operations.md)
