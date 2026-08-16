---
name: pg
description: "Development guide for the pg PostgreSQL library for Go
  (github.com/kataras/pg). Use this skill when building an application that talks to
  PostgreSQL with pg: defining entities as structs with `pg:\"...\"` tags, registering a
  schema, writing Repository[T] or *DB calls, composing WHERE clauses, sorting and
  pagination, transactions and retries, bulk loading with COPY, streaming rows, LISTEN and
  NOTIFY, migrations, typed error handling, and testing against a real database."
tags: [go, postgresql, pg, database, orm, pgx]
---

# The pg library for Go

`github.com/kataras/pg` maps Go structs to PostgreSQL tables on top of [pgx](https://github.com/jackc/pgx) v5. You declare entities as structs with `pg:"..."` tags, register them in a `Schema`, and read and write through a generic `Repository[T]` or the non-generic `*DB`. Raw SQL stays first class: the library generates the tedious statements and gets out of the way for the rest.

## Quick start

```go
package main

import (
    "context"

    "github.com/kataras/pg"
)

type Customer struct {
    ID        string    `pg:"type=uuid,primary"`
    Firstname string    `pg:"type=varchar(255)"`
    Email     string    `pg:"type=varchar(255),unique"`
    CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
}

func main() {
    schema := pg.NewSchema()
    schema.MustRegister("customers", Customer{})

    db, err := pg.Open(context.Background(), schema,
        "postgres://user:pass@localhost:5432/appdb?sslmode=verify-full")
    if err != nil {
        panic(err)
    }
    defer db.Close()

    if err = db.CreateSchema(context.Background()); err != nil {
        panic(err)
    }

    customers := pg.NewRepository[Customer](db)
    if err = customers.InsertSingle(context.Background(),
        Customer{Firstname: "Makis", Email: "makis@example.com"}, nil); err != nil {
        panic(err)
    }
}
```

- Module path: `github.com/kataras/pg`
- Go 1.26+, PostgreSQL 16.x

## Rules that prevent the common mistakes

Values are always bind parameters; identifiers never can be, so the library validates them at registration against `^[A-Za-z_][A-Za-z0-9_$]*$` and quotes them with pgx's identifier sanitizer. Anything arriving from a request must be a bind parameter, or must pass through a validating helper such as `Repository.OrderBy` or the schema-checked table-name CRUD on `*DB`.

These are developer-authored SQL, injected verbatim, and must never be built from user input: the `default`, `check`, `generated` and `conflict` tag values, `Conditions` fragments, and `OnConflict.SetWhere`.

`WithLogger` logs every statement and every bind argument, passwords included; use `WithLoggerLevel` in production.

Do not assert that a symbol exists or has a given signature without checking. This skill is not compiled against the library, so a stale claim here will not fail a build. When a reference document below disagrees with the `.go` source, the source wins and the document is a bug.

## Reference index

Load the one that matches the task; each is self-contained.

| Reference | Load it when |
| --- | --- |
| [api-map.md](references/api-map.md) | Writing code against the library and you need the real signature of an exported symbol, grouped by task |
| [architecture.md](references/architecture.md) | Understanding how the three packages fit together, tracing a query end to end, or finding where a kind of SQL gets built |
| [security.md](references/security.md) | Handling identifiers, user input, passwords, logging or `sslmode`, or reviewing a change for injection risk |
| [testing.md](references/testing.md) | Writing or running a test, deciding whether it needs a live database, or working with `pgtest` |
| [book.md](references/book.md) | Contributing to the library's own book under `book/`, or rebuilding its PDF |

## Longer form

The [pg book](https://github.com/kataras/pg/blob/main/book/output/pg-book.pdf) is a preface, 16 chapters and an epilogue covering the same ground for human readers, with the reasoning behind each API shape.
