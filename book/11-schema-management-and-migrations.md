# Chapter 11: Schema Management and Migrations

A `Schema` built with `MustRegister` (Chapter 2) exists only in Go
memory until something turns it into DDL a live PostgreSQL database
can execute. `DB.CreateSchema` does that: it reads every registered
table and emits the `CREATE SCHEMA`, extension, table, foreign key,
function and trigger statements needed to stand up a fresh database
that matches your structs, all inside one transaction.
`DB.CheckSchema` does the reverse comparison, reading a live database
back and reporting where it disagrees with the registered Go structs.
This chapter covers both, along with `DeleteSchema`, the extensions
the library creates on demand, and the `set_timestamp` trigger
convention for `updated_at` columns. The second half covers
`DB.Migrate`, a deliberately small, forward-only migration runner for
`.sql` files, and is honest about the ground it does not cover: no
down migrations, no checksums, no out-of-order detection. Every
function and behavior described here is read from `db_information.go`
and `migrate.go`.

## Table of Contents

- [Background](#background)
- [CreateSchema](#createschema)
- [CreateSchemaDumpSQL](#createschemadumpsql)
- [Extensions Created on Demand](#extensions-created-on-demand)
- [The set_timestamp Trigger Convention](#the-set_timestamp-trigger-convention)
- [CheckSchema](#checkschema)
- [DeleteSchema](#deleteschema)
- [DB.Migrate](#dbmigrate)
- [The Advisory Lock](#the-advisory-lock)
- [The schema_migrations Ledger](#the-schema_migrations-ledger)
- [Embedding Migrations](#embedding-migrations)
- [What Migrate Deliberately Does Not Do](#what-migrate-deliberately-does-not-do)
- [When to Reach for a Dedicated Migration Tool](#when-to-reach-for-a-dedicated-migration-tool)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Background

If you already know what DDL (Data Definition Language) is and have
run a migration tool before, skip to [CreateSchema](#createschema).
SQL splits into DML (Data Manipulation Language: `SELECT`, `INSERT`,
`UPDATE`, `DELETE`, the statements that read and write rows) and DDL
(`CREATE TABLE`, `ALTER TABLE`, `CREATE INDEX`, the statements that
define the shape a table's rows must take). PostgreSQL's DDL is
transactional: unlike some databases, `CREATE TABLE` and `ALTER TABLE`
inside a `BEGIN`/`COMMIT` block roll back cleanly on error, exactly
like any `INSERT`. This is what makes it safe for `CreateSchema` and
`Migrate` (below) to run a whole batch of DDL statements in one
transaction and trust that a failure partway through undoes
everything, rather than leaving the database in a half-built state. A
"migration" is simply a versioned, ordered unit of DDL (and sometimes
DML, e.g. a data backfill) applied once to move a database from one
known schema state to the next; a migration runner's job is tracking
which ones have already run so it never applies the same one twice.

## CreateSchema

`DB.CreateSchema(ctx context.Context) error` builds the full DDL for
every registered table and runs it in one transaction:

```go
schema := pg.NewSchema()
schema.MustRegister("customers", Customer{})

db, err := pg.Open(ctx, schema, connString)
if err != nil {
    log.Fatal(err)
}

if err := db.CreateSchema(ctx); err != nil {
    log.Fatal(err)
}
```

Internally it calls `CreateSchemaDumpSQL` to build the full SQL text,
then runs that text inside `db.InTransaction` (see
[Chapter 9](09-transactions.md)): if anything fails, the whole batch
rolls back and the error is returned wrapped with the generated SQL
attached, `%w:\n%s`, so a failed `CreateSchema` call tells you exactly
what it tried to run.

## CreateSchemaDumpSQL

`DB.CreateSchemaDumpSQL(ctx context.Context) (string, error)` builds
the same SQL `CreateSchema` executes, but returns it as a string
instead of running it, useful for reviewing the generated DDL, keeping
it as a reference file, or handing it to a DBA who applies schema
changes by hand. It runs four dumpers in order, each appending to the
same `strings.Builder`:

1. `CREATE SCHEMA IF NOT EXISTS <search_path>;` (the search path
   itself, e.g. `public`, validated as a bare identifier before being
   interpolated, since it cannot be defended with `QuoteIdentifier`
   the way other identifiers in this package are: it is deliberately
   emitted unquoted so PostgreSQL's normal case-folding still applies
   for existing callers).
2. The extensions the schema needs (below).
3. `CREATE TABLE` for every registered, non-read-only table, followed
   by a second pass adding every table's foreign keys, so that table
   creation order never has to match reference order.
4. The `set_timestamp` function and per-table triggers (below).

Tables registered as read-only (views, presenters; see `pg.View` and
`pg.Presenter` in Chapter 2) are skipped by both the table and foreign
key passes: pg assumes you create and maintain a view's defining query
yourself, and only ever reads from it.

## Extensions Created on Demand

`CreateSchemaDumpSQL` inspects the registered schema and emits
`CREATE EXTENSION IF NOT EXISTS ...` for exactly the extensions your
column types need, never unconditionally:

| Extension | Emitted when |
| --- | --- |
| `pgcrypto` | any registered column uses the `desc.UUID` data type, or any table has a password-hashed column (`pg:"password"`). |
| `citext` | any registered column uses `desc.CIText` (case-insensitive text). |
| `hstore` | any registered column uses `desc.HStore`. |

`pgcrypto` backs both `gen_random_uuid()` (for a UUID primary key's
default) and `crypt()`/`gen_salt()` (for password columns, used by
`SelectByUsernameAndPassword` in Chapter 4). If none of your tables
use any of these three types, none of the three `CREATE EXTENSION`
statements is emitted at all.

## The set_timestamp Trigger Convention

If a table has a column named `updated_at` (configurable via
`Schema.UpdatedAtColumnName`) whose type is a time type
(`column.Type.IsTime()`), `CreateSchemaDumpSQL` automatically wires up
a trigger that stamps it with `NOW()` on every `UPDATE`, so application
code never has to remember to set it by hand. The function is created
once, shared by every table's trigger:

```sql
CREATE OR REPLACE FUNCTION trigger_set_timestamp()
RETURNS TRIGGER AS $$
BEGIN
NEW.updated_at = NOW();
RETURN NEW;
END;
$$ LANGUAGE plpgsql;
```

and each qualifying table gets its own trigger naming that shared
function:

```sql
CREATE TRIGGER set_timestamp
BEFORE UPDATE ON customers
FOR EACH ROW
EXECUTE PROCEDURE trigger_set_timestamp();
```

Both names come from `Schema` fields: `SetTimestampTriggerName`
(default `"set_timestamp"`, used for both the trigger and, prefixed
with `trigger_`, the function) and `UpdatedAtColumnName` (default
`"updated_at"`). Setting either to an empty string disables the whole
feature: no function or trigger is registered for any table.
`CreateSchemaDumpSQL` also calls `DB.ListTriggers` first and skips a
table that already has a trigger of that name, so calling
`CreateSchema` again against a database that already has the triggers
installed does not fail or duplicate them (the statement itself is
`CREATE OR REPLACE`, but the skip check also avoids the round trip).

## CheckSchema

`DB.CheckSchema(ctx context.Context) error` reads the live database
back through `ListTables` and compares it, column by column, against
the registered `Schema`. It exists to catch drift: a database that was
altered by hand, a migration that never ran, a struct tag someone
edited without updating the database to match.

What it detects:

- **A missing or extra registered table.** It asks `ListTables` for
  exactly the table names in the schema; if PostgreSQL returns a
  different count, `CheckSchema` fails immediately with `expected %d
  tables, got %d` before checking a single column.
- **A database column with no matching code column.** For each table,
  it walks the columns PostgreSQL actually reports and looks each one
  up by name in the registered `Table`; a database column that has no
  code counterpart fails with `column %q in table %q not found in
  schema`.
- **A mismatched column definition.** For every column present on
  both sides, it renders each one's full `pg` struct tag
  representation (name, type, nullable/default, primary key, unique,
  indexes, foreign key reference, check constraint, and more, via
  `Column.FieldTagString(false)`) and compares them case-insensitively;
  a difference anywhere in that tag fails with the two tag strings
  shown side by side.

What it does not detect:

- **A code column with no database counterpart.** `CheckSchema` walks
  the columns PostgreSQL reports, not the columns the Go struct
  declares, so a field you added to a struct but never migrated into
  the actual table passes silently: nothing about the extra Go field
  is visited by the comparison. Running `CreateSchema`/`Migrate`
  before `CheckSchema`, not after, is what actually catches this case,
  since the missing column becomes DDL that either runs or fails.
- **Anything not folded into the struct tag string:** trigger
  presence, table-level comments beyond `Description`, row-level
  security policies, table or column privileges, and any index or
  constraint not modeled by pg's own tag vocabulary.
- **Presenter tables.** `desc.TableTypePresenter` (custom
  hand-written `SELECT` queries with no backing relation) is excluded
  from `desc.DatabaseTableTypes`, the type set `CheckSchema` compares
  against, since a presenter has no real table for PostgreSQL to
  report back.

`CheckSchema` also tolerates one specific, common false positive: a
column that participates in a composite or hand-made unique index
(commonly `<table>_<column>_fkey` on a junction table) is not required
to have that index declared explicitly in code, so long as the code
side has no conflicting index declaration of its own.

## DeleteSchema

`DB.DeleteSchema(ctx context.Context) error` drops the whole schema:

```go
query := `DROP SCHEMA IF EXISTS ` + QuoteIdentifier(db.searchPath) + ` CASCADE;`
```

`CASCADE` means every object inside the schema (tables, views,
sequences, functions, triggers) is dropped along with it, in one
statement, regardless of foreign key or dependency ordering. This is
appropriate for tearing down a throwaway database in a test suite
(`pgtest` uses exactly this to clean up after itself) and is not
something to reach for against a database holding data you care
about; there is no confirmation step or dry-run mode.

## DB.Migrate

Where `CreateSchema` re-derives the schema from your current Go
structs every time, `Migrate` applies a fixed sequence of `.sql` files
exactly once each, in ascending lexical order of their filenames:

```go
applied, err := db.Migrate(ctx, migrationsFS, &pg.MigrateOptions{})
if err != nil {
    log.Fatal(err)
}

for _, name := range applied {
    log.Printf("applied migration %s", name)
}
```

Lexical order is why migrations are conventionally named with a
zero-padded numeric prefix, `0001_init.sql`, `0002_add_users.sql`,
and so on: that convention keeps lexical order and intended order
identical, since `"0002_..." > "0001_..."` as plain strings the same
way `2 > 1` does as numbers.

`MigrateOptions` has two fields, both optional:

| Field | Default | Purpose |
| --- | --- | --- |
| `TableName` | `"schema_migrations"` | the tracking table, created on demand. Must be a bare identifier; validated before any SQL runs, since it is interpolated into DDL rather than bound as a parameter. |
| `Pattern` | `"*.sql"` | the `fs.Glob` pattern selecting migration filenames within `fsys`. |

The whole call runs inside a single `db.InTransaction`: if any pending
file fails, the transaction rolls back and every earlier file this
same call already applied is undone along with it, so a failed
`Migrate` call never leaves the tracking table or the schema
half-updated. A file's contents run as-is via `Exec` (the same way
`ExecFiles` runs a file, covered in Chapter 9's neighborhood in
`db.go`), so a file containing several `;`-separated statements runs
as one simple-protocol `Exec` covering all of them.

## The Advisory Lock

Before touching any file, `Migrate` takes a PostgreSQL advisory lock:

```go
db.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrateLockKey)
```

`migrateLockKey` is computed once at package init time from the
FNV-1a hash of the fixed string `"kataras/pg/migrate"`, so it is the
same value every time the package is compiled, across processes and
restarts, with no configuration needed. This is what makes it safe for
several instances of an application, for example replicas starting up
concurrently, to call `Migrate` against the same database at the same
moment: only one of them actually acquires the lock and runs the
pending files, while the rest block on `pg_advisory_xact_lock` until
the first one commits or rolls back. Once unblocked, each of the
others reads the tracking table, sees the versions the first instance
already recorded, and applies nothing. The lock is transaction-scoped
(`_xact_lock`, not a plain session-scoped advisory lock), so it
releases automatically the instant the transaction ends, commit or
rollback, and a crashed or killed process can never leave the lock
stuck for the next deployment to hang on.

## The schema_migrations Ledger

Inside the same transaction, after the lock, `Migrate` ensures the
tracking table exists:

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
    version text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);
```

then reads every `version` already recorded there, and for each
pending file not already present, executes its contents (if any) and
records its bare filename, exactly as matched by `Pattern` (e.g.
`"0001_init.sql"`, never a full path), as a new row. A file with empty
contents after reading is not executed, since there is nothing to run,
but is still recorded as applied: it counts toward "already applied"
on the next call and is never retried, so an intentionally empty
placeholder file behaves exactly like any other applied migration.

## Embedding Migrations

`Migrate` accepts any `fs.FS`, and the natural choice for a compiled
binary is `embed.FS`, so migrations ship inside the binary instead of
as loose files a deployment has to remember to copy alongside it:

```go
// migrations/0001_init.sql
// migrations/0002_add_orders.sql

//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
    ctx := context.Background()

    db, err := pg.Open(ctx, schema, connString)
    if err != nil {
        log.Fatal(err)
    }

    fsys, err := fs.Sub(migrationsFS, "migrations")
    if err != nil {
        log.Fatal(err)
    }

    if _, err := db.Migrate(ctx, fsys, nil); err != nil {
        log.Fatal(err)
    }
}
```

`fs.Sub` rooted at the embedded directory matters here: without it,
`fsys.Glob("*.sql")` would need to match `"migrations/0001_init.sql"`
against the pattern `"*.sql"`, which it does not (`*` does not cross a
path separator), so every file would look unmatched. Passing `fs.Sub`
strips the leading `migrations/` so `Pattern`'s default matches the
filenames directly. `nil` for `opts` in the example above applies both
documented defaults, `"schema_migrations"` and `"*.sql"`.

## What Migrate Deliberately Does Not Do

`Migrate` is, in its own doc comment's words, meant to be the smallest
honest migration runner, not a full migration framework. Three things
are left out on purpose, not by oversight:

- **No down/rollback direction.** There is no `.down.sql` counterpart
  and no API to reverse an applied migration. Reverting a mistake
  means writing and applying a new forward migration that undoes it,
  the same as any other schema change.
- **No checksum recorded or verified for an already-applied file.**
  Editing the contents of a file after it has already run has no
  effect on any future `Migrate` call: the ledger only remembers the
  filename, not a hash of its contents, so a silently edited
  already-applied file is not detected as drift.
- **No detection of, or special handling for, out-of-order files.** A
  file that lands lexically before one already applied (for example,
  adding `0001_forgotten.sql` after `0002_...` already ran) just
  applies wherever its filename sorts on the next call, with no
  warning that it arrived "late."

## When to Reach for a Dedicated Migration Tool

`Migrate` is a genuinely reasonable choice for a small-to-medium
application with one deployment pipeline and a team that reviews every
migration file in the same pull request as the code that needs it:
you already depend on pg, there is no second tool to install or CI
step to configure, and the advisory-lock behavior alone solves the
concurrent-replica problem correctly. It stops being enough the moment
you need any of the three exclusions above as a real feature: a team
large enough that migrations sometimes land out of order across
branches and needs a tool that detects and refuses that, a compliance
requirement to prove a migration was not edited after review
(checksums), or an operational need to roll a specific migration back
without hand-writing its inverse. At that point, reach for
[golang-migrate/migrate](https://github.com/golang-migrate/migrate)
or [pressly/goose](https://github.com/pressly/goose), both of which
implement exactly those three features pg's `Migrate` leaves out, on
top of the same idea of an ordered sequence of `.sql` files.

## Summary

- `CreateSchema` (via `CreateSchemaDumpSQL`) emits `CREATE SCHEMA`,
  needed extensions (`pgcrypto`, `citext`, `hstore`, each only when a
  registered column type actually needs it), tables, foreign keys,
  and the `set_timestamp` trigger family, all in one transaction.
- The `set_timestamp` trigger convention fires on any table with a
  time-typed `updated_at` column (both names configurable on
  `Schema`), sharing one `trigger_set_timestamp()` function across
  every table.
- `CheckSchema` compares a live database's columns, rendered as `pg`
  struct tags, against the registered Go structs. It catches a
  missing/extra table, a database column absent from code, and a
  mismatched column definition; it does not catch a code column never
  migrated into the database, or anything not folded into the tag
  string (triggers, RLS, privileges).
- `DeleteSchema` issues `DROP SCHEMA ... CASCADE`, with no
  confirmation step.
- `Migrate` applies `.sql` files from an `fs.FS` in lexical filename
  order, all inside one transaction, guarded by a fixed-key
  `pg_advisory_xact_lock` that lets concurrent replicas start up
  against the same database safely, recording each applied filename
  in a `schema_migrations` ledger.
- `Migrate` deliberately excludes down migrations, checksums and
  out-of-order detection; reach for golang-migrate or goose once a
  team's process actually needs one of those.

## Further Reading

- [PostgreSQL: CREATE EXTENSION](https://www.postgresql.org/docs/current/sql-createextension.html):
  the statement behind `pgcrypto`/`citext`/`hstore` provisioning.
- [PostgreSQL: pgcrypto](https://www.postgresql.org/docs/current/pgcrypto.html):
  `gen_random_uuid()` and `crypt()`, the two functions this extension
  provides that pg relies on.
- [PostgreSQL: Trigger Behavior](https://www.postgresql.org/docs/current/trigger-definition.html):
  `BEFORE UPDATE ... FOR EACH ROW`, the exact trigger shape
  `set_timestamp` installs.
- [PostgreSQL: Advisory Locks](https://www.postgresql.org/docs/current/explicit-locking.html#ADVISORY-LOCKS):
  `pg_advisory_xact_lock` and how a transaction-scoped advisory lock
  differs from a session-scoped one.
- [PostgreSQL: information_schema and DDL transactionality](https://www.postgresql.org/docs/current/ddl.html):
  background for why `CREATE TABLE`/`ALTER TABLE` can safely run
  inside the same transaction as any other statement.
- [golang-migrate/migrate](https://github.com/golang-migrate/migrate):
  a full-featured migration tool with down migrations and multiple
  database backends.
- [pressly/goose](https://github.com/pressly/goose):
  a migration tool supporting both `.sql` and Go-function migrations,
  with out-of-order detection.

---

**Next Chapter**: [LISTEN and NOTIFY](12-listen-notify.md)
