package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"time"

	"github.com/kataras/pg"
)

//go:embed _embed
var embedDir embed.FS

type (
	BaseEntity struct {
		ID        string    `pg:"type=uuid,primary"`
		CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
		UpdatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
	}

	Blog struct {
		BaseEntity

		Name string `pg:"type=varchar(255)"`
	}

	BlogMaster struct {
		Blog
		PostsCount int64 `pg:"type=bigint"`
	}
)

func main() {
	// Create Schema instance.
	schema := pg.NewSchema()
	// Register the table as a view, the third argument is the only important step here.
	// This view is created through _embed/example.sql file.
	schema.MustRegister("blog_master", BlogMaster{}, pg.View)

	// Create Database instance.
	connString := "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable"
	db, err := pg.Open(context.Background(), schema, connString)
	if err != nil {
		log.Fatal(fmt.Errorf("open database: %w", err))
	}
	defer db.Close()

	// Here you can define your functions, triggers, tables and e.t.c. as an embedded sql file which
	// should be executed on the database.
	if err = db.ExecFiles(context.Background(), embedDir, "_embed/example.sql"); err != nil {
		log.Fatal(err)
	}

	// Optional, and this doesn't have any meaning here
	// because we explore just the "views" example here.
	if err := db.CreateSchema(context.Background()); err != nil {
		log.Fatal(err)
	}

	if err := db.CheckSchema(context.Background()); err != nil {
		log.Fatal(err)
	}
	//

	repo := pg.NewRepository[BlogMaster](db)
	blogs, err := repo.Select(context.Background(), `SELECT * FROM blog_master`)
	if err != nil {
		log.Fatal(fmt.Errorf("select all blog masters: %w", err))
	}

	for _, blog := range blogs {
		fmt.Printf("%s: posts count: %d\n", blog.Name, blog.PostsCount)
	}
}
