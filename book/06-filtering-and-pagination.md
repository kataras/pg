# Chapter 6: Filtering and Pagination

An endpoint that lists rows almost never wants every row. It wants a
filtered, sorted, paged slice, plus a total count for the client's
pager. Building that by hand means juggling three interlocking
concerns at once: a `WHERE` clause whose optional filters come and go
depending on what the caller passed, an `ORDER BY` column that must
come from a trusted list rather than raw user input, and a `LIMIT`/
`OFFSET` pair with its own bind parameters appended after whatever the
filter already used. pg gives each concern its own tool: `Conditions`
(in `where.go`) builds the `WHERE` fragment and renumbers its bind
parameters so one filter set can drive both the page query and its
`COUNT` twin; `desc.Table.OrderBy`/`Repository[T].OrderBy` validate a
caller-supplied sort column against the table's real columns before
quoting it; and `PageOptions`/`SelectPaginated`/`SelectWithTotal` (in
`pagination.go`) turn a plain `SELECT` into a paged one. This chapter
covers all three, verified against the current source.

## Table of Contents

- [Background](#background)
- [The Conditions Builder](#the-conditions-builder)
- [Building Fragments: Where, And, AndIf](#building-fragments-where-and-andif)
- [Array and Search Helpers](#array-and-search-helpers)
- [Zero-Gated Range and Equality Filters](#zero-gated-range-and-equality-filters)
- [Local Numbering and Build](#local-numbering-and-build)
- [One Filter Set, Two Queries](#one-filter-set-two-queries)
- [Sorting: Why ORDER BY Cannot Be a Bind Parameter](#sorting-why-order-by-cannot-be-a-bind-parameter)
- [OrderBy Validation and the Fallback Chain](#orderby-validation-and-the-fallback-chain)
- [Pagination with PageOptions and SelectPaginated](#pagination-with-pageoptions-and-selectpaginated)
- [SelectWithTotal and the Window Column](#selectwithtotal-and-the-window-column)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Background

If you are comfortable with parameterized SQL and know why string
concatenation is unsafe, skip to
[The Conditions Builder](#the-conditions-builder).

A parameterized query separates the SQL text from the values it
operates on: `WHERE email = $1` is sent to PostgreSQL once, as a fixed
string, and `$1`'s value is sent alongside it as data, never
substituted into the text. This is what makes parameterized queries
immune to SQL injection: a value cannot "break out" into SQL syntax,
because it was never part of the SQL text in the first place. The
awkward part is building a query whose filter set varies at runtime.
If a caller only supplies some of ten possible filters, the naive
approach concatenates SQL fragments and separately tracks which `$N`
belongs to which fragment, a bookkeeping problem that gets worse the
moment the same filter set needs to drive two different queries (a
page of rows, and a count of matching rows) with the placeholders
renumbered consistently in both. `Conditions` exists to remove that
bookkeeping entirely.

## The Conditions Builder

**`Conditions` builds a `WHERE` clause from raw SQL fragments whose
bind parameters are local to each fragment, then renumbers all of them
to consecutive global positions when you render the final clause.**
Inside any fragment passed to `Where` or one of the `And*` methods,
`$1`..`$n` refer to that call's own `args`, and may repeat within the
same fragment; `Build` rewrites every occurrence to the correct global
`$N` afterward.

```go
c := pg.Where("archived = $1", false).
    And("price BETWEEN $1 AND $2", 10, 100)

clause, args := c.Build(1)
// clause: (archived = $1) AND (price BETWEEN $2 AND $3)
// args:   []any{false, 10, 100}
```

`Conditions` is deliberately SQL-transparent: it does not parse or
understand the fragments you give it, it only renumbers placeholders
and joins fragments with `AND`. Column names, type names and match
expressions passed to the helper methods below are developer-authored
SQL literals, not sanitized in any way; never build them from
unvalidated user input. User input belongs in the `args` alongside a
fragment, or, for a dynamic sort column, in `Repository[T].OrderBy` /
`desc.Table.OrderBy` (covered later in this chapter). The zero value of
`Conditions` is not ready to use; start with `Where`.

## Building Fragments: Where, And, AndIf

`Where(fragment string, args ...any) *Conditions` starts a builder
with an optional first fragment. Passing `""` starts an empty builder
with nothing appended, so code that builds a filter set conditionally
can always start from `Where("")` and grow it with `And`/`And*` calls
without a special first-fragment case.

`And(fragment string, args ...any) *Conditions` appends a fragment
joined with `AND` to whatever is already accumulated; an empty
fragment is a no-op, which is what makes `Where("")` safe to grow
unconditionally. `AndIf(cond bool, fragment string, args ...any)
*Conditions` appends `fragment` only when `cond` is true; when it is
false, `args` are discarded entirely and the builder is returned
unchanged, which is the common shape for an optional filter whose
presence a caller already decided:

```go
c := pg.Where("").
    AndIf(status != "", "status = $1", status).
    AndIf(minAge > 0, "age >= $1", minAge)
```

## Array and Search Helpers

Four methods cover recurring filter shapes that would otherwise be
hand-written every time.

`AndAnyOf(column, elemType string, values any) *Conditions` appends an
optional array filter that passes when `values` is `NULL` or empty and
otherwise requires `column` to equal one of its elements:

```sql
($1::<elemType>[] IS NULL OR CARDINALITY($1::<elemType>[]) = 0
 OR <column> = ANY($1::<elemType>[]))
```

`values` is bound once as `$1` even though the rendered fragment
references it three times; `Build` gives all three occurrences the
same global position, so the underlying argument is not duplicated.
`elemType` must match `^[A-Za-z_][A-Za-z0-9_ ]*$` (a bare or
space-separated PostgreSQL type name, such as `smallint` or `double
precision`); `AndAnyOf` panics on a violation, since `elemType` is
developer-authored SQL, not user input, and a malformed value is a
coding mistake to catch immediately.

`AndMatchAnyOf(match, elemType string, values any) *Conditions` is the
same optional-array gate around a caller-supplied `match` expression
instead of a fixed `column = ANY(...)` comparison, for `EXISTS`/`NOT
EXISTS`/`IN`-subquery shapes `AndAnyOf` cannot express; `match` must
itself reference `$1` as the array parameter.

`AndNameMatchAnyOf(matchExpr string, names []string) *Conditions`
appends a multi-name search that passes when `names` is `NULL` or
empty and otherwise requires at least one non-blank name for which
`matchExpr` holds, using `unnest` internally so `matchExpr` can
reference the unnested element as `t`:

```go
c.AndNameMatchAnyOf(`name ILIKE ('%' || btrim(t) || '%')`,
    []string{"Ann", "Bob"})
```

`AndSearch(term string, substring bool, tsvExpr, textExpr string)
*Conditions` appends a text-search filter that always passes when
`term` is empty and otherwise requires a match: an `ILIKE '%term%'`
comparison against `textExpr` when `substring` is true, or a full-text
`tsvExpr @@ plainto_tsquery($1)` comparison when it is false. Only one
of `tsvExpr`/`textExpr` is used, depending on `substring`; the unused
one is ignored.

## Zero-Gated Range and Equality Filters

Three more methods gate a comparison on whether the value is the Go
zero value for its kind, so an unset filter (left at its zero value by
the caller) simply does not filter.

`AndOptionalEq(column string, value any) *Conditions` picks its gate
from `value`'s `reflect.Kind`: a string value gates on `$1 = ''`, an
integer/unsigned/float value gates on `$1 = 0`, and any other kind
(bool, `time.Time`, a slice) applies the equality unconditionally,
since there is no defined "disabled" zero value for those; pair
`AndIf` with such a value instead if it needs to be optional.

`AndMin(column string, value any) *Conditions` and `AndMax(column
string, value any) *Conditions` are lower- and upper-bound filters,
ignored when `value` is not strictly positive:

```sql
-- AndMin("price", 10)
($1 <= 0 OR price >= $1)
-- AndMax("price", 100)
($1 <= 0 OR price <= $1)
```

## Local Numbering and Build

`Build(startIndex int) (clause string, args []any)` renders every
accumulated fragment as one parenthesized, `AND`-joined clause, with
each fragment's local `$1`..`$n` placeholders renumbered to
consecutive global positions starting at `startIndex`, and returns
that clause together with the flattened arguments in matching order.
Use `startIndex` 1 for a standalone query, or a higher value to append
the clause after other, already-numbered parameters.

An empty builder, whether from `Where("")` with nothing appended or
from one where every `And*` call was skipped, renders as the literal
clause `TRUE` with nil args, so `WHERE ` + clause is always valid SQL
and never accidentally filters out every row.

`Args() []any` returns the accumulated arguments in fragment order
without paying for the renumbering pass, useful when you only need the
arguments and already know the clause is unused (rare in practice).
`NextIndex(startIndex int) int` returns `startIndex` plus the number of
accumulated arguments: the first free `$N` after a `Build(startIndex)`
call, so a caller can append its own trailing parameters (`LIMIT $N` /
`OFFSET $N+1`) without recounting placeholders by hand. `String()`
implements `fmt.Stringer` by rendering `Build(1)`'s clause, handy for
logging and tests where the exact starting index does not matter.

## One Filter Set, Two Queries

**The reason `Conditions` renumbers placeholders instead of letting
you write them once is that a filter set almost always needs to drive
two queries: the page of rows, and the total count of matching rows.**
Calling `Build` with the same `startIndex` twice on the same
`Conditions` value yields byte-identical clauses and arguments both
times, which is exactly what lets you reuse one filter set for both
queries instead of hand-writing (and hand-renumbering) the `WHERE`
clause a second time:

```go
filters := pg.Where("archived = $1", false).
    AndSearch(search, true, "search_vector", "full_name").
    AndAnyOf("category", "smallint", categoryIDs).
    AndMin("price", minPrice).
    AndMax("price", maxPrice)

whereClause, args := filters.Build(1)

total, err := repo.Count(ctx,
    "SELECT COUNT(*) FROM items WHERE "+whereClause, args...)
if err != nil {
    return nil, 0, err
}

orderBy, err := repo.OrderBy(sortColumn, sortDescending)
if err != nil {
    return nil, 0, err
}

pageQuery := fmt.Sprintf(
    "SELECT * FROM items WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d",
    whereClause, orderBy, filters.NextIndex(1), filters.NextIndex(1)+1)

pageArgs := append(append([]any{}, args...), limit, offset)
items, err := repo.Select(ctx, pageQuery, pageArgs...)
```

Both queries see identical `$1`..`$5` positions for the shared filter,
because `Build(1)` is deterministic and side-effect free; only the
page query goes on to append its own `LIMIT`/`OFFSET` parameters,
picked up exactly where `filters.NextIndex(1)` leaves off. The
`SelectPaginated` helper later in this chapter automates the `LIMIT`/
`OFFSET` appending step for the common case; `Conditions` itself is
independent of it and works equally well handed to `SelectPaginated`'s
`query` argument or to a query you assemble yourself, as shown here.

## Sorting: Why ORDER BY Cannot Be a Bind Parameter

A `WHERE` clause filters on values, and values can always be bind
parameters. An `ORDER BY` clause names a column, and PostgreSQL only
accepts a `$N` placeholder where a value is expected, never where an
identifier is expected (this is documented behavior of the wire
protocol pg's driver, pgx, implements; see
[jackc/pgx#885](https://github.com/jackc/pgx/issues/885)). A caller
that lets an end user pick the sort column therefore has no
parameterized way to pass that choice through. The unsafe shortcut,
string-concatenating the user's column name straight into the query,
reopens exactly the injection hole parameterized queries exist to
close.

The safe alternative is to validate the requested column against an
allowlist and only then quote it, never to concatenate raw input into
SQL. `desc.Table.OrderBy` and its repository-scoped wrapper,
`Repository[T].OrderBy`, do exactly that in one call.

## OrderBy Validation and the Fallback Chain

```go
func (td *Table) OrderBy(column string, descending bool,
    extraColumns ...string) (string, error)

func (repo *Repository[T]) OrderBy(column string, descending bool,
    extraColumns ...string) (string, error)
```

`column` is matched against the table's own columns
case-insensitively, the same rule `GetColumnByName` uses, or by exact,
case-sensitive membership in `extraColumns` (for computed or aliased
columns that have no `*Column` entry, such as an expression exposed
under an alias in the `SELECT` list). On a match against a table
column, the returned fragment quotes the descriptor's canonical column
name, not whatever casing the caller passed; on a match against
`extraColumns`, it quotes the caller-supplied name as given, since
there is no descriptor entry to canonicalize against. Quoting uses
`pgx.Identifier.Sanitize`, which double-quotes the identifier and
doubles any embedded `"`.

Every entry in `extraColumns` must itself be a bare, unquoted
identifier, the same shape the library already enforces for every
table, column and unique index name. `OrderBy` validates all of them
up front, before even looking at `column`, and returns a descriptive
error naming the first offending entry if any fails; without this
check, a schema-qualified or space-containing entry would still be
accepted and quoted as one bogus identifier (`"t.name"` becomes the
literal quoted identifier `"t.name"`, not the table-qualified column
`t."name"`), surfacing later as an opaque "column does not exist"
error from PostgreSQL rather than a clear one from `OrderBy` itself.

An unrecognized `column` returns a descriptive error naming just the
offending column, not the full allowlist, so the error is safe to
surface to a client without leaking the table's column names. An empty
`column` does not error; it falls back, in order, to a column named
`created_at`, then to one named `updated_at`, then to the table's
primary key column. If the table has none of those three, `OrderBy`
returns an error rather than guessing. `descending` selects the
trailing `" DESC"` (true) or `" ASC"` (false) on the returned fragment.

```go
orderBy, err := repo.OrderBy("id", true)
// orderBy: `"id" DESC`

orderBy, err = repo.OrderBy("", false, "total_score")
// falls back to created_at/updated_at/primary key, in that order
```

A caller that aliases the table in its own query can prefix the
returned fragment with the alias itself, e.g. `"f." + fragment`, since
`OrderBy` has no way to know about a query-local alias on its own.

## Pagination with PageOptions and SelectPaginated

`PageOptions` describes `LIMIT`/`OFFSET` pagination and ordering for
`SelectPaginated`:

```go
type PageOptions struct {
    Limit        int64  // zero or negative adds no LIMIT.
    Offset       int64  // zero or negative adds no OFFSET.
    OrderBy      string // interpolated, never bound; see above.
    WithoutTotal bool   // skip the derived COUNT query.
}
```

`OrderBy` here is interpolated directly into the query, never bound as
a parameter, for the same reason covered above; it must come from
`Repository[T].OrderBy`/`desc.Table.OrderBy` or a trusted literal
written by you, never directly from unvalidated user input.

```go
func (repo *Repository[T]) SelectPaginated(ctx context.Context,
    page PageOptions, query string, args ...any) ([]T, int64, error)
```

`query` is a `SELECT` without its own `ORDER BY`, `LIMIT`, `OFFSET` or
a trailing semicolon; `SelectPaginated` defensively trims trailing
whitespace and a trailing `;` before use, so a caller-supplied query
ending in either does not produce invalid SQL once extended.

Unless `page.WithoutTotal` is set, the total row count is obtained
from a query derived automatically by wrapping `query` in a subquery:
`SELECT COUNT(*) FROM (query) AS _pg_total`, executed with the same
`args` via `DB.Count`. This is a different mechanism from
`Conditions`-based reuse: it works whether or not you used `Conditions`
to build `query`'s `WHERE` clause, because it counts the whole query,
not just a filter fragment. A total of zero short-circuits:
`SelectPaginated` returns `(nil, 0, nil)` immediately, without running
the page query at all, since it would necessarily return no rows
either. When `WithoutTotal` is set, the `COUNT` query is skipped
entirely, total is reported as `-1`, and the page query still runs.

`page.Limit` and `page.Offset`, when positive, are appended to the
query as extra bind parameters (never interpolated), numbered to
continue correctly after whatever positional parameters `query` already
uses. Row scanning for the page query is identical to
`Repository[T].Select`: rows convert to `[]T` via `RowsToStruct[T]`
on the repository's own table descriptor.

```go
orderBy, err := repo.OrderBy("created_at", true)
if err != nil {
    return nil, 0, err
}

page := pg.PageOptions{Limit: 20, Offset: 40, OrderBy: orderBy}
items, total, err := repo.SelectPaginated(ctx, page,
    "SELECT * FROM customers WHERE active")
```

## SelectWithTotal and the Window Column

`SelectWithTotal` covers the opposite case: a query you have already
written in full, including its own `ORDER BY`, that produces its total
via a `COUNT(*) OVER()` window column rather than a derived subquery.

```go
func (repo *Repository[T]) SelectWithTotal(ctx context.Context,
    query string, args ...any) ([]T, int64, error)
```

`query` must include the window column aliased as `total_count`:

```go
query := `
    SELECT id, name, category, COUNT(*) OVER() AS total_count
    FROM items ORDER BY id`

items, total, err := repo.SelectWithTotal(ctx, query)
```

Internally this calls `repo.Table().RowsToStructWithTotal[T](rows,
"total_count")`, which scans every other column into `T` exactly
as `RowsToStruct` does and additionally captures the named window
column out of band, into the returned `int64`, so `T` needs no
artificial field for it. `total_count` is matched case-insensitively
against each row's field descriptions, the same rule every other
column resolution in the library uses. `COUNT(*) OVER()` yields the
same value on every row of the result set, so the last row scanned
wins for the returned total (an unspecified total comes back only if
the query does not uphold that guarantee).

Unlike `SelectPaginated`, `SelectWithTotal` does not derive a separate
`COUNT` query, does not append `ORDER BY`/`LIMIT`/`OFFSET`, and does
not trim `query`; you are responsible for the window column and any
ordering/pagination clauses yourself. Zero rows returns `(empty, 0,
nil)` from both methods.

## Summary

- `Conditions`, started with `Where` and grown with `And`/`AndIf`/
  `AndAnyOf`/`AndMatchAnyOf`/`AndNameMatchAnyOf`/`AndSearch`/
  `AndOptionalEq`/`AndMin`/`AndMax`, builds a `WHERE` clause from raw
  SQL fragments whose bind parameters are call-local and repeatable.
- `Build(startIndex)` renumbers every fragment's placeholders to
  consecutive global positions and is deterministic: calling it twice
  with the same `startIndex` on the same `Conditions` value produces
  identical output, which is what lets one filter set drive both a
  page query and its `COUNT` twin. `Args`/`NextIndex`/`String` cover
  the remaining bookkeeping.
- Dynamic `ORDER BY` cannot be a bind parameter (see
  [jackc/pgx#885](https://github.com/jackc/pgx/issues/885)); validate
  the column with `Repository[T].OrderBy`/`desc.Table.OrderBy`, which
  fall back through `created_at`, `updated_at`, then the primary key
  when no column is given, and accept an `extraColumns` allowlist for
  computed columns, itself identifier-validated.
- `SelectPaginated` wraps a plain `SELECT` with a derived
  `COUNT(*)` subquery (skippable via `PageOptions.WithoutTotal`) and
  appends `LIMIT`/`OFFSET` as bind parameters; a zero total
  short-circuits without running the page query.
- `SelectWithTotal` is for a query that already carries its own
  `COUNT(*) OVER() AS total_count` window column and its own
  ordering; it captures the total out of band via the table
  descriptor's `RowsToStructWithTotal[T]` method.

## Further Reading

- [jackc/pgx#885](https://github.com/jackc/pgx/issues/885):
  the driver issue documenting why an identifier, such as an `ORDER
  BY` column, cannot be a bind parameter.
- [PostgreSQL: Extended Query Protocol](https://www.postgresql.org/docs/current/protocol-flow.html#PROTOCOL-FLOW-EXT-QUERY):
  how parameter binding works at the wire-protocol level.
- [PostgreSQL: Window Functions](https://www.postgresql.org/docs/current/tutorial-window.html):
  the `OVER()` clause behind `SelectWithTotal`'s `total_count` column.
- [PostgreSQL: Full Text Search](https://www.postgresql.org/docs/current/textsearch.html):
  `plainto_tsquery` and `tsvector`, used by `AndSearch`'s non-substring
  mode.
- [OWASP: Query Parameterization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Query_Parameterization_Cheat_Sheet.html):
  the general case for why values, and only values, belong in bind
  parameters.

---

**Next Chapter**: [Writing Data](07-writing-data.md)
