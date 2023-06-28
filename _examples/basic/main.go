package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/kataras/pg"
)

type Base struct {
	ID        string    `pg:"type=uuid,primary"`
	CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
	UpdatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
}

type Customer struct {
	Base

	Firstname string `pg:"type=varchar(255)"`
}

func main() {
	// Create Schema instance.
	schema := pg.NewSchema()
	schema.MustRegister("customers", Customer{})

	// Create Database instance.
	connString := "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable"
	db, err := pg.Open(context.Background(), schema, connString)
	if err != nil {
		log.Fatal(fmt.Errorf("open database: %w", err))
	}
	defer db.Close()

	// Optionally create and check the database schema.
	if err = db.CreateSchema(context.Background()); err != nil {
		log.Fatal(fmt.Errorf("create schema: %w", err))
	}

	if err = db.CheckSchema(context.Background()); err != nil {
		log.Fatal(fmt.Errorf("check schema: %w", err))
	}

	// Create a Repository of Customer type.
	customers := pg.NewRepository[Customer](db)

	var newCustomer = Customer{
		Firstname: "John",
	}

	// Insert a new Customer.
	err = customers.InsertSingle(context.Background(), newCustomer, &newCustomer.ID)
	if err != nil {
		log.Fatal(fmt.Errorf("insert customer: %w", err))
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

	// Get all.
	query := `SELECT * FROM customers ORDER BY created_at DESC;`
	allCustomers, err := customers.Select(context.Background(), query)
	if err != nil {
		log.Fatal(fmt.Errorf("select all: %w", err))
	}
	log.Printf("All Customers (%d): ", len(allCustomers))
	for _, customer := range allCustomers {
		fmt.Printf("- (%s) %s\n", customer.ID, customer.Firstname)
	}
}
