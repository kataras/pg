# gen

The gen package provides a function to generate Go schema files from a PostgreSQL database.

## Usage

To use the gen package, you need to import it in your Go code:

```go
import "github.com/kataras/pg/gen"
```

The main function of the gen package is `GenerateSchemaFromDatabase`, which takes a context, an `ImportOptions` struct and an `ExportOptions` struct as arguments. The `ImportOptions` struct contains the connection string and the list of tables to import from the database. The `ExportOptions` struct contains the root directory and the file name generator for the schema files.

For example, this code snippet shows how to generate schema files for all tables in a test database:

```go
package main

import (
  "context"
  "fmt"
  "os"
  "time"

  "github.com/kataras/pg/gen"
)

func main() {
  rootDir := "./_testdata"

  i := gen.ImportOptions{
    ConnString: "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable",
  }

  e := gen.ExportOptions{
    RootDir: rootDir,
  }

  if err := gen.GenerateSchemaFromDatabase(context.Background(), i, e); err != nil {
    fmt.Println(err.Error())
    return
  }

  fmt.Println("OK")
}
```

The `GenerateSchemaFromDatabase` function will create a directory named `_testdata` and write schema files for each table in the test database. The schema files will have the same name as the table name (on its singular form), with a `.go` extension.

You can also customize the import and export options by using the fields of the `ImportOptions` and `ExportOptions` structs. For example, you can use the `ListTables.Filter` field to filter out some tables or columns, or use the `GetFileName` field to change how the schema files are named.

For more details on how to use the gen package, please refer to the [godoc](https://pkg.go.dev/github.com/kataras/pg/gen) documentation.

## License

The gen package is licensed under the MIT license. See [LICENSE](https://github.com/kataras/pg/blob/main/LICENSE) for more information.