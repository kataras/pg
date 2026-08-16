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
		ConnString: getTestConnString(),
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

// getTestConnString returns the connection string to test against, read from the
// PG_CONNSTRING environment variable when set. This package (gen) cannot reuse the root
// pg package's own getTestConnString (package pg, unexported), so this is a small local
// equivalent: when PG_CONNSTRING is unset or empty, it falls back to the same hardcoded
// literal this example used before, so CI's postgres service container (see
// .github/workflows/ci.yml) keeps working unchanged.
func getTestConnString() string {
	if override := os.Getenv("PG_CONNSTRING"); override != "" {
		return override
	}

	return "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable"
}
