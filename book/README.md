# Preface

pg is a PostgreSQL library for Go, built on top of pgx/v5. It maps Go
structs to database tables through `pg:"..."` struct tags, generates and
verifies the schema those tags describe, and exposes a generic
`Repository[T]` for the CRUD operations most applications write by hand
against `database/sql`. This book is its guide: a path from an empty
`go.mod` to a tested, production-shaped data layer, written for the
library as it exists today and verified against its source code.

## Who this book is for

You should be comfortable with Go (structs, generics, interfaces, the
standard library basics) and with relational databases in general: what a
primary key is, what a transaction guarantees, roughly how an index
speeds up a query. No prior experience with pg, or with pgx, is assumed.
If you have used `database/sql` directly, or another Go database library,
you will recognize the shape of the problems pg solves; the book takes
care to explain not just what an API does but why it is shaped the way it
is, since that is usually the part a reference alone leaves out.

## How the book is organized

The chapters build on one another and are best read in order the first
time; afterwards each stands alone as a reference.

- **Chapters 1–3** are the foundation: installing pg, declaring a schema
  with struct tags, and opening and configuring a connection pool.
- **Chapters 4–7** cover everyday data access: the repository pattern for
  CRUD, querying and scanning rows, filtering and pagination with the
  `Where` builder, and writing data with inserts, updates and upserts.
- **Chapters 8–10** cover higher-throughput and failure-handling paths:
  bulk loading and streaming with `COPY`, transactions, and the error
  types pg returns so you can branch on a constraint violation instead of
  matching an error string.
- **Chapters 11–13** cover the database itself: schema management and
  migrations, LISTEN/NOTIFY, and introspecting a live database or
  generating Go code from one.
- **Chapters 14–16** are about running pg in production: security,
  observability and operations, and testing with the `pgtest` package. An
  epilogue closes the book with what to build next and where to go for
  updates.

## Conventions

Code examples use the full import path (`github.com/kataras/pg`) and are
complete enough to adapt directly into a real program; where an example
belongs to a larger program, the chapter says so. Shell commands are
written for a POSIX shell and work in PowerShell unless noted. The
examples target Go 1.26, the version pg's own go.mod requires, and are
written against the library as published in this book's edition; every
identifier in every listing was checked against the source before the
chapter shipped.

Each chapter closes with a **Further Reading** list of canonical
references: the PostgreSQL documentation, the pgx documentation, and
relevant parts of go.dev, for going deeper than a single chapter can.
Some chapters open with a **Background** section on the underlying
concept (what a connection pool does, what `LISTEN`/`NOTIFY` guarantees)
for readers new to that piece of PostgreSQL specifically; where a chapter
has one, its introduction says where to skip to if you already know the
fundamentals.

## About pg

pg has been developed by Gerasimos Maropoulos
([@kataras](https://github.com/kataras)), the author of the Iris web
framework, and is released under the MIT license. The library lives at
[github.com/kataras/pg](https://github.com/kataras/pg), where issues,
discussions and contributions are welcome; runnable programs beyond the
ones in this book live in its `_examples` directory. This book is
maintained separately from the library, and corrections to it are
welcome the same way.
