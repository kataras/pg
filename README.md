# kataras/pg

[![build status](https://img.shields.io/github/actions/workflow/status/kataras/pg/ci.yml?branch=main&style=for-the-badge)](https://github.com/kataras/pg/actions/workflows/ci.yml)  [![report card](https://img.shields.io/badge/report%20card-a%2B-ff3333.svg?style=for-the-badge)](https://goreportcard.com/report/github.com/kataras/pg) [![godocs](https://img.shields.io/badge/go-%20docs-488AC7.svg?style=for-the-badge)](https://pkg.go.dev/github.com/kataras/pg/@main) [![view examples](https://img.shields.io/badge/examples%20-a83adf.svg?style=for-the-badge&logo=go)](https://github.com/kataras/pg/tree/main/_examples)

<img align="left" src="https://www.iris-go.com/images/pg_logo.png">

A high-performance Go library that provides a simple and elegant way to interact with PostgreSQL databases. It allows you to define your entities as structs with pg tags, register them in a schema, and perform CRUD operations using a repository pattern. It also handles database connection, schema creation and verification, and query generation and execution. You can use PG to write concise and readable code that works with PostgreSQL databases.

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
$ go mod tidy -compat=1.20 # -compat="1.20" for windows.
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
| Time | time, timetz, time without time zone |
| Timestamp | timestamp, timestamptz |
| TsQuery | tsquery |
| TsVector | tsvector |
| TxIDSnapshot | txid_snapshot |
| UUID | uuid |
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

## 📦 3rd-party Packages

List of 3rd-party packages based on `PG`.

* Iris Web Framework PostgreSQL Database Middleware: <https://github.com/iris-contrib/middleware/tree/master/pg>

## 🛡 Security Vulnerabilities

If you discover a security vulnerability within pg, please send an e-mail to [kataras2006@hotmail.com](mailto:kataras2006@hotmail.com). All security vulnerabilities will be promptly addressed.

## 📝 License

This project is licensed under the [MIT license](LICENSE).