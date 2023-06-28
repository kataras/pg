package main

import (
	"context"
	"fmt"
	"log"

	"github.com/kataras/pg"
)

type TableInfo struct {
	TableName string `pg:"table_name"`
	TableType string `pg:"table_type"`
}

func main() {
	// Create Schema instance.
	schema := pg.NewSchema()
	// Register the table as a presenter, the third argument is the only important step here.
	schema.MustRegister("table_info_presenters", TableInfo{}, pg.Presenter)

	// Create Database instance.
	connString := "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable"
	db, err := pg.Open(context.Background(), schema, connString)
	if err != nil {
		log.Fatal(fmt.Errorf("open database: %w", err))
	}
	defer db.Close()

	if err = db.CreateSchema(context.Background()); err != nil {
		log.Fatal(err)
	}

	if err = db.CheckSchema(context.Background()); err != nil {
		log.Fatal(err)
	}

	repo := pg.NewRepository[TableInfo](db)

	// This can be created through normal table registration but
	// this is just an example of how to use the presenter.
	tables, err := repo.Select(context.Background(), `SELECT table_name,table_type FROM information_schema.tables WHERE table_schema = $1;`, db.SearchPath())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Found %d table(s).\n", len(tables))
	for _, t := range tables {
		fmt.Printf("- %s (%s)", t.TableName, t.TableType)
	}
}
