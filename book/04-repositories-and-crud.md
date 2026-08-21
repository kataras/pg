# Chapter 4: Repositories and CRUD

`Repository[T]` is where most application code meets pg day to day:
one generic type, bound to one registered struct, exposing reading,
inserting, upserting, updating, deleting and counting as plain Go
methods with no query string required for the common cases. `*DB`
itself mirrors nearly all of it without the type parameter, for code
that does not know `T` at compile time, plus a table-name-based CRUD
surface added alongside it that validates every table and column name
against the registered `Schema` before any SQL is built. This chapter
documents every method on both, and the safety property that makes
the table-name API sound. Every signature below is read directly out
of `repository.go`, `db_repository.go` and `db_crud.go`.

## Table of Contents

- [Repository[T]: The Type-Safe Entry Point](#repositoryt-the-type-safe-entry-point)
- [Reading Rows](#reading-rows)
- [Inserting Rows](#inserting-rows)
- [Upserting Rows](#upserting-rows)
- [Updating Rows](#updating-rows)
- [Deleting and Duplicating Rows](#deleting-and-duplicating-rows)
- [Counting Rows](#counting-rows)
- [The Non-Generic *DB Mirror](#the-non-generic-db-mirror)
- [Table-Name CRUD on *DB](#table-name-crud-on-db)
- [Updating JSONB Columns](#updating-jsonb-columns)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Repository[T]: The Type-Safe Entry Point

```go
func NewRepository[T any](db *DB) *Repository[T]

func (db *DB) NewRepository[T any]() *Repository[T] // the method form
```

`NewRepository[T]` looks up `T`'s table definition in `db`'s `Schema`
(the same `Schema.Get` lookup by `reflect.Type` that
[Chapter 2](02-schema-and-struct-tags.md) covers) and caches it on the
returned `*Repository[T]`. It panics if `T` was never registered:
this is deliberate, the same way `MustRegister` panics on a bad tag.
A typo between the struct you registered and the struct you build a
repository for is a startup-time failure, not a runtime error the
first time a handler runs:

```go
customers := pg.NewRepository[Customer](db) // panics if Customer
                                             // was never registered
```

`db.NewRepository[Customer]()` is the same thing spelled as a method,
for call sites that read better with the database first; it calls the
function above and behaves identically. This book uses the function
form throughout.

`repo.DB()` returns the underlying `*DB`, and `repo.Table()` returns
the cached `*desc.Table` (read-only; do not mutate it). `repo.IsReadOnly()`
reports whether the underlying table is a view or presenter (see
[Views and Presenters](02-schema-and-struct-tags.md#views-and-presenters)),
which every write method below checks up front, returning
`ErrIsReadOnly` instead of attempting SQL against something that is
not a writable table. `repo.InTransaction(ctx, fn)` runs `fn` with a
transaction-scoped repository, joining an already-open transaction
instead of nesting one, exactly as `DB.InTransaction` does.

## Reading Rows

| Method | Signature | Behavior |
| --- | --- | --- |
| `Select` | `(ctx, query string, args ...any) ([]T, error)` | Runs `query`, scans every result row into a `T` by column name. |
| `SelectSingle` | `(ctx, query string, args ...any) (T, error)` | Same, for a query expected to return one row; returns `ErrNoRows` for zero. |
| `SelectByID` | `(ctx, id any) (T, error)` | `SELECT * FROM <table> WHERE <primary key> = $1 LIMIT 1`, built from the table's registered primary key column; no query string needed. |
| `SelectByUsernameAndPassword` | `(ctx, username, plainPassword string) (T, error)` | Matches the columns tagged `username`/`password`; the password side is compared with PostgreSQL's `crypt()`, reusing the stored value's own salt. |
| `Exists` | `(ctx, value T) (bool, error)` | Reports whether a row matches `value`'s non-zero fields. |

`Select`/`SelectSingle` take caller-supplied SQL, exactly like
`database/sql`'s `Query`/`QueryRow`, with one difference: the scan
step is automatic. Result columns are matched to `T`'s fields by
column name (case-insensitively), not by position, so the query need
not select columns in struct-declaration order, and can select a
subset of them:

```go
recent, err := customers.Select(ctx,
    `SELECT * FROM customers WHERE created_at > $1
     ORDER BY created_at DESC;`, since)

one, err := customers.SelectSingle(ctx,
    `SELECT * FROM customers WHERE email = $1;`, email)

byID, err := customers.SelectByID(ctx, customerID)
```

`SelectByUsernameAndPassword` only works once a `username`-tagged and
a `password`-tagged column exist on `T` (see
[Chapter 2](02-schema-and-struct-tags.md#tag-option-reference)) and,
for encrypted passwords, once `Schema.HandlePassword` is configured.

## Inserting Rows

| Method | Signature | Behavior |
| --- | --- | --- |
| `Insert` | `(ctx, values ...T) error` | Zero values: no-op. One value: delegates to `InsertSingle`. Multiple: delegates to `InsertMany`. |
| `InsertSingle` | `(ctx, value T, idPtr any) error` | Inserts one row. A non-nil `idPtr` adds a `RETURNING <primary key>` and scans it back. |
| `InsertMany` | `(ctx, values ...T) error` | Bulk-inserts via multi-row `VALUES` statements, batched, inside one transaction. |

`InsertMany` batches rows into multi-row `INSERT` statements of up to
`desc.DefaultInsertBatchSize` (500) rows per statement, rather than
one round trip per row, which is what makes inserting a few thousand
rows fast instead of a multi-second (or multi-minute) operation. The
whole call runs inside a single `InTransaction`, so a failure in a
later batch rolls back every earlier batch too. Because PostgreSQL
caps a single statement at 65535 bind parameters, a wide table (many
insertable columns) shrinks the effective batch size below 500 to
stay under that ceiling: the exact formula is `min(500, 65535 /
Table.NumInsertableColumns())`. Per-row semantics match
`InsertSingle`: a zero-valued field on a column that carries a
database default still emits the SQL `DEFAULT` keyword instead of a
bind parameter, so `clock_timestamp()`, `gen_random_uuid()` and the
like still fire per row exactly as they would for a single insert.

```go
err := customers.InsertSingle(ctx, newCustomer, &newCustomer.ID)

err := customers.Insert(ctx, c1, c2, c3) // -> InsertMany
```

## Upserting Rows

| Method | Signature | Behavior |
| --- | --- | --- |
| `Upsert` | `(ctx, forceOnConflictExpr string, values ...T) error` | Zero: no-op. One: delegates to `UpsertSingle`. Multiple: delegates to `UpsertMany`. |
| `UpsertSingle` | `(ctx, forceOnConflictExpr string, value T, idPtr any) error` | Inserts or updates one row. |
| `UpsertMany` | `(ctx, forceOnConflictExpr string, values ...T) error` | Bulk `INSERT ... ON CONFLICT DO UPDATE`, batched the same way `InsertMany` is. |

`forceOnConflictExpr` names the `ON CONFLICT` target: an empty string
uses the struct's own declared conflict target (its `unique_index` or
`unique` tag), a non-empty string usually names a specific unique
index or column expression to target for a full `DO UPDATE SET`
instead. The exported constant `pg.DoNothing` (`"DO NOTHING"`) is a
special case of that non-empty form: it keeps the same tag-derived
target the empty-string case would use, but forces `DO NOTHING`
instead of `DO UPDATE`, for the common "insert if absent, otherwise
leave the existing row alone" case. When the struct declares no
conflict-target tag at all, the result is a target-less `ON CONFLICT
DO NOTHING`, valid PostgreSQL that fires against a conflict on any
unique constraint on the table:

```go
err := customers.UpsertSingle(ctx, "customer_unique_idx",
    customerToUpsert, &customerToUpsert.ID)

err := customers.Upsert(ctx, pg.DoNothing, c1, c2, c3)
```

A `DO NOTHING` action, whether reached via `pg.DoNothing` or the
`conflict=DO NOTHING` struct tag (see
[Chapter 7](07-writing-data.md#upsert-and-the-tag-driven-conflict-target)),
still appends `RETURNING` on the single-row path, so `UpsertSingle`
with a non-nil `idPtr` behaves the way you would expect: an insert that
did not conflict populates `idPtr`, and a row skipped by the conflict
returns `ErrNoRows`. Those two outcomes are distinguishable, which is
the point. The bulk path (`UpsertMany`) never appends `RETURNING`, so
it reports no identifiers either way. See
[Chapter 7](07-writing-data.md#repository-level-conflict-methods) for
`Repository.InsertSingleOnConflict` with `OnConflict{DoNothing: true}`,
which gives the same contract with an explicit conflict target.

`UpsertMany` shares `InsertMany`'s transaction, batching and per-row
`DEFAULT` semantics; see [Inserting Rows](#inserting-rows) above for
the details.

## Updating Rows

| Method | Signature | Behavior |
| --- | --- | --- |
| `Update` | `(ctx, values ...T) (int64, error)` | Updates every column except the primary key, by primary key value. Equivalent to `UpdateOnlyColumns(ctx, nil, values...)`. |
| `UpdateOnlyColumns` | `(ctx, columnsToUpdate []string, values ...T) (int64, error)` | Updates only the named columns. |
| `UpdateExceptColumns` | `(ctx, columnsToExcept []string, values ...T) (int64, error)` | Updates every column except the named ones. |
| `UpdateOnlyColumnsReportNoRows` | `(ctx, columnsToUpdate []string, values ...T) (bool, error)` | Same as `UpdateOnlyColumns`, but returns `(false, nil)` instead of `(0, nil)`-with-no-error when no row matched, by returning `ErrNoRows` internally and swallowing it into a boolean. |

All four match rows by primary key and return the number of rows
affected (or, for the last one, whether exactly one row was). Passing
more than one value runs every update inside one transaction, and the
returned count is the sum across all of them:

```go
updated, err := customers.UpdateOnlyColumns(ctx,
    []string{"cognito_user_id"}, customerWithNewID)

updated, err := customers.UpdateExceptColumns(ctx,
    []string{"created_at"}, customerWithEverythingElseChanged)
```

`UpdateOnlyColumns` is also how you write a column back to its Go
zero value on purpose, something a naive "skip zero fields" update
strategy could never do: passing `[]string{"username"}` with
`Username: ""` on the struct genuinely sets the column to `''`,
because the column list, not the struct's zero-ness, decides what
gets written.

## Deleting and Duplicating Rows

| Method | Signature | Behavior |
| --- | --- | --- |
| `Delete` | `(ctx, values ...T) (int64, error)` | Deletes rows matching the given values' primary keys; returns the count removed. |
| `DeleteByID` | `(ctx, id any) (bool, error)` | Deletes one row by primary key value directly, without needing a whole `T`. |
| `Duplicate` | `(ctx, id any, newIDPtr any) error` | Duplicates the row with primary key `id` via `INSERT ... SELECT`; `newIDPtr`, if non-nil, receives the new row's primary key. |

```go
deleted, err := customers.Delete(ctx, existingCustomer)
removed, err := customers.DeleteByID(ctx, customerID)

var newID string
err := customers.Duplicate(ctx, customerID, &newID)
```

`Duplicate` builds its `INSERT` from a `SELECT` of the existing row
rather than reading the row into Go and re-inserting it, so it never
round-trips the row's data through your process.

## Counting Rows

```go
func (repo *Repository[T]) Count(ctx context.Context,
    query string, args ...any) (int64, error)
```

`Count` runs a caller-supplied query, typically a `COUNT(*)` or other
aggregate, and scans the single resulting value into an `int64`. A
query that yields zero rows (a `COUNT` wrapped in a `GROUP BY` that
matched nothing, for instance) counts as zero: `Count` swallows
`ErrNoRows` and returns `(0, nil)` rather than forcing every caller to
special-case it.

```go
n, err := customers.Count(ctx,
    `SELECT COUNT(*) FROM customers WHERE created_at > $1;`, since)
```

## The Non-Generic *DB Mirror

Every `Repository[T]` method above has a namesake on `*DB` itself,
for code paths that do not carry a compile-time `T`, generic helper
functions, code generation, or handlers that dispatch on a runtime
type. The `*DB` versions resolve the table definition from the
argument's own `reflect.Type` via `Schema.Get`, the exact mechanism
`Repository[T]` uses internally, just without a cached lookup:

| Method | Signature |
| --- | --- |
| `SelectByID` | `(ctx, destPtr any, id any) error` |
| `SelectByUsernameAndPassword` | `(ctx, destPtr any, username, plainPassword string) error` |
| `Exists` | `(ctx, value any) (bool, error)` |
| `Insert` | `(ctx, values ...any) error` |
| `InsertSingle` | `(ctx, value any, idPtr any) error` |
| `Upsert` | `(ctx, forceOnConflictExpr string, values ...any) error` |
| `UpsertSingle` | `(ctx, forceOnConflictExpr string, value any, idPtr any) error` |
| `Update` | `(ctx, values ...any) (int64, error)` |
| `UpdateOnlyColumns` | `(ctx, columnsToUpdate []string, values ...any) (int64, error)` |
| `UpdateExceptColumns` | `(ctx, columnsToExcept []string, values ...any) (int64, error)` |
| `Delete` | `(ctx, values ...any) (int64, error)` |
| `Duplicate` | `(ctx, value any, idPtr any) error` |
| `Mutate` | `(ctx, query string, args ...any) (int64, error)` |
| `MutateSingle` | `(ctx, query string, args ...any) (bool, error)` |

Across the whole conflict-handling family the conflict specification
comes first, immediately after `ctx`: `Upsert`, `UpsertMany`,
`UpsertSingle`, `InsertOnConflict` and `InsertSingleOnConflict` all
read the same way on `*DB` and on `Repository[T]`. The variadic
methods have no choice, since `values ...T` must be last, and the
single-row methods follow them so that one rule covers every case.

One method is worth reading carefully rather than assuming it mirrors
its `Repository[T]` counterpart exactly.

`DB.Select` is not `Repository[T].Select` without the type parameter,
it takes a scanner callback instead of returning `[]T`, because `*DB`
has no `T` to scan into automatically:

```go
func (db *DB) Select(ctx context.Context,
    scannerFunc func(Rows) error, query string, args ...any) error
```

```go
var names []string
err := db.Select(ctx, func(rows pg.Rows) error {
    for rows.Next() {
        var name string
        if err := rows.Scan(&name); err != nil {
            return err
        }
        names = append(names, name)
    }
    return nil
}, `SELECT firstname FROM customers;`)
```

For the common "scan a single column into a slice" or "scan one ad
hoc row" shapes, the generic `*DB` methods `db.QuerySlice[T]`,
`db.QueryMap[K, V]` and `db.QueryFunc[T]` (in `common.go`) usually read
better than a hand-written `DB.Select` callback; they are covered in
[Chapter 5](05-querying-and-scanning.md).

## Table-Name CRUD on *DB

`db_crud.go` adds a second, table-name-based CRUD surface on `*DB`:
`DeleteByID`, `DeleteBy`, `ExistsBy` and `CountBy` all take the target
table by its registered *name* (a string) rather than a typed Go
value, which is convenient for generic, table-agnostic code, an admin
tool, a generic REST layer, code driven by configuration, at the cost
of losing the compile-time type safety `Repository[T]` gives you.

| Method | Signature |
| --- | --- |
| `DeleteByID` | `(ctx, tableName string, id any) (bool, error)` |
| `DeleteBy` | `(ctx, tableName string, colValPairs ...any) (int64, error)` |
| `ExistsBy` | `(ctx, tableName string, colValPairs ...any) (bool, error)` |
| `CountBy` | `(ctx, tableName string, colValPairs ...any) (int64, error)` |
| `SelectSingle` | `(ctx, destPtr any, query string, args ...any) error` |

`colValPairs` is a flat `"col1", v1, "col2", v2, ...` argument list,
ANDed together into a `WHERE` clause; passing none deletes/matches
every row of the table. This shape reopens exactly the risk an
earlier, removed version of this API had: a table or column name
coming straight from a caller-controlled string, concatenated into
SQL, is an injection vector. Every method here closes that hole the
same way, and it is worth naming precisely, since it is what makes
this API safe to expose to less-trusted callers than the rest of the
library:

1. `tableName` is resolved through `Schema.GetByTableName`. An
   unknown table name returns a descriptive error before any SQL is
   built at all.
2. Every column name in `colValPairs` is resolved through the
   returned `*desc.Table` (`GetColumnByName`). An unknown column
   likewise returns a descriptive error instead of reaching a query.
3. Only names that survive both checks, meaning names that already
   exist in the registered `Schema`, are quoted with
   `QuoteIdentifier` and interpolated into the generated SQL; values
   are always passed as `$N` bind parameters, never interpolated.
4. `DeleteByID` and `DeleteBy` additionally return `ErrIsReadOnly`
   for a view, materialized view or presenter table, matching
   `Repository.Delete`'s behavior for read-only tables.

```go
removed, err := db.DeleteByID(ctx, "customers", customerID)

deleted, err := db.DeleteBy(ctx, "customers",
    "cognito_user_id", oldCognitoID)

exists, err := db.ExistsBy(ctx, "customers", "email", email)

n, err := db.CountBy(ctx, "customers", "name", "Ada")
```

`SelectSingle` on `*DB` is a different shape from the four above: it
resolves its table not from a `tableName` string but from `destPtr`'s
own registered Go type (the same `Schema.Get(reflect.TypeOf(destPtr))`
mechanism `SelectByID` uses), and its `query` is caller-supplied SQL,
not built from a table name at all. It exists to let table-agnostic
code scan one row into any registered struct type with the same
column-by-name convenience `Repository[T].SelectSingle` gives a typed
repository, but, unlike the other four methods in this section, it
does not parse or validate the query string itself, keeping it safe is
the caller's responsibility, exactly as with `Repository[T].SelectSingle`:

```go
var c Customer
err := db.SelectSingle(ctx, &c,
    `SELECT * FROM customers WHERE email = $1;`, email)
```

## Updating JSONB Columns

```go
func (db *DB) UpdateJSONB(ctx context.Context,
    tableName, columnName, rowID string,
    values map[string]any, fieldsToUpdate []string) (int64, error)
```

`UpdateJSONB` updates a single `jsonb` column, in full or in part,
without requiring you to read the row, unmarshal the column,
modify it in Go and write the whole document back. `tableName` and
`columnName` are resolved against the schema exactly like the
table-name CRUD above (an unknown table or column returns a
descriptive error before any SQL is built, and the resolved names are
quoted), and the table's primary key column (required) is used to
locate the row by `rowID`.

When `fieldsToUpdate` is empty, `values` replaces the column outright
(`SET col = $1`). When `fieldsToUpdate` is non-empty, every key in
`values` is required to also appear in `fieldsToUpdate` (a mismatch is
a descriptive error, not a silent partial write), and the update
becomes a shallow JSONB merge, `SET col = col || $1`, so keys not
present in `values` are left untouched in the stored document:

```go
// Full replace.
n, err := db.UpdateJSONB(ctx, "customers", "preferences",
    customerID, allPreferences, nil)

// Partial merge: only "theme" and "locale" change.
n, err := db.UpdateJSONB(ctx, "customers", "preferences", customerID,
    map[string]any{"theme": "dark", "locale": "en"},
    []string{"theme", "locale"})
```

## Summary

- `pg.NewRepository[T](db)` panics on an unregistered `T`, catching
  a schema/repository mismatch at startup rather than at query time.
- Reading: `Select`, `SelectSingle`, `SelectByID`,
  `SelectByUsernameAndPassword`, `Exists`. Writing:
  `Insert`/`InsertSingle`/`InsertMany`,
  `Upsert`/`UpsertSingle`/`UpsertMany` (batched, one transaction,
  capped by `desc.DefaultInsertBatchSize` and PostgreSQL's
  65535-bind-parameter limit).
- Updating: `Update`, `UpdateOnlyColumns`, `UpdateExceptColumns`,
  `UpdateOnlyColumnsReportNoRows`, all matched by primary key.
  Deleting/duplicating: `Delete`, `DeleteByID`, `Duplicate`.
  Counting: `Count` (treats `ErrNoRows` as zero).
- `*DB` mirrors nearly every `Repository[T]` method without the type
  parameter, resolving the table from the argument's own registered
  type; the conflict specification is always the first argument after
  `ctx` on both, and `DB.Select` takes a scanner callback rather than
  returning a slice.
- `DeleteByID`, `DeleteBy`, `ExistsBy`, `CountBy` and `SelectSingle`
  on `*DB` take a table by name; the first four validate the table
  (via `Schema.GetByTableName`) and every column (via
  `GetColumnByName`) before building SQL, quoting resolved names and
  binding values as `$N` parameters, which is what makes them safe
  against a caller-controlled table or column name.
- `UpdateJSONB` updates a `jsonb` column in full or, given
  `fieldsToUpdate`, as a shallow merge (`col || $1`) that leaves
  untouched keys alone.

## Further Reading

- [PostgreSQL: INSERT](https://www.postgresql.org/docs/current/sql-insert.html):
  the `ON CONFLICT` clause `Upsert`/`UpsertSingle`/`UpsertMany`
  generate.
- [PostgreSQL: The JSONB Type](https://www.postgresql.org/docs/current/datatype-json.html):
  the `||` concatenation operator `UpdateJSONB`'s partial-update path
  relies on.
- [pgx godoc: Rows](https://pkg.go.dev/github.com/jackc/pgx/v5#Rows):
  the interface behind pg's `Rows` alias, relevant to `DB.Select`'s
  scanner callback.
- [pkg.go.dev/github.com/kataras/pg](https://pkg.go.dev/github.com/kataras/pg):
  the generated godoc reference, useful for cross-checking a method
  signature against the currently installed version.

---

**Next Chapter**: [Querying and Scanning](05-querying-and-scanning.md)
