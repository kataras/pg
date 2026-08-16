# kataras/pg

[![build status](https://img.shields.io/github/actions/workflow/status/kataras/pg/ci.yml?branch=main&style=for-the-badge)](https://github.com/kataras/pg/actions/workflows/ci.yml)  [![report card](https://img.shields.io/badge/report%20card-a%2B-ff3333.svg?style=for-the-badge)](https://goreportcard.com/report/github.com/kataras/pg) [![godocs](https://img.shields.io/badge/go-%20docs-488AC7.svg?style=for-the-badge)](https://pkg.go.dev/github.com/kataras/pg/@main) [![view examples](https://img.shields.io/badge/examples%20-a83adf.svg?style=for-the-badge&logo=go)](https://github.com/kataras/pg/tree/main/_examples)

<img align="left" width="72" height="72" src="book/output/brand/pg-mark-256.png" alt="pg">

A Go library for PostgreSQL (16.x). You declare entities as structs with `pg` tags, register them in a schema, and perform CRUD operations through a repository pattern. It also handles database connections, schema creation and verification, and query generation and execution, so you write concise, readable code against PostgreSQL.

<br/>

> 🤖 **Ask AI about pg.** Google's free [Code Wiki for pg](https://codewiki.google/github.com/kataras/pg) is generated from this repository and stays in sync with it, commit by commit. Browse the architecture with diagrams, or ask its chat plain-language questions about the codebase and get answers grounded in the actual source. For an assistant that writes pg code with you, [Plexon AI](https://plexon.ai), the desktop assistant built by the author of pg, ships the [pg skill](skill/) built in; see [AI Tools for pg](#-ai-tools-for-pg) below.

## 📚 The pg Book

A preface, 16 chapters and an epilogue, every code example verified against the library source. Each chapter opens with a plain-language background for newcomers and closes with curated further reading, and the book explains not just what each API does but why it is shaped the way it is.

**[Read the PDF](book/output/pg-book.pdf)** (165 pages), or start with the [preface](book/README.md) and read the chapters as Markdown:

| # | Chapter | # | Chapter |
| --- | --- | --- | --- |
| 1 | [Getting Started](book/01-getting-started.md) | 9 | [Transactions](book/09-transactions.md) |
| 2 | [Schema and Struct Tags](book/02-schema-and-struct-tags.md) | 10 | [Errors](book/10-errors.md) |
| 3 | [Connections and Configuration](book/03-connections-and-configuration.md) | 11 | [Schema Management and Migrations](book/11-schema-management-and-migrations.md) |
| 4 | [Repositories and CRUD](book/04-repositories-and-crud.md) | 12 | [LISTEN and NOTIFY](book/12-listen-notify.md) |
| 5 | [Querying and Scanning](book/05-querying-and-scanning.md) | 13 | [Introspection and Code Generation](book/13-introspection-and-code-generation.md) |
| 6 | [Filtering and Pagination](book/06-filtering-and-pagination.md) | 14 | [Security](book/14-security.md) |
| 7 | [Writing Data](book/07-writing-data.md) | 15 | [Observability and Operations](book/15-observability-and-operations.md) |
| 8 | [Bulk Loading and Streaming](book/08-bulk-loading-and-streaming.md) | 16 | [Testing](book/16-testing.md) |

The book is rebuilt from its own module: `cd book && go run .`. See [book/README_EBOOK.md](book/README_EBOOK.md).

<br/>

## 💻 Installation

The only requirement is the [Go Programming Language](https://go.dev/dl/).

### Create a new project

```sh
$ mkdir myapp
$ cd myapp
$ go mod init myapp
$ go get github.com/kataras/pg@latest
```

<details><summary>Install on existing project</summary>

```sh
$ cd myapp
$ go get github.com/kataras/pg@latest
```

**Run**

```sh
$ go mod tidy
$ go run .
```

</details>

<br/>

## 📖 Example

PG contains extensive and thorough **[documentation](https://pkg.go.dev/github.com/kataras/pg@vlatest)** making it easy to get started with the library.

```go
package main

import (
  "context"
  "fmt"
  "log"
  "time"

  "github.com/kataras/pg"
)

// Base is a struct that contains common fields for all entities.
type Base struct {
  ID        string    `pg:"type=uuid,primary"` // UUID as primary key
  CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"` // Timestamp of creation
  UpdatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"` // Last update
}

// Customer is a struct that represents a customer entity.
type Customer struct {
  Base // Embed Base struct

  Firstname string `pg:"type=varchar(255)"` // First name of the customer
}

func main() {
  // Default value for struct field tag `pg`.
  // It can be modified to a custom one as well, e.g.
  // pg.SetDefaultTag("db")

  // Create Schema instance.
  schema := pg.NewSchema()
  // First argument is the table name, second is the struct entity.
  schema.MustRegister("customers", Customer{})

  // Create Database instance.
  // sslmode=disable is for local development only; prefer sslmode=verify-full in production.
  connString := "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable"
  db, err := pg.Open(context.Background(), schema, connString)
  if err != nil {
    log.Fatal(err)
  }
  defer db.Close()

  // If needed, create and verify the database schema
  // based on the pg tags of the structs.
  //
  // Alternatively, you can generate
  // Go schema files from an existing database:
  // see the ./gen sub-package for more details.
  if err = db.CreateSchema(context.Background()); err != nil {
    log.Fatal(err)
  }

  if err = db.CheckSchema(context.Background()); err != nil {
    log.Fatal(err)
  }

  // Create a Repository of Customer type.
  customers := pg.NewRepository[Customer](db)

  var newCustomer = Customer{
    Firstname: John,
  }

  // Insert a new Customer.
  err = customers.InsertSingle(context.Background(), newCustomer, &newCustomer.ID)
  if err != nil {
    log.Fatal(err)
  }

  // Get by id.
  /*
  query := `SELECT * FROM customers WHERE id = $1 LIMIT 1;`
  existing, err := customers.SelectSingle(context.Background(), query, newCustomer.ID)
  OR:
  */
  existing, err := customers.SelectByID(context.Background(), newCustomer.ID)
  if err != nil {
    log.Fatal(err)
  }

  log.Printf("Existing Customer (SelectSingle):\n%#+v\n", existing)

  // List all.
  query = `SELECT * FROM customers ORDER BY created_at DESC;`
  allCustomers, err := customers.Select(context.Background(), query)
  if err != nil {
    log.Fatal(err)
  }

  log.Printf("All Customers (%d):", len(allCustomers))
  for _, customer := range allCustomers {
    fmt.Printf("- (%s) %s\n", customer.ID, customer.Firstname)
  }
}
```

 > If you already have a database, you can use the [gen](./gen) sub-package to create structs that match its schema.

## ✒️ ASCII art

```
┌───────────────────────┐
│  NewSchema() *Schema  ├───────────────────────────────────┐
├───────────────────────┘                                   │
│                                                           │
├─────────────────────────────────────────────────────┐     │
│  Schema                                             │     │
├─────────────────────────────────────────────────────┤     │
│  - MustRegister(tableName string, emptyStruct any)  │     │
└─────────────────────────────────────────────────────┘     │
                                                            │
                                                            │
                                                            │
                                ┌───────────────────────────┘     ┌─────────────────────────┐
                                │                                 │                         │
┌───────────────────────────────▼─────────────────────────────────┴───────────┐             │
│  Open(ctx context.Context, schema *Schema, connString string) (*DB, error)  │             │
├─────────────────────────────────────────────────────────────────────────────┘             │
│                                                                                           │
├─────────────────────────────────────────────────────────────────────────────────────┐     │
│  DB                                                                                 │     │
├─────────────────────────────────────────────────────────────────────────────────────┤     │
│                                                                                     │     │
│  - CreateSchema(ctx context.Context) error                                          │     │
│  - CheckSchema(ctx context.Context) error                                           │     │
│                                                                                     │     │
│  - InTransaction(ctx context.Context, fn (*DB) error) error                         │     │
│  - IsTransaction() bool                                                             │     │
│                                                                                     │     │
│  - Query(ctx context.Context, query string, args ...any) (Rows, error)              │     │
│  - QueryRow(ctx context.Context, query string, args ...any) Row                     │     │
│                                                                                     │     │
│  - Exec(ctx context.Context, query string, args... any) (pgconn.CommandTag, error)  │     │
│                                                                                     │     │
│  - Listen(ctx context.Context, channel string) (*Listener, error)                   │     │
│  - Notify(ctx context.Context, channel string, payload any) error                   │     │
│  - Unlisten(ctx context.Context, channel string) error                              │     │
│                                                                                     │     │
│  - Close() error                                                                    │     │
└─────────────────────────────────────────────────────────────────────────────────────┘     │
                                                                                            │
                                                                                            │
                                                                                            │
                                                                                            │
                      ┌─────────────────────────────────────────────────────────────────────┘
                      │
┌─────────────────────▼─────────────────────┐
│  NewRepository[T](db *DB) *Repository[T]  │
├───────────────────────────────────────────┘
│
├────────────────────────────────────────────────────────────────────────────────────────────┐
│  Repository[T]                                                                             │
├────────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                            │
│  - InTransaction(ctx context.Context, fn func(*Repository[T]) error) error                 │
│  - IsTransaction() bool                                                                    │
│                                                                                            │
│  - Select(ctx context.Context, query string, args ...any) ([]T, error)                     │
│  - SelectSingle(ctx context.Context, query string, args ...any) (T, error)                 │
│  - SelectByID(ctx context.Context, id any) (T, error)                                      │
│  - SelectByUsernameAndPassword(ctx context.Context, username, plainPwd string) (T, error)  │
│                                                                                            │
│  - Insert(ctx context.Context, values ...T) error                                          │
│  - InsertSingle(ctx context.Context, value T, destIdPtr any) error                         │
│                                                                                            │
│  - Update(ctx context.Context, values ...T) (int64, error)                                 │
│  - UpdateOnlyColumns(ctx context.Context, columns []string, values ...T) (int64, error)    │
│                                                                                            │
│  - Upsert(ctx context.Context, uniqueIndex string, values ...T) error                      │
│  - UpsertSingle(ctx context.Context, uniqueIndex string, value T, destIdPtr any) error     │
│                                                                                            │
│  - Delete(ctx context.Context, values ...T) (int64, error)                                 │
│                                                                                            │
│  - ListenTable(ctx context.Context, cb func(notification, error) error) (Closer, error)    │
└────────────────────────────────────────────────────────────────────────────────────────────┘
```

## 🛄 Data types

| PostgreSQL data type                  | Struct field tag `type` options |
| ------------------------------------- | ------------------------| 
| BigInt | bigint, int8 |
| BigIntArray | bigint[], int8[] |
| BigSerial | bigserial, serial8 |
| Bit | bit |
| BitVarying | bit varying, varbit | 
| Boolean | boolean, bool |
| Box | box | 
| Bytea | bytea |
| Character | character, char |
| CharacterArray | character[], char[] |
| CharacterVarying | character varying, varchar |
| CharacterVaryingArray | character varying[], varchar[] |
| Cidr | cidr |
| Circle | circle |
| Date | date |
| DoublePrecision | double precision, float8 |
| Inet | inet |
| Integer | integer, int, int4 |
| IntegerArray | integer[], int[], int4[] |
| IntegerDoubleArray | integer[][], int[][], int4[][] |
| Interval | interval |
| JSON | json |
| JSONB | jsonb |
| Line | line |
| Lseg | lseg |
| MACAddr | macaddr |
| MACAddr8 | macaddr8 |
| Money | money |
| Numeric | numeric, decimal |
| Path | path |
| PgLSN | pg_lsn |
| Point | point  |
| Polygon | polygon |
| Real | real, float4 |
| SmallInt | smallint, int2 |
| SmallSerial | smallserial, serial2 |
| Serial | serial4 |
| Text | text |
| TextArray | text[] |
| TextDoubleArray | text[][] |
| Time | time, time without time zone |
| TimeTZ |timetz, time with time zone  |
| Timestamp | timestamp, timestamp without time zone |
| TimestampTZ | timestamptz, timestamp with time zone |
| TsQuery | tsquery |
| TsVector | tsvector |
| TxIDSnapshot | txid_snapshot |
| UUID | uuid |
| UUIDArray | uuid[] |
| XML | xml |
| Int4Range | int4range |
| Int4MultiRange | int4multirange |
| Int8Range | int8range |
| Int8MultiRange | int8multirange |
| NumRange | numrange |
| NumMultiRange | nummultirange |
| TsRange | tsrange |
| TsMultirange | tsmultirange |
| TsTzRange | tstzrange |
| TsTzMultiRange | tstzmultirange |
| DateRange | daterange |
| DateMultiRange | datemultirange |
| CIText | citext |
| HStore | hstore |

### Data type examples

UUID

```go
type Entity struct {
  ID string `pg:"type=uuid,primary"`
}
```

Timestamp

```go
type Entity struct {
  CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
}
```

Varchar

```go
type Entity struct {
  PhotoURL string `pg:"type=varchar(255)"`
}
```

Varchar Array

```go
type Entity struct {
  SearchTerms []string `pg:"type=varchar[]"`
}
```

Integer

```go
type Entity struct {
  ReadTimeMinutes int `pg:"type=smallint,default=1,check=read_time_minutes > 0"`
}
```

Custom JSON Object

```go
type Entity struct {
  Feature Feature `pg:"type=jsonb"`
}
```

Array of custom JSON objects

```go
type Entity struct {
  Tags []Tag `pg:"type=jsonb"`
}
```

## 🧰 Query helpers

`PG` ships a set of small, composable helpers on top of `*DB`/`Repository[T]` for the query
patterns every service ends up hand-writing: dynamic `WHERE` clauses, pagination, ad-hoc read
models, and table-name-based CRUD for generic code.

### WHERE builder (`Conditions`)

`pg.Where` starts a `Conditions` builder from raw SQL fragments. Each fragment keeps its own,
call-local `$1..$n` placeholders, which `Build` renumbers into one consecutive, global sequence
when it renders the final clause, so the very same `Conditions` value can drive both a list
query and its `COUNT` twin without hand-renumbering placeholders a second time:

```go
where := pg.Where("").
  AndIf(status != "", "status = $1", status).
  AndMin("age", minAge)

clause, args := where.Build(1)

total, err := repo.Count(ctx, "SELECT COUNT(*) FROM customers WHERE "+clause, args...)
if err != nil || total == 0 {
  return nil, total, err
}

clause, args = where.Build(1) // same clause/args, renumbered identically for the page query.
items, err := repo.Select(ctx,
  "SELECT * FROM customers WHERE "+clause+" ORDER BY created_at DESC LIMIT 20", args...)
```

`Conditions` also has `AndAnyOf`/`AndMatchAnyOf` (array-membership filters), `AndNameMatchAnyOf`
(multi-name search), `AndSearch` (`ILIKE`/full-text search) and `AndOptionalEq` (zero-gated
equality) for the other common filter shapes: see their godoc for the exact SQL each renders.
Column/type names embedded in a fragment are developer-authored SQL, never sanitized: don't
splice user input into the fragment itself, only into the args alongside it.

> Tip: column names in a fragment can come from the [gen](./gen) sub-package's generated,
> type-safe column constants instead of hand-typed strings, e.g.
> `pg.Where(definition.Customer.Status.String()+" = $1", status)`.

### Ordering and pagination

`Repository.OrderBy` validates a caller-supplied sort column against the table's own columns
(falling back to `created_at`, then `updated_at`, then the primary key, when empty) and returns a
quoted, injection-safe `ORDER BY` fragment. `SelectPaginated` takes that fragment together with a
`Limit`/`Offset` and returns a page of rows plus the total row count in one call:

```go
orderBy, err := repo.OrderBy(sortColumn, descending) // e.g. sortColumn from a query param.
if err != nil {
  return nil, 0, err
}

items, total, err := repo.SelectPaginated(ctx, pg.PageOptions{
  Limit:   20,
  Offset:  page * 20,
  OrderBy: orderBy,
}, "SELECT * FROM customers WHERE status = $1", status)
```

`PageOptions.OrderBy` is interpolated, not bound (PostgreSQL has no bind-parameter form for a
dynamic identifier), which is exactly why it must come from `Repository.OrderBy` (or a trusted
literal), never directly from unvalidated user input. Set `PageOptions.WithoutTotal` to skip the
derived `COUNT` query (the returned total is then `-1`); for a query whose `SELECT` list already
carries a `COUNT(*) OVER()` column, use `Repository.SelectWithTotal` instead: that column must be
aliased exactly `total_count` (the literal name `SelectWithTotal` passes to
`desc.RowsToStructWithTotal`), or the total silently comes back as zero.

### Ad-hoc read models (`QueryStructs`)

`QueryStructs`/`QueryStruct` scan query results into a struct that was never registered in the
`Schema`, handy for joins and other presenter-style reads that don't map to a single table.
Struct/map/slice fields decode automatically from a JSON/JSONB projection such as `to_jsonb(...)`:

```go
type OrderWithCustomer struct {
  ID       int64
  Total    float64
  Customer *Customer // populated from a `to_jsonb(c.*) AS customer` projection.
}

rows, err := pg.QueryStructs[OrderWithCustomer](ctx, db, `
  SELECT o.id, o.total, to_jsonb(c.*) AS customer
  FROM orders o JOIN customers c ON c.id = o.customer_id`)
```

### `QueryMap`, `QueryFunc` and `Count`

```go
idsByEmail, err := pg.QueryMap[string, string](ctx, db, "SELECT email, id FROM customers;")

type nameAndCount struct {
  Name  string
  Count int64
}
rows, err := pg.QueryFunc(ctx, db, func(rows pg.Rows) (nameAndCount, error) {
  var nc nameAndCount
  err := rows.Scan(&nc.Name, &nc.Count)
  return nc, err
}, "SELECT name, COUNT(*) FROM customers GROUP BY name;")

total, err := db.Count(ctx, "SELECT COUNT(*) FROM customers WHERE status = $1", "active")
```

### Table-name CRUD (`*DB`)

For generic, table-agnostic code that only has a table name (not a typed `Repository[T]`), `*DB`
exposes a small, schema-validated CRUD surface: every table and column name is resolved against
the registered `Schema` before it ever reaches SQL, so an unknown name returns a descriptive error
instead of being concatenated into a query:

```go
removed, err := db.DeleteBy(ctx, "customers", "status", "banned") // WHERE status = $1
exists, err := db.ExistsBy(ctx, "customers", "email", "a@b.com")  // SELECT EXISTS(...)
count, err := db.CountBy(ctx, "customers")                        // no pairs: counts the whole table

var customer Customer
err = db.SelectSingle(ctx, &customer, "SELECT * FROM customers WHERE id = $1", id)
```

`colValPairs` is a flat `"col1", v1, "col2", v2, ...` list, ANDed together; `DeleteByID` covers the
common by-primary-key case.

### Typed transactions (`pg.InTransaction[R]`)

Package-level `InTransaction[R]` removes the boilerplate a hand-written repository wrapper type
otherwise repeats for every wrapper: open a transaction on the underlying `*DB`, rebuild the
wrapper around it, then call the caller's function with that rebuilt wrapper.

```go
type CustomerRepository struct {
  *pg.Repository[Customer]
}

func NewCustomerRepository(db *pg.DB) *CustomerRepository {
  return &CustomerRepository{pg.NewRepository[Customer](db)}
}

func (r *CustomerRepository) InTransaction(ctx context.Context, fn func(*CustomerRepository) error) error {
  return pg.InTransaction(ctx, r.DB(), NewCustomerRepository, fn)
}
```

### `ExecMany` and `SetConstraintsDeferred`

```go
err := db.InTransaction(ctx, func(tx *pg.DB) error {
  if err := tx.SetConstraintsDeferred(ctx); err != nil {
    return err
  }

  return tx.ExecMany(ctx,
    `INSERT INTO parents (id) VALUES (1)`,
    `INSERT INTO children (parent_id) VALUES (1)`,
  )
})
```

`SetConstraintsDeferred` defers every deferrable constraint (or just the named ones) for the
remainder of the current transaction. It errors when called outside one. `ExecMany` runs each
statement in order inside a single transaction (joining the current one, as above, or opening its
own), sending one `Exec` at a time, since the extended protocol cannot prepare a multi-statement
string.

### Typed constraint errors (`ConstraintError`)

```go
err := repo.InsertSingle(ctx, customer, &customer.ID)
if cerr, ok := pg.AsConstraintError(err); ok {
  switch cerr.Kind {
  case pg.ConstraintUnique:
    http.Error(w, fmt.Sprintf("%s already exists", cerr.ConstraintName), http.StatusConflict)
  case pg.ConstraintForeignKey:
    http.Error(w, "referenced row does not exist", http.StatusUnprocessableEntity)
  case pg.ConstraintNotNull, pg.ConstraintCheck:
    http.Error(w, cerr.Error(), http.StatusBadRequest)
  default:
    http.Error(w, "constraint violation", http.StatusConflict)
  }
  return
}
```

`AsConstraintError` extracts a typed view (`Kind`, `ConstraintName`, `TableName`, `ColumnName`,
`Detail`, `Code`) out of a PostgreSQL SQLSTATE-23 integrity-constraint violation, so an HTTP
handler (or any other layer) can map it to a response without parsing error text itself; it is
extraction-only, `pg` never wraps its own returned errors in one.

### `MutateSingle`

```go
ok, err := repo.MutateSingle(ctx, "UPDATE customers SET status = $1 WHERE id = $2", "archived", id)
if err != nil {
  return err
}
if !ok {
  return pg.ErrNoRows
}
```

Prefer this over the older `tag, err := repo.Exec(...); tag.RowsAffected() > 0` pattern -
`MutateSingle` (and `Mutate`, for the raw affected-row count) does the `Exec`-then-check in one call.

### Bulk loading

`InsertMany`/`UpsertMany` (used automatically by `Insert`/`Upsert` for more than one value) and
`Repository.CopyFrom` both load many rows at once, but they trade off differently:

| | `InsertMany` / `UpsertMany` | `Repository.CopyFrom` |
| --- | --- | --- |
| Protocol | batched multi-row `INSERT` (`desc.DefaultInsertBatchSize` = 500 rows/batch, shrunk automatically for wide tables) | PostgreSQL `COPY ... FROM STDIN` |
| Speed | fast | fastest, for large, conflict-free loads |
| `ON CONFLICT` / upsert | yes (`UpsertMany`) | no |
| `RETURNING` | yes, via the single-row `InsertSingle`/`UpsertSingle` path | no |
| Per-row `DEFAULT` | a zero-valued field on a defaulted column emits `DEFAULT` for that row | per-**column**, not per row: a column is either present (every row's literal value is stored, zero values included) or entirely omitted (`DEFAULT` fires for every row): see `desc.CopyPlan` |
| Bind-parameter ceiling | capped at PostgreSQL's 65535/statement (batch size shrinks automatically) | none |
| All-or-nothing | one failing batch rolls back the whole call (single transaction) | the whole `COPY` succeeds in full, or nothing is stored |

```go
n, err := repo.CopyFrom(ctx, customers) // fastest path for a large, one-shot, conflict-free import.
```

`CopyFrom` returns `ErrIsReadOnly` for a read-only repository, and `desc.ErrCopyPassword` for a
table whose password column is hashed in the database (no Go-side `desc.PasswordHandler`
installed via `Schema.HandlePassword`). COPY cannot invoke a SQL function per row the way
`INSERT` can.

## 📤 Streaming queries

`SelectIter`/`QueryIter` return a lazy `iter.Seq2[T, error]` that decodes one row at a time instead
of materializing the whole result as a slice. Use them for exports and large scans where a full
`[]T` wouldn't comfortably fit in memory:

```go
for customer, err := range repo.SelectIter(ctx, "SELECT * FROM customers WHERE status = $1", "active") {
  if err != nil {
    return err
  }

  if customer.Email == target {
    break // releases the connection back to the pool immediately.
  }

  process(customer)
}
```

`pg.QueryIter[T]` is the same idea for single-column rows (the streaming analog of `QuerySlice`).
Breaking out of the loop early closes the underlying rows and releases the connection right away,
so the caller can immediately issue another query on the same `*DB` with no extra cleanup.

## 🔁 Retrying transactions

`InTransactionRetry` (on both `*DB` and `Repository[T]`) retries a transaction, with exponential
backoff and full jitter, when it fails on a transient SQLSTATE: `40001` (serialization failure) or
`40P01` (deadlock detected) by default. Pair it with `pgx.Serializable` to get the isolation level
under which those errors are actually raised:

```go
err := db.InTransactionRetry(ctx, pg.RetryOptions{
  TxOptions: pgx.TxOptions{IsoLevel: pgx.Serializable},
}, func(tx *pg.DB) error {
  // ... statements that might race another transaction under SERIALIZABLE ...
  return nil
})
```

Every attempt runs in its own, brand-new transaction, so `fn` should re-read whatever it needs from
the database on each call rather than assuming it only runs once.

## 🩺 Health checks

```go
func healthHandler(db *pg.DB) http.HandlerFunc {
  return func(w http.ResponseWriter, r *http.Request) {
    health, err := db.Health(r.Context())
    if err != nil {
      http.Error(w, err.Error(), http.StatusServiceUnavailable)
      return
    }

    json.NewEncoder(w).Encode(health) // {"server_version": "...", "pool": {...}}
  }
}
```

`db.Health` pings the pool and reports the server version together with pool statistics; use the
plain `db.Ping(ctx)` instead for a minimal liveness check. Both always check the connection pool
directly, even when called on a transaction-scoped `*DB`, so neither ever touches or invalidates an
in-flight transaction.

## 🔭 Observability

`WithLoggerLevel` is `WithLogger` with a caller-chosen `tracelog.LogLevel`, instead of `WithLogger`'s
hardcoded, very verbose `tracelog.LogLevelTrace` (which logs every bind argument, passwords
included):

```go
db, err := pg.Open(ctx, schema, connString,
  pg.WithLoggerLevel(logger, tracelog.LogLevelWarn),
)
```

`WithQueryTracer` composes one or more `pgx.QueryTracer` implementations into the pool, for
example an OpenTelemetry tracer such as [otelpgx](https://github.com/exaring/otelpgx). `pg` does
not depend on otelpgx (or on OpenTelemetry at all); it is a separate module a caller adds to their
own `go.mod`:

```go
db, err := pg.Open(ctx, schema, connString,
  pg.WithLoggerLevel(logger, tracelog.LogLevelWarn), // must come BEFORE WithQueryTracer, see below.
  pg.WithQueryTracer(otelpgx.NewTracer()),
)
```

`WithLogger`/`WithLoggerLevel` both overwrite the tracer slot outright rather than composing with
it, so when combining query tracing with logging, always pass `WithLogger`/`WithLoggerLevel` BEFORE
`WithQueryTracer` in the `Open` call. The reverse order silently drops the tracer(s).

## 🔌 PgBouncer / pooler compatibility

For a PgBouncer deployment in transaction-pooling mode, prepared statements can't be reused across
pooled connections, so disable pgx's statement caching and switch to the simple protocol:

```go
db, err := pg.Open(ctx, schema, connString,
  pg.WithDefaultQueryExecMode(pgx.QueryExecModeSimpleProtocol),
  pg.WithStatementCacheCapacity(0),
  pg.WithDescriptionCacheCapacity(0),
)
```

The same settings can instead be passed as connection-string parameters, with no `ConnectionOption`
needed:

```
postgres://user:pass@pgbouncer-host:6432/mydb?sslmode=verify-full&default_query_exec_mode=simple_protocol&statement_cache_capacity=0&description_cache_capacity=0
```

## 🗄️ Migrations

`DB.Migrate` applies the not-yet-applied `.sql` files found in an `fs.FS` (typically an
`embed.FS`), in ascending filename order, inside a single transaction guarded by a Postgres
advisory lock, so several instances of an application starting up concurrently never double-apply
the same file:

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

fsys, err := fs.Sub(migrationsFS, "migrations") // opts.Pattern ("*.sql") matches filenames directly.
if err != nil {
  return err
}

applied, err := db.Migrate(ctx, fsys, nil) // nil opts: "schema_migrations" table, "*.sql" pattern.
if err != nil {
  return err
}
log.Printf("applied %d migration(s): %v", len(applied), applied)
```

This is deliberately the smallest honest migration runner: no down/rollback direction, no checksum
verification, no out-of-order detection. Reach for
[golang-migrate/migrate](https://github.com/golang-migrate/migrate) or
[pressly/goose](https://github.com/pressly/goose) if you need any of that.

## 🧪 Testing

The [pgtest](./pgtest) sub-package gives each test its own randomly named, ephemeral PostgreSQL
schema against a real server (including a CI service container), dropped automatically once the
test finishes, so tests can run concurrently, even across packages, without stepping on each
other's tables:

```go
func TestWidgetRepository(t *testing.T) {
  connString := pgtest.ConnString(t) // skips the test if PG_CONNSTRING is unset.

  schema := pg.NewSchema()
  schema.MustRegister("widgets", Widget{})

  db := pgtest.New(t, schema, connString) // fresh, isolated schema; dropped on cleanup.
  repo := pg.NewRepository[Widget](db)

  // ... use repo; the ephemeral schema is dropped when the test finishes.
}
```

Build a fresh `*pg.Schema` inside each test (as above) rather than sharing one across tests -
`pgtest.New` mutates the schema's tables in place, so a second, still-overlapping `New` call given
the same `*pg.Schema` fails the test immediately instead of racing.

## ❔ Optional (NULL) parameters

`Ptr` and `NullIfZero` turn a plain value into the pointer form a query argument needs. pgx v5
encodes a nil typed pointer as SQL `NULL` natively, no `pgtype` wrapper required:

```go
_, err := db.Exec(ctx, "UPDATE customers SET nickname = $1 WHERE id = $2", pg.Ptr("bob"), id)

// NullIfZero binds SQL NULL when the value is the zero value of its type (e.g. "" or 0).
_, err = db.Exec(ctx, "UPDATE customers SET referrer_id = $1 WHERE id = $2", pg.NullIfZero(referrerID), id)
```

## 🤖 AI Tools for pg

Two AI tools know this library specifically, beyond what a general model knows about Go and PostgreSQL.

[Plexon AI](https://plexon.ai) is a desktop assistant for Windows, macOS and Linux, built by the author of this library, and it is the recommended way to write pg code with an assistant. It ships the pg skill: the struct-tag vocabulary, the `Repository[T]` and `*DB` surface with real signatures, the WHERE and pagination builders, transactions and retries, COPY, migrations and the security rules, in reference documents the assistant loads on demand while it writes your code. Turn the skill on from the Skills panel, or install the [Software Developer persona](https://plexon.ai/personas/software-developer/), which enables it alongside the rest of its engineering tooling. [Download Plexon AI](https://plexon.ai/download/).

The [live AI wiki](https://codewiki.google/github.com/kataras/pg) is free and runs in your browser: browse the architecture with diagrams, or ask its chat about the codebase.

Using Claude Code? This repository is a plugin marketplace, so the CLI installs the same skill and keeps it updated:

```sh
claude plugin marketplace add kataras/pg
claude plugin install pg@kataras-pg
```

Codex, Cursor, Copilot and anything else that reads Markdown instructions can vendor [`skill/pg/`](skill/) and point at `skill/pg/SKILL.md`. See [skill/README.md](skill/README.md) for every installation route.

## 📦 3rd-party Packages

List of 3rd-party packages based on `PG`.

* Iris Web Framework PostgreSQL Database Middleware: <https://github.com/iris-contrib/middleware/tree/main/pg>

## 🛡 Security Vulnerabilities

If you discover a security vulnerability within pg, please send an e-mail to [contact@hellenic.dev](mailto:contact@hellenic.dev). All security vulnerabilities will be promptly addressed.

## 📝 License

This project is licensed under the [MIT license](LICENSE).