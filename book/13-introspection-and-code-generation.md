# Chapter 13: Introspection and Code Generation

Every chapter so far started from a Go struct and asked pg to produce
SQL. This chapter runs the library in the other direction: reading a
live PostgreSQL database and asking pg to describe it, then, from that
description, to write Go source. `db_information.go` exposes the
introspection API that DB.CreateSchema and DB.CheckSchema already use
internally to talk to `information_schema` and the `pg_catalog` system
tables. The `gen` package builds on that same API to reverse-engineer
entity structs from an existing database (`GenerateSchemaFromDatabase`)
or to emit typed column-name constants from a schema you already
registered in code (`GenerateColumnsFromSchema`). Every function and
type named below is read directly from `db_information.go` and the
`gen` package's source.

## Table of Contents

- [Background](#background)
- [Listing Tables](#listing-tables)
- [Filtering Tables](#filtering-tables)
- [Listing Columns](#listing-columns)
- [Constraints and Unique Indexes](#constraints-and-unique-indexes)
- [Triggers](#triggers)
- [Table and Database Size](#table-and-database-size)
- [Server Version](#server-version)
- [Autovacuum Controls](#autovacuum-controls)
- [Reverse-Engineering Structs From a Database](#reverse-engineering-structs-from-a-database)
- [File Layout Strategies](#file-layout-strategies)
- [Formatting Generated Code](#formatting-generated-code)
- [Typed Column-Name Constants](#typed-column-name-constants)
- [Safety Guards in the Generator](#safety-guards-in-the-generator)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Background

If you have used `pg_dump --schema-only` or a framework's "introspect
the database" command before, skip to [Listing
Tables](#listing-tables).

**Two catalogs, one merged result.** PostgreSQL keeps a full
description of every object it manages (tables, columns, constraints,
indexes, triggers) in two places: the SQL-standard
`information_schema` views, which are portable across databases but
coarse, and the `pg_catalog` system tables (`pg_class`,
`pg_constraint`, `pg_index`, and so on), which are PostgreSQL-specific
but expose detail `information_schema` does not, such as an index's
access method or a constraint's exact definition text. pg's
introspection layer queries both: `information_schema` for the basic
column shape, `pg_catalog` for constraints, unique indexes and
triggers. The result is assembled into the same `*desc.Table` and
`*desc.Column` types that `Schema.MustRegister` builds from a Go
struct, so code that reads a live database and code that reads your
struct tags end up looking at the same shape.

## Listing Tables

`DB.ListTables` is the entry point. It queries the columns for every
table in the connection's search path (or a subset you name), groups
them back into tables, and returns `[]*desc.Table`:

```go
tables, err := db.ListTables(ctx, pg.ListTablesOptions{})
if err != nil {
    log.Fatal(err)
}

for _, t := range tables {
    fmt.Println(t.Name, t.StructName, len(t.Columns))
}
```

`ListTablesOptions` has two fields:

| Field | Purpose |
| --- | --- |
| `TableNames []string` | Restrict the result to these table names; empty lists every table in the search path. |
| `Filter desc.TableFilter` | Customize or reject tables and columns after they are read (see below). |

The returned tables are sorted so that "parent" tables sort before
tables whose name contains an underscore (typically junction or
child tables), and read-only tables (views) always sort last. This
ordering is what `gen.GenerateSchemaFromDatabase` relies on when it
writes files in dependency-friendly order, though it does not
otherwise affect correctness: foreign keys are still applied in a
second pass regardless of table order.

## Filtering Tables

A raw database scan usually needs adjustment: a `jsonb` column should
decode into a specific Go struct, or a legacy table should be skipped
from generation entirely. `ListTablesOptions.Filter` takes anything
implementing the `desc.TableFilter` interface:

```go
type TableFilter interface {
    FilterTable(*Table) bool
}
```

Returning `false` drops the table from the result. `desc.TableFilterFunc`
adapts a plain function to that interface, useful for one-off filtering
logic:

```go
opts := pg.ListTablesOptions{
    Filter: pg.TableFilterFunc(func(t *pg.Table) bool {
        if t.Name == "schema_migrations" {
            return false // exclude this table entirely.
        }
        return true
    }),
}
```

For overriding column types rather than excluding tables,
`pg.MapTypeFilter` is the ready-made implementation you will reach for
most often. It maps a dotted expression (`"table.column"`, or
`"table.column.jsonb"` to force the JSONB decode path) to a Go value
whose type becomes the column's `FieldType`:

```go
opts := pg.ListTablesOptions{
    Filter: pg.MapTypeFilter{
        "customer_profiles.fields.jsonb": entity.Fields{},
    },
}
```

`MapTypeFilter.FilterTable` never rejects a table; it only rewrites
column types. Internally it panics on a malformed expression string,
since the map is developer-authored configuration, not user input
(the same reasoning `Conditions` applies to a fragment's SQL text in
[Chapter 6](06-filtering-and-pagination.md)). `ListTables` catches
that panic for you: it calls the filter through an unexported
`safeFilterTable` helper that recovers and turns the panic back into a
returned `error`, so a typo in a filter expression fails the call
instead of crashing the process at connect time.

## Listing Columns

`DB.ListColumns` is what `ListTables` calls internally, and it is also
useful directly when you only need column data:

```go
columns, err := db.ListColumns(ctx, "customers", "blogs")
```

It composes three lower-level queries and merges their results into
`[]*desc.Column`:

- `DB.ListColumnsInformationSchema` reads `information_schema.columns`
  joined with `pg_catalog.pg_attribute`, returning
  `[]*desc.ColumnBasicInfo`: table name, table description, table
  type, column name, ordinal position, description, default
  expression, parsed data type, nullability, identity and generated
  status.
- `DB.ListConstraints` reads `pg_constraint` and `pg_indexes`,
  returning `[]*desc.Constraint`, then folds primary key, unique,
  check and foreign key information onto the matching column.
- `DB.ListUniqueIndexes` reads `pg_index` for unique, non-primary,
  non-constraint-backed indexes, returning `[]*desc.UniqueIndex` (table
  name, index name, and the ordered column list), so a composite
  unique index shows up correctly on every column it covers instead of
  only the first.

The merge order matters for one detail worth knowing if you ever
compare `ListColumns` output against `CheckSchema`'s expectations: a
column that is both a primary key or a plain `Unique` column and also
carries a same-column index gets its `Index` field cleared, because
PostgreSQL already manages that index implicitly; `UniqueIndex` is
left alone even in that case, since a composite unique index spanning
other columns is still meaningful information.

## Constraints and Unique Indexes

`DB.ListConstraints` and `DB.ListUniqueIndexes` are also useful on
their own, for example to audit a schema outside of the struct-driven
workflow:

```go
constraints, err := db.ListConstraints(ctx, "orders")
for _, c := range constraints {
    fmt.Println(c.ConstraintName, c.ConstraintType, c.ColumnName)
}
```

`desc.Constraint` reports `TableName`, `ColumnName` (filled in from the
constraint's definition text for the common single-column case),
`ConstraintName`, `ConstraintType`, and `IndexType` (the access method,
e.g. `btree` or `gin`, when the constraint is backed by an index).
`desc.UniqueIndex` is simpler: `TableName`, `IndexName`, and the
ordered `Columns` slice. Both are populated from the same query
`ListColumns` uses, so there is no separate code path to keep in sync.

## Triggers

`DB.ListTriggers` reads `information_schema.triggers`, scoped to the
database catalog and the tables registered on `db`'s schema, and
returns `[]*desc.Trigger`: `Catalog`, `SearchPath`, `Name`,
`Manipulation` (`INSERT`, `UPDATE`, `DELETE`), `TableName`,
`ActionStatement`, `ActionOrientation` (`ROW` or `STATEMENT`), and
`ActionTiming` (`BEFORE` or `AFTER`). `DB.CreateSchema` calls this
before writing the `updated_at` trigger function so that reapplying
`CreateSchema` against an already-provisioned database does not try to
create the same trigger twice.

## Table and Database Size

`DB.ListTableSizes` and `DB.GetSize` both return a `SizeInfo`
(`SizePretty`, `Size` in bytes, `SizeTotalPretty`, `SizeTotal` in
bytes, where "total" adds the size of every index on the table).
`ListTableSizes` returns one `TableSizeInfo` (which embeds `SizeInfo`
plus `TableName`) per table in the search path, largest first,
regardless of whether the table is registered in your `*pg.Schema`.
`GetSize` sums the same figures across every table in the search path
into a single `SizeInfo`:

```go
sizes, err := db.ListTableSizes(ctx)
for _, s := range sizes {
    fmt.Printf("%-20s %10s (total %s)\n",
        s.TableName, s.SizePretty, s.SizeTotalPretty)
}

total, err := db.GetSize(ctx)
fmt.Println("database:", total.SizeTotalPretty)
```

These are operational, not schema, tools: they are worth wiring into a
periodic job that watches for a table growing unexpectedly, which is
usually the first sign of a missing index or a runaway retention
policy. [Chapter 15](15-observability-and-operations.md) revisits them
alongside `PoolStat` and `DB.Health`.

## Server Version

`DB.GetVersion` runs `SELECT version();` and parses out just the
version number, e.g. `"16.1"` from `"PostgreSQL 16.1 on
x86_64-pc-linux-gnu, compiled by gcc..."`. It also trims a trailing
comma that some builds append before the compiler information (for
example `"PostgreSQL 16.1,"`), so the returned string is always a bare
version number, ready to compare or log without further cleanup.

## Autovacuum Controls

Autovacuum reclaims dead tuples left behind by updates and deletes; if
it falls behind, table bloat and stale query-planner statistics
follow. Three methods let you inspect and, in narrow cases, disable
it:

```go
enabled, err := db.IsAutoVacuumEnabled(ctx) // SHOW autovacuum;

err = db.DisableAutoVacuum(ctx)             // whole database
err = db.DisableTableAutoVacuum(ctx, "events")
```

`DisableAutoVacuum` runs `ALTER DATABASE <db> SET autovacuum = off;`
and `DisableTableAutoVacuum` runs `ALTER TABLE <table> SET
(autovacuum_enabled = false);`; both quote their identifier arguments
with `QuoteIdentifier` before interpolating them (see [Chapter
14](14-security.md) for why that quoting, rather than a bind
parameter, is the correct tool here). Disabling autovacuum on a live
table is rarely the right long-term answer: it is most often reached
for temporarily around a large bulk load, then turned back on
immediately afterward with a manual `VACUUM ANALYZE`.

## Reverse-Engineering Structs From a Database

`gen.GenerateSchemaFromDatabase` is the code-generation counterpart to
everything above: it opens a connection, calls `DB.ListTables` under
the hood, and writes one Go source file per table plus a `schema.go`
that registers all of them under a `*pg.Schema`:

```go
package main

import (
    "context"
    "log"

    "github.com/kataras/pg"
    "github.com/kataras/pg/gen"
)

func main() {
    i := gen.ImportOptions{
        ConnString: "postgres://postgres:pass@localhost:5432/app" +
            "?sslmode=disable",
        ListTables: pg.ListTablesOptions{
            Filter: pg.MapTypeFilter{
                "customers.metadata.jsonb": map[string]any{},
            },
        },
    }

    e := gen.ExportOptions{
        RootDir: "./internal/entities",
    }

    err := gen.GenerateSchemaFromDatabase(context.Background(), i, e)
    if err != nil {
        log.Fatal(err)
    }
}
```

`ImportOptions` is small on purpose: `ConnString` (accepted in any
format `pg.Open` accepts) and `ListTables`, passed straight through to
`DB.ListTables`, so every filtering technique from earlier in this
chapter applies here too. If a column's PostgreSQL type has no obvious
Go equivalent (a custom enum, a domain type), `GenerateSchemaFromDatabase`
prints a diagnostic listing every `table.column.type` it could not
resolve, together with a ready-to-paste `MapTypeFilter` snippet naming
the first one, before it fails to compile anything meaningful for that
column.

`ExportOptions` controls where and how the generated files land; see
the next section for its file-naming strategies. After writing every
file, `GenerateSchemaFromDatabase` looks for `goimports` on `PATH` (the
executable name is the package variable `gen.GoImportsTool`, default
`"goimports"`) and runs it over `RootDir` if found, cleaning up import
grouping that `go/format.Source` alone does not handle. `GoImportsTool`
is a trusted, developer-set knob: it names a local executable to run,
never a value derived from the database or from network input.

## File Layout Strategies

By default, `ExportOptions.GetFileName` places every table's struct in
its own file named after the table, all in one flat package under
`RootDir`. Two ready-made strategies change that:

`EachTableToItsOwnPackage` gives every table its own subpackage, named
after its singular form: a `customers` table becomes
`RootDir/customer/customer.go`, package `customer`. This is the
strategy to reach for when tables are otherwise unrelated and you want
each entity importable on its own. It is itself a `GetFileName`-shaped
function (`func(rootDir, tableName string) string`), so assign it
directly, with no call: `GetFileName: gen.EachTableToItsOwnPackage`.

`EachTableGroupToItsOwnPackage` groups related tables together: the
first table seen with a given prefix establishes a package, and any
later table whose singular name starts with `<group>_` joins that same
package instead of getting one of its own. A `customer` table
followed by a `customer_address` table both land in package
`customer`, as `customer.go` and `customer_address.go`. This is the
strategy the library's own example test
(`gen.ExampleGenerateSchemaFromDatabase`) uses, and it is the better
default when your schema has genuine parent/child table families such
as `orders` and `order_items`.

```go
e := gen.ExportOptions{
    RootDir:    "./internal/entities",
    GetFileName: gen.EachTableGroupToItsOwnPackage(),
}
```

Note that `EachTableGroupToItsOwnPackage` returns a stateful closure
(it tracks which groups it has already seen), so call it once per
`GenerateSchemaFromDatabase` invocation rather than sharing one
instance across calls with different table sets. This makes it a
factory, not a `GetFileName` value itself: unlike
`EachTableToItsOwnPackage` above, which you assign directly, you must
call `EachTableGroupToItsOwnPackage()` to obtain the closure. Leaving
off the parentheses assigns the factory function itself, whose type is
`func() func(rootDir, tableName string) string`, not the
`GetFileName`-shaped value `ExportOptions.GetFileName` expects, so it
fails to compile.

`ExportOptions.ToSingular` and `ExportOptions.GetPackageName` are the
lower-level hooks both strategies are built from, in case neither
grouping rule fits: write your own `func(rootDir, tableName string)
string` and assign it to `GetFileName` directly.

## Formatting Generated Code

Every generated file is round-tripped through `go/format.Source`
before it is written, so the output is always valid, gofmt-clean Go,
independent of whether `goimports` happens to be installed.
`ExportOptions.FileMode` controls the permission bits the files are
written with; it defaults to `0o644` (owner read-write, group and
other read-only) specifically so that generated source is never left
world-writable by accident. Override it explicitly if your build
pipeline needs something stricter.

## Typed Column-Name Constants

`gen.GenerateColumnsFromSchema` solves a different problem: you
already have a `*pg.Schema` built from hand-written structs (as in
every earlier chapter), and you want compile-time-checked names for
its columns instead of typing `"created_at"` as a bare string wherever
a query needs one.

```go
schema := pg.NewSchema()
schema.MustRegister("companies", Company{})
schema.MustRegister("customers", Customer{})

opts := gen.ExportOptions{
    RootDir: "./definition",
}

if err := gen.GenerateColumnsFromSchema(schema, opts); err != nil {
    log.Fatal(err)
}
```

The generated package exposes one `Column`-valued field per table
column, plus a `PG_TableName` string constant, and every `Column`
implements `fmt.Stringer` by returning its underlying name:

```go
definition.Company.Name.String()      // "name"
definition.Customer.Email.String()    // "email"
definition.Customer.PG_TableName      // "customers"
```

This is where the payoff from [Chapter 6](06-filtering-and-pagination.md)
shows up. `Conditions`, `Repository.OrderBy` and `desc.Table.OrderBy`
all take plain strings for column names, and both explicitly document
that the string should come from a validated source, never straight
from a client request. Generated `Column` constants are exactly that
validated source: they can only ever name a column that genuinely
exists on the table at generation time, so a typo becomes a Go compile
error instead of a runtime "column does not exist":

```go
where := pg.Where(definition.Customer.Email.String()+" = $1", email)

orderBy, err := repo.OrderBy(definition.Customer.CreatedAt.String(), true)
if err != nil {
    log.Fatal(err)
}
```

Regenerate whenever the schema changes (a new migration, a renamed
column) and the constants stay in lockstep with the database, the same
way regenerating protobuf bindings keeps generated code in lockstep
with a `.proto` file.

## Safety Guards in the Generator

Both generators write files whose names are derived from table names,
and table names for `GenerateSchemaFromDatabase` come from a live
database rather than from your own source code. Before deriving a
path from any table name, both generators call an internal
`validateTableFileName` check that rejects an empty name, a name that
is not `filepath.IsLocal` (so no absolute path and no `..` segment
that could escape `RootDir`), and a name containing a path separator.
A table literally named `../../etc/passwd` or `a/b` therefore fails
generation with a descriptive error rather than being joined,
unchecked, onto `RootDir` and written outside it. This mirrors the
same identifier-validation discipline the rest of the library applies
to table and column names read from struct tags (see [Chapter
14](14-security.md)): a name arriving from an external system (here,
whatever the connected database happens to contain) is validated
before it is trusted to shape a filesystem path, exactly as it is
validated before being trusted to shape SQL.

## Summary

- `DB.ListTables`, `DB.ListColumns`, `DB.ListColumnsInformationSchema`,
  `DB.ListConstraints` and `DB.ListUniqueIndexes` read
  `information_schema` and `pg_catalog` and assemble the same
  `*desc.Table`/`*desc.Column` shape the struct-tag path produces.
- `ListTablesOptions.Filter` accepts any `desc.TableFilter`;
  `pg.MapTypeFilter` overrides column Go types by dotted expression,
  and a filter panic is recovered into a returned error by
  `ListTables` itself.
- `DB.ListTriggers`, `DB.ListTableSizes`, `DB.GetSize`, `DB.GetVersion`,
  `DB.IsAutoVacuumEnabled`, `DB.DisableAutoVacuum` and
  `DB.DisableTableAutoVacuum` round out the operational introspection
  surface.
- `gen.GenerateSchemaFromDatabase` reverse-engineers Go structs and a
  registering `schema.go` from a live database, driven by
  `ImportOptions` (what to read) and `ExportOptions` (where and how to
  write it), with `EachTableToItsOwnPackage` and
  `EachTableGroupToItsOwnPackage` as ready-made layout strategies.
- `gen.GenerateColumnsFromSchema` emits typed `Column` constants from
  an already-registered `*pg.Schema`, meant to feed `Conditions`,
  `Repository.OrderBy` and `desc.Table.OrderBy` instead of hand-typed
  column-name strings.
- Both generators validate a table name against filesystem-escape
  characters (`validateTableFileName`) before deriving a path from it,
  and write files at a safe, non-world-writable default `FileMode`.

## Further Reading

- [PostgreSQL: The Information Schema](https://www.postgresql.org/docs/current/information-schema.html):
  the portable `information_schema` views this chapter's basic column
  queries read from.
- [PostgreSQL: System Catalogs](https://www.postgresql.org/docs/current/catalogs.html):
  `pg_class`, `pg_constraint`, `pg_index` and the rest of the
  PostgreSQL-specific catalog this chapter's constraint and index
  queries read from.
- [PostgreSQL: Routine Vacuuming](https://www.postgresql.org/docs/current/routine-vacuuming.html):
  what autovacuum does and why disabling it is a narrow, temporary
  tool rather than a default.
- [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports):
  the tool `gen.GoImportsTool` names and runs over generated output
  when it is available on `PATH`.
- [go/format](https://pkg.go.dev/go/format): the standard-library
  formatter every generated file is round-tripped through before it is
  written to disk.

---

**Next Chapter**: [Security](14-security.md)
