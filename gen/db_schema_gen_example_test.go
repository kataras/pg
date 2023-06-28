package gen

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/kataras/pg"
)

type Features []Feature

type Feature struct {
	IsFeatured bool `json:"is_featured"`
}

type Tag struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

func ExampleGenerateSchemaFromDatabase() {
	const (
		rootDir = "./_testdata"
	)
	defer func() {
		os.RemoveAll(rootDir)
		time.Sleep(1 * time.Second)
	}()

	i := ImportOptions{
		ConnString: "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable",
		ListTables: pg.ListTablesOptions{
			Filter: pg.TableFilterFunc(func(table *pg.Table) bool {
				columnFilter := func(column *pg.Column) bool {
					columnName := column.Name

					switch table.Name {
					case "blog_posts":
						switch columnName {
						case "feature":
							column.FieldType = reflect.TypeOf(Feature{})
						case "other_features":
							column.FieldType = reflect.TypeOf(Features{})
						case "tags":
							column.FieldType = reflect.TypeOf([]Tag{})
						}
					}

					return true
				}

				table.FilterColumns(columnFilter)
				return true
			}),
		},
	}

	e := ExportOptions{
		RootDir: rootDir,
		// Optionally:
		// GetFileName: EachTableToItsOwnPackage,
		GetFileName: EachTableGroupToItsOwnPackage(),
	}

	if err := GenerateSchemaFromDatabase(context.Background(), i, e); err != nil {
		fmt.Println(err.Error())
		return
	}

	fmt.Println("OK")

	// Output:
	// OK
}
