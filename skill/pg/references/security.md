# Security invariants

These were established deliberately. Breaking one is a security
regression, not a style choice. The full narrative treatment, with more examples, is
`book/14-security.md`; this document is the terse, verified version for agent use. Every
claim below was checked against `errors.go`, `where.go`, `db.go`, `db_information.go`,
`desc/struct_table.go`, `desc/insert_query.go`, `desc/on_conflict.go` and `desc/order_by.go`.

## Values vs identifiers

PostgreSQL's wire protocol has a bind-parameter slot for a **value**, never for an
**identifier** (a table, column or sort-direction name): see
[jackc/pgx#885](https://github.com/jackc/pgx/issues/885). Every struct field value, every
`Conditions` fragment argument, every `db.QueryRow(ctx, query, args...)` call sends its
values as `$1`, `$2`, ... bind parameters; this is automatic and not something to opt into.
An identifier that must vary at runtime has no parameterized form and has to become part of
the query text itself, which is why the rest of this document exists.

## Identifiers: validate, then quote

| Step | Where | Detail |
| --- | --- | --- |
| Validate | `desc.ConvertStructToTable`, called once at `Schema.Register`/`MustRegister` time | Every table name, every resolved column name, every `unique_index` name is checked against `identifierRegex = ^[A-Za-z_][A-Za-z0-9_$]*$` (`desc/struct_table.go`); a violation fails registration before any query is ever built |
| Quote | `pg.QuoteIdentifier(identifier string) string` = `pgx.Identifier{identifier}.Sanitize()` | Double-quotes the identifier and doubles any embedded `"`; the single escaping path, used every time a validated name is written into SQL (`desc.Table.OrderBy`, the table-name CRUD in `db_crud.go`, `DB.DeleteSchema`, `DB.DisableAutoVacuum`, `DB.DisableTableAutoVacuum`, `DB.SelectByUsernameAndPassword`'s column references, `DB.Listen`/`Unlisten`'s channel) |

`migrateTableNameRegex` (`migrate.go`) and `searchPathRegex`/`listenTableIdentifierPattern`
are the same pattern, re-declared locally in the files that need it (`migrate.go`,
`db_information.go`, `db_table_listener.go`) because each interpolates its own identifier
into DDL/PL-pgSQL text rather than routing through `desc`.

## Why quoting alone is not enough

Quoting is an escaping transform, not a meaning-preserving one: it stops an identifier from
breaking out of its syntactic slot, but a *quoted* identifier is also taken literally by
PostgreSQL (case and all), while an *unquoted* one is folded to lowercase first. Wrapping an
identifier in `QuoteIdentifier` unconditionally can silently retarget it to a different
object if it was previously unquoted (`MyApp` and `myapp` become different schemas).
`db.searchPath` is the one place in the library that demonstrates this: it is interpolated
**unquoted** into `CREATE SCHEMA IF NOT EXISTS <search path>;` deliberately, so it keeps
Postgres's normal case-folding, and is defended by `validateSearchPath`
(`searchPathRegex`, same pattern as `identifierRegex`) instead of `QuoteIdentifier`. Do not
"fix" an unquoted identifier by adding quotes without checking whether case sensitivity was
load-bearing there.

## The trust boundary

| Side | Examples | Rule |
| --- | --- | --- |
| Developer-controlled | Struct tags, registered schema names, literal strings in your own source | Trusted: validated for shape, never for content |
| Request-controlled | A query parameter, JSON body field, path segment, header | Must arrive as a bind parameter, or pass through a validating helper before it can reach identifier position |

The two validating helpers for request-controlled data that needs to reach identifier
position:

- `Repository.OrderBy` / `desc.Table.OrderBy`: matches a caller-supplied sort column
  case-insensitively against the table's own columns (or an explicit `extraColumns`
  allowlist), returns a quoted fragment or an error naming just the rejected column (never
  the full allowlist).
- The schema-validated table-name CRUD in `db_crud.go` (`DB.DeleteByID`, `DeleteBy`,
  `ExistsBy`, `CountBy`): the table name is resolved via `Schema.GetByTableName` and every
  `colValPairs` column via that table's `*desc.Table`; an unregistered name fails before any
  SQL is built.

If neither fits, build your own allowlist-and-quote helper. Never format a caller-supplied
string directly into query text "just this once."

## Developer-authored SQL, injected verbatim

These are written into generated SQL with **no escaping and no validation beyond a bare
syntax check**. They are meant to be literal SQL fragments only a developer writes, never
built from end-user input:

| Source | Column/field | Lands in |
| --- | --- | --- |
| `pg:"default=..."` tag | `desc.Column.Default` | `CREATE TABLE`'s `DEFAULT` clause |
| `pg:"check=..."` tag | `desc.Column.CheckConstraint` | `CHECK (...)` constraint |
| `pg:"generated=..."` tag | `desc.Column.GeneratedExpression` | `GENERATED ALWAYS AS (...) STORED` |
| `pg:"conflict=..."` tag | `desc.Column.Conflict` | Tag-driven `ON CONFLICT ... DO UPDATE`/`DO NOTHING` action |
| `desc.OnConflict.SetWhere` / `pg.OnConflict.SetWhere` | struct field | `" WHERE <SetWhere>"` appended after a `DO UPDATE SET` list |
| Every fragment passed to `Where`/`And`/`AndIf`/... | `where.go` | `Conditions` only renumbers `$N` placeholders; it never parses or sanitizes the SQL text itself |
| `column`/`elemType`/`match`/`matchExpr`/`tsvExpr`/`textExpr` arguments to the `Conditions` helper methods | `where.go` | Same as above; `elemType` is checked against `^[A-Za-z_][A-Za-z0-9_ ]*$` and **panics** on a mismatch (a coding mistake, not a runtime condition) |

## Passwords

Two independent mechanisms, chosen per column:

| Mechanism | How | Where |
| --- | --- | --- |
| Server-side (`pg:"...,password"`, no handler) | `crypt($N, gen_salt('<PasswordAlg>'))` on insert/update; `crypt($plainPassword, storedColumn)` on `SelectByUsernameAndPassword` (reuses the stored value's own salt) | `desc/insert_query.go` |
| Go-side (`Schema.HandlePassword(desc.PasswordHandler{...})`) | Your `Encrypt`/`Decrypt` functions run in Go before/after the query | `desc/password_handler.go`, `desc/scanner.go`'s `passwordTextScanner` |

`desc.PasswordAlg` (default `"bf"`, blowfish) selects the `gen_salt` algorithm; it is
interpolated directly into SQL, so `validatePasswordAlg` rejects anything outside
`{"bf", "md5", "xdes", "des"}` before that SQL is built. Set `PasswordAlg` once at startup,
never concurrently with query building. A working `Decrypt` implies the stored value is
reversible, not a one-way hash. That is a design smell for anything called a password;
prefer setting only `Encrypt`.

`CopyFrom`/`Repository.CopyFrom` cannot use the server-side path at all (COPY cannot call a
SQL function per row). A db-side-hashed password column makes `BuildCopyPlan` return
`desc.ErrCopyPassword` immediately; a `PasswordHandler`-backed column encrypts per row in
Go instead, which does work under COPY.

## Secrets in logs

| Option | Level | Logs bind arguments (including passwords)? |
| --- | --- | --- |
| `pg.WithLogger(logger)` | Hardcoded `tracelog.LogLevelTrace` | Every statement, always. Never use in production. |
| `pg.WithLoggerLevel(logger, level)` | Caller-chosen | At every level except `tracelog.LogLevelNone`, a **failed** query still logs its statement and bind arguments down to `LogLevelError`; only `LogLevelNone` guarantees a sensitive argument never reaches the logger |

Never put a connection string into an error message or stdout (see `pg.Open`'s own error
wrapping, which deliberately includes only `host`/`dbname`, never the parsed config).

## sslmode

| `sslmode` | Encrypts | Verifies server cert | Verifies hostname |
| --- | --- | --- | --- |
| `disable` | No | No | No |
| `allow` | Only if server demands it | No | No |
| `prefer` | If available (falls back to plaintext) | No | No |
| `require` | Yes | No | No |
| `verify-ca` | Yes | Yes (trusted CA) | No |
| `verify-full` | Yes | Yes | Yes |

Prefer `verify-full` in production (or, at minimum, `require`); `require` alone does not
stop an active man-in-the-middle presenting its own certificate. `pg.Open`'s own godoc
example uses `sslmode=disable` only for a local walkthrough and says so.

## Error messages without leaking schema

`pg.AsConstraintError(err) (*ConstraintError, bool)` extracts a typed view (`Kind`,
`ConstraintName`, `TableName`, `ColumnName`, `Detail`, `Code`) from a class-23 PgError.
Map a client-facing message from `Kind` (a small, closed set you write yourself); never echo
`cerr.Error()` or `cerr.Detail` back to a caller. Both still carry the constraint name and
the offending value straight from PostgreSQL. `AsConstraintError` is extraction-only: the
library never wraps its own returned errors in a `ConstraintError`, so this mapping only
happens where explicitly called.

## Least-privilege roles

Not enforced by the library, but composes with everything above: a dedicated role per
application (never a shared superuser), granted only `SELECT`/`INSERT`/`UPDATE`/`DELETE`
on its own tables. `DB.CreateSchema`/`DB.Migrate`/hand-run DDL belong to a separate,
more-privileged role used only at deploy time, not the role held open in a running
service's connection pool.
