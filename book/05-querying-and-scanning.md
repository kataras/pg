# Chapter 5: Querying and Scanning

Raw SQL is first-class in pg. A `*DB` and a `*Repository[T]` both
expose `Query`, `QueryRow`, `QueryBoolean`, `Exec`, `Mutate`,
`MutateSingle` and `Count` directly, so you never have to fight the
library to write the query you actually want. On top of that, a small
set of generic `*DB` methods in `common.go` (`QuerySlice`,
`QueryTwoSlices`, `QueryMap`, `QuerySingle`, `QueryFunc`), each with
its own type parameters, remove the boilerplate of
looping over `pgx.Rows` for the common shapes: a single column, two
columns, or a hand-written scan function. When the result shape is a
struct, `Repository[T].Select` scans through the table's registered
descriptor, and `QueryStructs`/`QueryStruct`/`ScanStructs` do the same
for a struct that was never registered at all, including one that
embeds a JSON-decoded field from a `to_jsonb(x.*)` projection. Every
signature and behavior described here was verified against the
library source in `C:/github/pg`, primarily `common.go`, `scan.go`,
`desc/scanner.go` and `desc/loose_table.go`.

## Table of Contents

- [Background](#background)
- [Raw SQL on DB and Repository](#raw-sql-on-db-and-repository)
- [The Generic Query Helpers](#the-generic-query-helpers)
- [Scanning Into Registered Structs](#scanning-into-registered-structs)
- [Ad Hoc Read Models: QueryStructs, QueryStruct, ScanStructs](#ad-hoc-read-models-querystructs-querystruct-scanstructs)
- [Column Name Precedence in LooseTable](#column-name-precedence-in-loosetable)
- [What Gets JSON-Decoded Automatically, and What Does Not](#what-gets-json-decoded-automatically-and-what-does-not)
- [Scanning a Join with to_jsonb](#scanning-a-join-with-to_jsonb)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Background

If you have used `database/sql` before, skip to
[Raw SQL on DB and Repository](#raw-sql-on-db-and-repository); the
shapes below will already be familiar.

pg is built directly on `github.com/jackc/pgx/v5`, not on
`database/sql`. `Row` and `Rows` in this library are type aliases for
`pgx.Row` and `pgx.Rows`, and every query method ultimately calls
`pgx.Pool.Query`/`QueryRow`/`Exec` (or the transaction's equivalent,
when the `*DB` is transactional). This matters for one practical
reason: a `pgx.Row` returned by `QueryRow` defers its error until you
call `Scan`, exactly like `database/sql`, and a `pgx.Rows` returned by
`Query` must be closed, either explicitly with `rows.Close()` or by
letting one of the scanning helpers in this chapter close it for you.
Every helper described below already does that housekeeping; you only
need to manage `rows.Close()` yourself when you call `db.Query` or
`repo.Query` directly and scan the result by hand.

## Raw SQL on DB and Repository

**A repository does not hide SQL from you.** `Repository[T]` wraps a
`*DB` and a cached `*desc.Table`, and every one of its raw-SQL methods
is a one-line delegation to the same method on `*DB`:

| Method | On `*DB` | On `Repository[T]` | Returns |
| --- | --- | --- | --- |
| `Query` | yes | yes | `(Rows, error)` |
| `QueryRow` | yes | yes | `Row` |
| `QueryBoolean` | yes | yes | `(bool, error)` |
| `Exec` | yes | yes | `(pgconn.CommandTag, error)` |
| `Mutate` | yes | yes | `(int64, error)` |
| `MutateSingle` | yes | yes | `(bool, error)` |
| `Count` | yes | yes | `(int64, error)` |
| `Select` | yes (callback form) | no (use `Select(ctx, query, args...) ([]T, error)` instead) | see below |

`Query` and `QueryRow` mirror `database/sql`'s naming: `Query` returns
`Rows` for a result set of any size, `QueryRow` returns a `Row` whose
`Scan` reports `ErrNoRows` if the query produced nothing. `Exec` runs
a statement that returns no rows (an `UPDATE`, `DELETE`, `INSERT`
without `RETURNING`, or DDL) and hands back a `pgconn.CommandTag`,
pgx's wrapper around the server's command tag, from which you can read
`RowsAffected()` yourself. `Mutate` and `MutateSingle` save you that
one extra call: `Mutate` runs `Exec` and returns
`tag.RowsAffected()` directly as an `int64`, and `MutateSingle` wraps
`Mutate` and returns `rowsAffected > 0` as a `bool` for the common
"did exactly one row change" check. `QueryBoolean` is the same idea
for a query whose entire result is a single boolean column, typically
a `SELECT EXISTS (...)`. `Count` is built for a single numeric result
such as `COUNT(*)`, with one deliberate accommodation: a query that
legitimately produces no row at all (for example, a `COUNT(*) ...
GROUP BY` over an empty table, which unlike a bare `COUNT(*)` yields
zero rows rather than one row with value zero) still returns `(0,
nil)` instead of forcing you to special-case `ErrNoRows` at every call
site.

```go
n, err := repo.Count(ctx, "SELECT COUNT(*) FROM customers")

exists, err := repo.QueryBoolean(ctx,
    "SELECT EXISTS (SELECT 1 FROM customers WHERE email = $1)",
    email)

affected, err := repo.Mutate(ctx,
    "UPDATE customers SET name = $1 WHERE id = $2", name, id)
```

`Repository[T]` adds one method `*DB` does not have in this raw-SQL
group: `Select(ctx, query, args...) ([]T, error)`, which scans every
row into `T` using the repository's own table descriptor. `*DB` has a
`Select` too, but with a different, callback-based signature: `Select(
ctx, scannerFunc func(Rows) error, query string, args ...any) error`.
It runs `query`, hands the resulting `Rows` to `scannerFunc`, and
closes them afterward regardless of what `scannerFunc` returns. Reach
for `DB.Select` when you want to drive `rows.Next()`/`rows.Scan()`
yourself without worrying about closing the rows on every exit path;
reach for `db.QuerySlice`, `db.QueryFunc` or `db.QueryStructs` (below) when the
result fits one of their shapes, since they save you from writing that
loop at all.

## The Generic Query Helpers

**`common.go` exists because most result sets are shaped like a list
of one thing, not a list of structs.** Five generic methods on `*DB`
cover the common non-struct shapes, all sharing the same convention: a
query yielding no rows returns an empty, non-nil result and a nil
error, never `ErrNoRows`.

These are *methods* that carry their own type parameters, a Go 1.27
feature; before that, Go only allowed type parameters on functions, so
earlier releases of pg shipped them as package-level functions taking
the `*DB` as their first argument. If you are upgrading, rewrite
`pg.QuerySlice[string](ctx, db, q)` as `db.QuerySlice[string](ctx, q)`
and so on for the whole family.

`(db *DB) QuerySlice[T any](ctx, query, args...) ([]T, error)` scans a
single-column result directly into a `[]T`, where `T` is any type
`rows.Scan` accepts on its own (`string`, `int64`, `time.Time`, a
`uuid.UUID`, and so on):

```go
names, err := db.QuerySlice[string](ctx,
    "SELECT name FROM customers WHERE active;")
```

When `T` is `string`, `QuerySlice` silently drops every empty-string
result from the returned list; this is a documented quirk, not a
general "skip zero values" rule, and it does not apply to `QueryIter`
([Chapter 8](08-bulk-loading-and-streaming.md)) or any other helper.

`(db *DB) QueryTwoSlices[T, V any](ctx, query, args...) ([]T, []V,
error)` is the same idea for a two-column query, returning two parallel
slices instead of a slice of pairs.

`(db *DB) QueryMap[K comparable, V any](ctx, query, args...) (map[K]V,
error)` scans a two-column query into a map keyed by the first column.
A later duplicate key overwrites an earlier one, so if you need a
specific row to win, order the query accordingly (an `ORDER BY` that
places the winning row last). A no-rows query returns an empty,
non-nil map, matching the rest of the family.

`(db *DB) QuerySingle[T any](ctx, query, args...) (T, error)` is the
single-row, single-column case, built on `QueryRow(...).Scan(&entry)`.
It does surface `ErrNoRows` (there is no list to fall back to empty),
so check for it with `pg.IsErrNoRows(err)` when the caller needs to
tell "no row" apart from a real failure.

The last member of the family handles everything QuerySlice cannot
express: a handful of columns combined into an ad hoc type.
`ScanFunc[T any]` is `func(rows Rows) (T, error)`, and
`(db *DB) QueryFunc[T any](ctx, scan ScanFunc[T], query, args...)
([]T, error)` calls it once per row:

```go
type nameAndCount struct {
    Name  string
    Count int64
}

rows, err := db.QueryFunc(ctx, func(rows pg.Rows) (nameAndCount, error) {
    var nc nameAndCount
    err := rows.Scan(&nc.Name, &nc.Count)
    return nc, err
}, "SELECT name, COUNT(*) FROM customers GROUP BY name ORDER BY name;")
```

Every one of these five methods is a thin loop over `db.Query`
followed by `rows.Next()`/your scan step/`rows.Err()`, with the
`rows.Close()` handled for you via `defer`. Pick `QueryFunc` only when
the row genuinely does not fit a registered struct or a
`QueryStructs`/`ScanStructs` ad hoc type (see below); those cover the
struct case with less code than a hand-written `ScanFunc`.

## Scanning Into Registered Structs

When `T` was registered with `Schema.Register`/`MustRegister`,
`Repository[T].Select` and `Repository[T].SelectSingle` scan by
matching each result column's name, case-insensitively, against the
struct's descriptor:

```go
customers, err := repo.Select(ctx,
    "SELECT * FROM customers WHERE created_at > $1", since)

customer, err := repo.SelectSingle(ctx,
    "SELECT * FROM customers WHERE id = $1", id)
```

Under the hood both delegate to generic methods on the repository's
cached `*desc.Table`: `td.RowsToStruct[T](rows pgx.Rows) ([]T, error)`
and `td.RowToStruct[T](rows pgx.Rows) (T, error)`. A third entry
point, the plain function `desc.ConvertRowsToStruct
(td *desc.Table, rows pgx.Rows, valuePtr any) error`, scans exactly one
already-positioned row (after a successful `rows.Next()`) into an
existing pointer, and is what `DB.SelectByID` and
`Repository[T].SelectIter` (see
[Chapter 8](08-bulk-loading-and-streaming.md)) build on when a `[]T`
or a `T` return value is not what the caller needs.

Column resolution is O(1) per column: `RowsToStruct` builds a
case-insensitive lookup of the table's columns once, before the row
loop, rather than re-scanning `td.Columns` for every column of every
row. A column with no matching struct field is routed to a no-op
scanner and silently ignored, unless the table descriptor was marked
`Strict`, in which case an unmapped column is an error. A registered
table also gets type-aware scan targets you do not have to think
about: a nullable `UUID`/`Text`/`CharacterVarying` column tolerates an
unexpected `NULL` into a plain (non-pointer) string field, a nullable
`JSONB`/`JSON` column that is not itself a `sql.Scanner` is decoded
with `encoding/json/v2` automatically, and a `password` column (see
[Chapter 7](07-writing-data.md)) is routed through the table's
`PasswordHandler` if one is configured.

## Ad Hoc Read Models: QueryStructs, QueryStruct, ScanStructs

`Repository[T]` requires `T` to be registered; `NewRepository[T]`
panics otherwise. Three generic entry points in `scan.go` exist for
exactly the opposite situation: scanning a query's result into a
struct that was never registered at all, typically a join result or a
presenter shape assembled just for one query.

```go
type OrderWithCustomer struct {
    ID       int64
    Total    float64
    Customer *Customer // populated from to_jsonb(c.*) AS customer.
}

rows, err := db.QueryStructs[OrderWithCustomer](ctx, `
    SELECT o.id, o.total, to_jsonb(c.*) AS customer
    FROM orders o JOIN customers c ON c.id = o.customer_id`)
```

`(db *DB) QueryStructs[T any](ctx, query, args...) ([]T, error)` runs
`query` and scans every row into `T`. `(db *DB) QueryStruct[T any](ctx,
query, args...) (T, error)` is the single-row counterpart and reports
`ErrNoRows` when the query yields nothing. `pg.ScanStructs[T any](rows
Rows) ([]T, error)` is a package-level function rather than a method,
because `Rows` is a type alias for `pgx.Rows` and Go does not allow
methods on a type declared in another package. It scans an
already-open `Rows` value (from
`db.Query`, `repo.Query`, or a transaction's `Query`) the same way, and
closes it before returning.

All three resolve `T`'s descriptor the same way: `db`'s `Schema` is
consulted first (the exact registered descriptor Repository[T] would
use, including password handling), and only when `T` is not registered
does the resolution fall back to `desc.LooseTable`, a
schema-independent descriptor built purely from `T`'s field tags and
names via reflection. `ScanStructs` has no `*DB` to consult, so it
always takes the `LooseTable` path, even for a type that happens to be
registered elsewhere; prefer `QueryStructs`/`QueryStruct` (or
`Repository[T].Select`) when a `*DB` is available and `T` is
registered. `LooseTable`'s result is cached per `reflect.Type`, so
repeated calls for the same `T` pay the reflection cost only once.

A query yielding no rows returns an empty, non-nil slice and a nil
error from `QueryStructs`/`ScanStructs`, matching `Repository[T]
.Select`'s convention.

## Column Name Precedence in LooseTable

`desc.LooseTable` does not require a `pg` struct tag on every field the
way the registered-table path does; a field with no tag at all is
still scanned. For each exported field, the column name is resolved in
this order:

1. The `pg` tag's `name=` option, e.g. `` pg:"name=food_id" `` maps to
   `food_id`. Every other option in the tag is ignored by
   `LooseTable`, including a bare, comma-less tag such as `` pg:"id" ``
   (a naming shorthand the registered-table path understands, but
   `LooseTable` does not); such a field falls through to its `json`
   tag or snake-cased field name instead.
2. The `json` tag's name, e.g. `` json:"foodId,omitempty" `` maps to
   `foodId`. A bare `` json:"-" `` skips the field entirely, following
   `encoding/json`'s own convention; `` json:"-," `` (with the trailing
   comma) is instead read as the literal column name `-`. That second
   rule is `LooseTable`'s own: it parses the `json` tag itself with
   `strings.Cut`, so it is unaffected by the library's move to
   `encoding/json/v2`, which rejects `` json:"-," `` outright as a
   malformed tag rather than treating it as the name `-` the way v1
   did. Column naming and JSON decoding are separate steps here.
3. `SnakeCase(field name)`, e.g. `FoodID` maps to `food_id`.

A field tagged `` pg:"-" `` is skipped outright, checked before the
`json` tag. Unexported fields are always skipped. Embedded (anonymous)
struct fields are treated as one ordinary field, not flattened: an
embedded struct becomes exactly one column of its own (JSON-wrapped,
per the rules in the next section), rather than having its own fields
promoted to the top level the way a registered table's tagged nested
struct is flattened. A type that needs promoted embedded fields must
be registered with `Schema.Register` instead.

Every column `LooseTable` produces is marked nullable, since it has no
schema to consult for a field's real nullability; this is why a plain
(non-pointer) text-like field on an ad hoc type tolerates an
unexpected `NULL` the same way a registered table's nullable column
does. The resulting descriptor is also non-strict, so a result column
with no matching field is ignored rather than causing an error, which
is what lets a join or presenter query freely select extra columns the
Go type does not care about.

## What Gets JSON-Decoded Automatically, and What Does Not

**A struct, map or slice field on an ad hoc type is JSON-decoded
without a manual `json.Unmarshal` call**, whether the source column is
a genuine `jsonb`/`json` column or a `to_jsonb(...)`/`row_to_json(...)`
projection in the `SELECT` list. Specifically: a field whose type,
after removing at most one pointer indirection, has kind struct, map,
or slice is marked as a JSON column, except for three deliberate
carve-outs:

- `time.Time` (and a pointer to it) is never JSON-wrapped; it scans
  through pgx's native timestamp handling.
- `[]byte` (and `*[]byte`) is never JSON-wrapped; it is `bytea`, not
  JSON.
- Any type that implements `sql.Scanner` (or whose pointer does) is
  left alone, since it already knows how to scan itself; wrapping it
  in JSON would hand it a payload it does not expect.
- Any type defined in `github.com/jackc/pgx/v5/pgtype` (for example
  `pgtype.Range[T]`, `pgtype.Array[T]`) is excluded as well: pgx's own
  driver already scans directly into these, and none of them implement
  `sql.Scanner`, so the check above alone would not catch them. Marking
  one of these JSONB would hand pgx's native text representation (for
  example `[1,10)` for a range) to the JSON decoder and fail with a
  syntax error.

A non-pointer JSON-marked field is scanned through the same
`jsonScanner` a registered table's JSONB column uses, which both
decodes with `encoding/json/v2` and tolerates a SQL `NULL` as a no-op.
`jsonScanner` passes `json.MatchCaseInsensitiveNames(true)`, and that
option is load-bearing rather than cosmetic: a `to_jsonb(x.*)` or
`row_to_json(...)` projection carries PostgreSQL's lower-cased column
names as its keys, while the Go structs it decodes into carry `pg`
tags, or no tags at all. `encoding/json/v2` matches names exactly by
default, so without the option a `Name` field would silently stay at
its zero value for a `"name"` key. With it, the behavior you see is
the same case-insensitive matching v1 gave you for free. A
pointer field (such as `Customer *Customer` above) takes a different,
also pre-existing path: pgx's own `pgtype.JSONCodec` already decodes
generically into any Go pointer-to-pointer destination, allocating the
pointee on a non-null value and leaving it `nil` on `NULL`. Either way,
no extra configuration is required on your part.

## Scanning a Join with to_jsonb

Put the last two sections together and the earlier `OrderWithCustomer`
example is the general pattern for reading a join without writing a
custom struct-scanning function: project the "many" side's own columns
normally, and fold the "one" side into a single column with
`to_jsonb(alias.*)`.

```go
type adHocItem struct {
    ID     int64
    Name   string
    Parent *scanTestParent // decoded from to_jsonb(p.*) AS parent.
}

const joinQuery = `
    SELECT c.id, c.name, to_jsonb(p.*) AS parent, c.parent_id
    FROM child c JOIN parent p ON p.id = c.parent_id`

items, err := db.QueryStructs[adHocItem](ctx, joinQuery)
```

Three things happen at once here, all covered above: `c.id` and
`c.name` populate `adHocItem.ID`/`Name` by ordinary column-name
matching; `to_jsonb(p.*) AS parent` populates `Parent` through the
pointer-field JSON path; and `c.parent_id`, which `adHocItem` has no
field for, is silently ignored rather than causing an error, because
`LooseTable`'s descriptor is always non-strict. None of this requires
`adHocItem` to be registered with a `Schema`, and none of it requires a
hand-written `rows.Scan` call.

## Summary

- `Query`, `QueryRow`, `QueryBoolean`, `Exec`, `Mutate`, `MutateSingle`
  and `Count` exist on both `*DB` and `Repository[T]`; the repository
  versions delegate straight through. `DB.Select` is the callback form
  (`func(Rows) error`); `Repository[T].Select`/`SelectSingle` return a
  `[]T`/`T` directly.
- `db.QuerySlice`, `db.QueryTwoSlices`, `db.QueryMap` and
  `db.QuerySingle` cover one- and two-column result shapes without a
  hand-written scan loop; `db.QueryFunc` plus a `ScanFunc[T]` covers
  everything else that is not a registered struct. All five are generic
  methods on `*DB`, not package-level functions.
- A registered `T` scans through the table descriptor's own
  `td.RowsToStruct[T]`/`td.RowToStruct[T]` (or the
  `desc.ConvertRowsToStruct` function for a single already-positioned
  row), matching columns case-insensitively via a lookup built once per
  query, not once per row.
- `db.QueryStructs`, `db.QueryStruct` and the `pg.ScanStructs` function
  scan into an unregistered, ad hoc struct via `desc.LooseTable`;
  `QueryStructs`/`QueryStruct` first check whether `T` happens to be
  registered in `db`'s `Schema` and use that descriptor instead when it
  is.
- `LooseTable` resolves a column name from the `pg` tag's `name=`
  option, then the `json` tag, then `SnakeCase(field name)`; it never
  flattens embedded structs, marks every column nullable, and ignores
  unknown result columns.
- A struct, map or slice field JSON-decodes automatically, except
  `time.Time`, `[]byte`, `sql.Scanner` implementors and pgx's own
  `pgtype` types; this is what makes `to_jsonb(x.*) AS field` scan
  straight into a struct field with no manual unmarshaling. Decoding
  goes through `encoding/json/v2` with
  `json.MatchCaseInsensitiveNames(true)`, which keeps the
  case-insensitive key matching those projections depend on.

## Further Reading

- [pgx documentation](https://pkg.go.dev/github.com/jackc/pgx/v5):
  the driver `Row`, `Rows`, `CommandTag` and `pgtype` types this
  chapter builds on.
- [PostgreSQL: JSON Functions and Operators](https://www.postgresql.org/docs/current/functions-json.html):
  `to_jsonb`, `row_to_json` and the rest of the JSON function family.
- [Go: encoding/json/v2](https://pkg.go.dev/encoding/json/v2):
  the decoder every JSON-marked field goes through, including the
  `MatchCaseInsensitiveNames` option pg passes to it.
- [database/sql.Scanner](https://pkg.go.dev/database/sql#Scanner):
  the interface `LooseTable` and the registered-table scanner both
  check for before deciding to JSON-wrap a field.

---

**Next Chapter**: [Filtering and Pagination](06-filtering-and-pagination.md)
