# Examples

This folder contains some examples for the `PG` package.

- [Basic](./basic/main.go)
- [Logging](./logging/main.go)
- [Password](./password/main.go)
- [Presenter](./presenter/main.go)
- [View](./view/main.go)

The document below describes some basic principles of the package.

## Basic Example

This example shows how to use pg to perform basic CRUD operations on a single table.

### Model

The model is a struct that represents a customer entity with an id and a firstname.

```go
type Customer struct {
	ID        string    `pg:"type=uuid,primary"`
	CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
	UpdatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
	Firstname string    `pg:"type=varchar(255)"`
}
```

### Schema

The schema is an instance of `pg.Schema` that registers the model and its table name.

```go
schema := pg.NewSchema()
schema.MustRegister("customers", Customer{})
```

### Database

The database is an instance of `pg.DB` that connects to the PostgreSQL server using the connection string and the schema.

```go
connString := "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable"
db, err := pg.Open(context.Background(), schema, connString)
if err != nil {
    panic(err)
}
defer db.Close()
```

### Operations

The operations are methods of `pg.DB` or `pg.Repository` that perform queries on the database using the model.

- To create the tables for the pg.Schema above, use the `db.CreateSchema` method:

```go
err := db.CreateSchema(context.Background())
if err != nil {
    panic(err)
}
```

- To insert a record and bind the result ID, use the `db.InsertSingle` method:

```go
customer := &Customer{
    Firstname: "Alice",
}
err := db.InsertSingle(context.Background(), customer, &customer.ID)
if err != nil {
    panic(err)
}
```

- To insert one or more records, use the `db.Insert` method:

```go
customer := &Customer{
    Firstname: "Alice",
}
err := db.Insert(context.Background(), customer)
if err != nil {
    panic(err)
}
```

- To query a record by primary key, use the `db.SelectByID` method:

```go
var customer Customer
err := db.SelectByID(context.Background(), &customer, "some-uuid")
if err != nil {
    panic(err)
}
fmt.Println(customer.Firstname) // Alice
```

- To update a record, use the `db.Update` method:

```go
customer.Firstname = "Bob"
err := db.Update(context.Background(), customer)
if err != nil {
    panic(err)
}
```

- To delete a record, use the `db.Delete` method:

```go
err := db.Delete(context.Background(), customer)
if err != nil {
    panic(err)
}
```

## Repository Example

This example shows how to use pg to implement the repository pattern for a single table.

### Model

The model is a struct that represents a product entity with an ID, a name, and a price.

```go
type Product struct {
	ID   int64   `pg:"type=int,primary"`
	Name string  `pg:"name"`
	Price float64 `pg:"price"`
}
```

### Schema

The schema is an instance of `pg.Schema` that registers the model and its table name.

```go
schema := pg.NewSchema()
schema.MustRegister("products", Product{})
```

### Database

The database is an instance of `pg.DB` that connects to the PostgreSQL server using the connection string and the schema.

```go
connString := "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable"
db, err := pg.Open(context.Background(), schema, connString)
if err != nil {
    panic(err)
}
defer db.Close()
```

### Repository

The repository is an instance of `pg.Repository[Product]` that provides methods to perform queries on the products table using the model.

```go
products := pg.NewRepository[Product](db)
```

### Operations

- To insert a record, use the `products.InsertSingle` method:

```go
product := &Product{
    Name:  "Laptop",
    Price: 999.99,
}
err := products.InsertSingle(context.Background(), product, &product.ID)
if err != nil {
    panic(err)
}
```

- To query a record by primary key, use the `products.SelectByID` method:

```go
err := products.SelectByID(context.Background(), 1)
if err != nil {
    panic(err)
}
fmt.Println(product.Name) // Laptop
```

- To query multiple records by a condition, use the `products.Select` method:

```go
query := `SELECT * FROM products WHERE price > $1 ORDER BY price DESC;`
products, err := products.Select(context.Background(), query, 500)
if err != nil {
    panic(err)
}
for _, product := range products {
    fmt.Printf("- (%d) %s: $%.2f\n", product.ID, product.Name, product.Price)
}
```

- To update a record, use the `products.Update` method:

```go
product.Price = 899.99
err := products.Update(context.Background(), product)
if err != nil {
    panic(err)
}
```

- To delete a record, use the `products.Delete` method:

```go
err := products.Delete(context.Background(), product)
if err != nil {
    panic(err)
}
```

## Transaction Example

This example shows how to use pg to perform queries within a transaction.

### Model

The model is a struct that represents a customer entity with an id and a firstname.

```go
type Customer struct {
	ID        string    `pg:"type=uuid,primary"`
	CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
	UpdatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
	Firstname string    `pg:"type=varchar(255)"`
}
```

### Schema

The schema is an instance of `pg.Schema` that registers the model and its table name.

```go
schema := pg.NewSchema()
schema.MustRegister("customers", Customer{})
```

### Database

The database is an instance of `pg.DB` that connects to the PostgreSQL server using the connection string and the schema.

```go
connString := "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable"
db, err := pg.Open(context.Background(), schema, connString)
if err != nil {
    panic(err)
}
defer db.Close()
```

### Transaction

The transaction is an instance of `pg.DB` that is created by the `db.InTransaction` method. The `db.InTransaction` method takes a function that receives a `context.Context` and `pg.DB` instance as arguments. You can use the `pg.DB` instance to run queries within the transaction. If the function returns an error, the transaction will be rolled back. Otherwise, the transaction will be committed.

```go
err := db.InTransaction(context.Background(), func(db *pg.DB) error {
    // Run queries within the transaction
    err := db.Insert(context.Background(), customer)
    if err != nil {
        return err
    }
    err := db.Update(context.Background(), customer)
    if err != nil {
        return err
    }
    // Return nil to commit the transaction
    return nil
})
if err != nil {
    panic(err)
}
```
