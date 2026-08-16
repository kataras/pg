# Chapter 1: Getting Started

`github.com/kataras/pg` is a Go library for working with PostgreSQL.
It sits directly on top of `jackc/pgx/v5`, the PostgreSQL driver for
Go, and adds a thin, type-safe mapping layer on top of it: you
describe a table once, as a Go struct carrying `pg:"..."` field tags,
and the library generates the SQL needed to create that table, verify
it against a live database, and read and write rows through a generic
`Repository[T]`. The module path is `github.com/kataras/pg` and it
requires Go 1.26 or newer. This chapter installs the module, builds a
first program end to end (a struct, a schema, a connection, an insert
and a read), and names the pieces you will meet again in every later
chapter. Every symbol and example in this chapter is checked against
the library's source before being written down.

## Table of Contents

- [Background](#background)
- [Installation](#installation)
- [What pg Is](#what-pg-is)
- [Your First Program](#your-first-program)
- [The Pieces](#the-pieces)
- [Project Layout](#project-layout)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Background

If you have used Go's `database/sql` package, or another PostgreSQL
client for Go, before, skip to [Installation](#installation).

**What a driver does.** `database/sql` defines a small set of
interfaces (a connection, a statement, a set of result rows) and
delegates the actual wire protocol to a driver package registered
under a name such as "postgres". The driver is the part that speaks
PostgreSQL's binary protocol: it opens a TCP connection, authenticates,
encodes query parameters and decodes result rows into Go values. pg
does not implement this layer itself. It is built directly on
`jackc/pgx/v5`, a PostgreSQL driver that exposes its own richer API in
addition to a `database/sql`-compatible one, and pg uses that native
API rather than going through `database/sql`. That is why pg's own
`Rows` and `Row` types are aliases for `pgx.Rows` and `pgx.Row`, not
`sql.Rows` and `sql.Row`.

**What a connection pool does.** Opening a TCP connection to Postgres,
negotiating TLS and authenticating costs real time, typically several
milliseconds, so no serious program opens a fresh connection for every
query. A connection pool opens a bounded number of connections up
front or on demand, hands one out per query or transaction, and
returns it to the pool when the caller is done with it. pgx's pool
type is `pgxpool.Pool`, and a pg `*DB` wraps exactly one
`*pgxpool.Pool`: every query pg issues acquires a connection from that
pool and releases it afterward. [Chapter 3](03-connections-and-configuration.md)
covers how the pool is sized and configured.

**What a struct-to-table mapper does.** Without one, every read means
hand-writing `rows.Scan(&a, &b, &c)` in the exact column order of the
query, and every write means hand-writing placeholders and an argument
list that has to stay in sync with the struct by hand. A mapper closes
that gap in both directions: it reads a Go struct's fields (here,
through `pg:"..."` tags) to know what column each field represents,
then uses that same information to match result columns to struct
fields by name when scanning, and to build `INSERT`, `UPDATE` and
`CREATE TABLE` statements for the struct. pg's mapper lives in the
`desc` subpackage and is the subject of the whole of
[Chapter 2](02-schema-and-struct-tags.md).

## Installation

You need [Go 1.26+](https://go.dev/dl/). Create a module and add pg to
it:

```sh
mkdir myapp && cd myapp
go mod init myapp
go get github.com/kataras/pg@latest
```

pg depends on `github.com/jackc/pgx/v5` (the driver and connection
pool), `github.com/gertd/go-pluralize` (used by the naming helpers
that turn `Customer` into `customers`) and `golang.org/x/mod`. All
three are ordinary transitive dependencies pulled in by `go get`; there
is no code generator step required to start using the library, though
the `gen` subpackage can optionally generate Go structs from an
existing database (or column-constant files from an already-registered
`Schema`) when you prefer to start from SQL instead of from Go.

## What pg Is

pg is not a full ORM in the sense of lazy-loaded associations or a
query DSL that abstracts SQL away from you. You still write SQL for
anything beyond simple CRUD: `Select` and `SelectSingle` on a
`Repository[T]` both take a query string and arguments and scan the
result into your struct type. What pg removes is the boilerplate
around that SQL: connecting, building `INSERT`/`UPDATE`/`DELETE`
statements for a struct's own primary key, scanning rows into structs
by column name, and generating (and later verifying) the DDL for a
table straight from the struct that describes it. The trade-off is
explicit: you accept a `pg:"..."` tag vocabulary and a registration
step, in exchange for never hand-writing a `Scan` call list again for
the common cases.

## Your First Program

A pg program has a fixed shape: define one or more structs with `pg`
tags, register them in a `Schema`, open a `*DB`, then read and write
through a `Repository[T]`. Here it is end to end.

```go
// main.go
package main

import (
    "context"
    "log"
    "time"

    "github.com/kataras/pg"
)

// Customer maps to the "customers" table. The `pg` tag on each field
// tells the library the column's name (when given explicitly),
// PostgreSQL type, and any constraints.
type Customer struct {
    ID        string    `pg:"type=uuid,primary"`
    CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
    Firstname string    `pg:"type=varchar(255)"`
}

func main() {
    ctx := context.Background()

    // A Schema is a registry: it maps table names to the struct
    // types that describe them.
    schema := pg.NewSchema()
    schema.MustRegister("customers", Customer{})

    // Open parses the connection string, builds a pgxpool.Pool and
    // pings it once. Prefer sslmode=verify-full in production; see
    // Chapter 3 for what each sslmode actually guarantees.
    connString := "postgres://postgres:admin!123@localhost:5432/" +
        "test_db?sslmode=disable"
    db, err := pg.Open(ctx, schema, connString)
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    // CreateSchema issues CREATE TABLE (and any extensions,
    // foreign keys and triggers the schema needs) for every
    // registered table that does not exist yet.
    if err = db.CreateSchema(ctx); err != nil {
        log.Fatal(err)
    }

    // A Repository[T] is the type-safe entry point for CRUD on one
    // table. NewRepository panics if T was never registered, so a
    // typo here is caught at startup, not at the first query.
    customers := pg.NewRepository[Customer](db)

    newCustomer := Customer{Firstname: "Ada"}

    // InsertSingle's second argument, when non-nil, receives the
    // row's primary key after insert. ID has no explicit default in
    // the tag above, so the library filled one in automatically:
    // see the note below.
    err = customers.InsertSingle(ctx, newCustomer, &newCustomer.ID)
    if err != nil {
        log.Fatal(err)
    }

    existing, err := customers.SelectByID(ctx, newCustomer.ID)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("inserted customer: %#v\n", existing)
}
```

Run it:

```sh
go run main.go
```

Two details are worth pulling out. First, `Customer.ID` is tagged
`pg:"type=uuid,primary"` with no `default=` option, yet the row gets an
ID. A primary key column of type `uuid` with no explicit default and
no nullable flag is given the default `gen_random_uuid()`
automatically; this is also why `CreateSchema` emits `CREATE EXTENSION
IF NOT EXISTS pgcrypto` for any schema that uses a `uuid` column,
since `gen_random_uuid()` lives in that extension. Second,
`InsertSingle`'s `idPtr` argument is what makes the generated ID
visible to your program: pass a pointer to the field, and the
generated `INSERT` statement gets a `RETURNING` clause scanned back
into it; pass `nil` and the row is still inserted, but the ID is not
read back.

## The Pieces

The example above touches every major type in the library. Later
chapters go deep on each one; this is the map.

| Type | Role |
| --- | --- |
| `Schema` | A registry of table name to Go struct type, built with `NewSchema` and populated with `Register`/`MustRegister`. |
| `*DB` | A connection to one PostgreSQL database, wrapping a `*pgxpool.Pool`, opened with `Open` or `OpenPool`. Also exposes non-generic CRUD, transactions, `LISTEN`/`NOTIFY` and schema DDL. |
| `Repository[T]` | A type-safe, table-scoped view over `*DB` for one registered struct type `T`. Built with `NewRepository[T](db)`. |
| `desc` (subpackage) | Parses `pg` struct tags into `Table`/`Column` descriptors and builds the SQL (`CREATE TABLE`, `INSERT`, `UPDATE`, `DELETE`, scanning) behind both `*DB` and `Repository[T]`. Used directly only for advanced cases. |
| `gen` (subpackage) | Generates Go struct source from a live database's schema, or column-constant files from an already-registered `Schema`, for teams that prefer to start from SQL. |
| `pgtest` (subpackage) | Test helpers for spinning up a throwaway PostgreSQL instance (or reusing one) in Go tests. |

`Repository[T]` is deliberately generic per table rather than one
untyped `DB.Query` call per operation: `pg.NewRepository[Customer](db)`
gives you `Select`, `Insert`, `Update` and the rest already scoped to
the `customers` table and to the `Customer` type, so a mismatched
struct type is a compile error, not a runtime scan failure.
[Chapter 4](04-repositories-and-crud.md) covers every method it
exposes, along with the non-generic mirror on `*DB` itself for code
that does not want a type parameter.

## Project Layout

A single `main.go` is a fine layout while you are learning the
library. As a program grows, a common split keeps struct definitions,
the schema registration and the repositories in their own package,
separate from the code that uses them:

```
myapp/
├── go.mod
├── main.go          # wiring: Open, CreateSchema, run
├── store/            # domain package: structs, schema, repositories
│   ├── schema.go      # struct definitions and Schema registration
│   └── customers.go   # CustomerRepository built on pg.Repository[Customer]
└── api/               # whatever consumes store (HTTP handlers, CLI, ...)
```

The rule that keeps this healthy is the same one that keeps any
layered Go program healthy: `store` knows nothing about `api`, and
`api` depends on `store`, never the other way around. That is what
lets you test `store` with a real (or `pgtest`-provisioned) database
and no HTTP server in the loop at all.

## Summary

- pg is a type-safe mapping layer on top of `jackc/pgx/v5`, not a
  full ORM; you still write SQL for anything past simple CRUD.
- Install with `go get github.com/kataras/pg@latest`; no code
  generation step is required to start.
- A program's shape is fixed: define structs with `pg:"..."` tags,
  register them with `schema.MustRegister(tableName, StructValue{})`,
  open a `*DB` with `pg.Open`, then read and write through
  `pg.NewRepository[T](db)`.
- `db.CreateSchema(ctx)` issues the DDL for every registered table;
  a `uuid` primary key with no explicit default gets
  `gen_random_uuid()` automatically, and pulls in the `pgcrypto`
  extension.
- `InsertSingle(ctx, value, &value.ID)` inserts a row and reads its
  generated primary key back through a `RETURNING` clause; passing
  `nil` instead skips that read.
- `Schema`, `*DB`, `Repository[T]`, and the `desc`, `gen` and
  `pgtest` subpackages are the whole surface area; every later
  chapter is about one of them.

## Further Reading

- [A Tour of Go](https://go.dev/tour/): the interactive introduction
  to the Go language this book assumes.
- [Managing dependencies (go.dev)](https://go.dev/doc/modules/managing-dependencies):
  the official guide to Go modules, `go get` and `go.mod`.
- [pgx godoc](https://pkg.go.dev/github.com/jackc/pgx/v5): the driver
  pg is built on; useful whenever you reach past pg's own API for
  `Rows`, `Row` or pool configuration.
- [PostgreSQL: gen_random_uuid()](https://www.postgresql.org/docs/current/pgcrypto.html):
  the pgcrypto function pg uses as the default for an untagged `uuid`
  primary key.
- [pkg.go.dev/github.com/kataras/pg](https://pkg.go.dev/github.com/kataras/pg):
  the generated godoc reference for every exported symbol in this book.

---

**Next Chapter**: [Schema and Struct Tags](02-schema-and-struct-tags.md)
