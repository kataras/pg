package main

import (
	"context"

	"github.com/kataras/golog"
	"github.com/kataras/pg"
	pgxgolog "github.com/kataras/pgx-golog"
)

// [...]

const connString = "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable&search_path=public"

func main() {
	golog.SetLevel("debug")
	schema := pg.NewSchema()

	logger := pgxgolog.NewLogger(golog.Default)
	/*
		tracer := &tracelog.TraceLog{
			Logger:   logger,
			LogLevel: tracelog.LogLevelTrace,
		}

		connConfig, err := pgxpool.ParseConfig(connString)
		if err != nil {
			panic(err)
		}

		// Set the tracer.
		connConfig.ConnConfig.Tracer = tracer

		pool, err := pgxpool.NewWithConfig(context.Background(), connConfig)
		if err != nil {
			panic(err)
		}

		// Use OpenPool instead of Open to use the pool's connections.
		db := pg.OpenPool(schema, pool)
	*/
	// OR:
	db, err := pg.Open(context.Background(), schema, connString, pg.WithLogger(logger))
	if err != nil {
		panic(err)
	}
	defer db.Close()

	rows, err := db.Query(context.Background(), `SELECT * FROM blog_posts;`)
	if err != nil {
		panic(err)
	}

	rows.Close()
}
