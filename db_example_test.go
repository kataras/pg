package pg

import (
	"context"
	"fmt"
)

func ExampleOpen() {
	db, err := openTestConnection(true)
	if err != nil {
		handleExampleError(err)
		return
	}
	defer db.Close()

	// Work with the database...
}

func openTestConnection(resetSchema bool) (*DB, error) {
	// Database code.
	schema := NewSchema()
	schema.MustRegister("customers", Customer{})  // Register the Customer struct as a table named "customers".
	schema.MustRegister("blogs", Blog{})          // Register the Blog struct as a table named "blogs".
	schema.MustRegister("blog_posts", BlogPost{}) // Register the BlogPost struct as a table named "blog_posts".

	// Open a connection to the database using the schema and the connection string.
	db, err := Open(context.Background(), schema, getTestConnString())
	if err != nil {
		return nil, err
	}
	// Let the caller close the database connection pool: defer db.Close()

	if resetSchema {
		// Let's clear the schema, so we can run the tests even if already ran once in the past.
		if err = db.DeleteSchema(context.Background()); err != nil { // DON'T DO THIS ON PRODUCTION.
			return nil, fmt.Errorf("delete schema: %w", err)
		}

		if err = db.CreateSchema(context.Background()); err != nil { // Create the schema in the database if it does not exist.
			return nil, fmt.Errorf("create schema: %w", err)
		}

		if err = db.CheckSchema(context.Background()); err != nil { // Check if the schema in the database matches the schema in the code.
			return nil, fmt.Errorf("check schema: %w", err)
		}
	}

	return db, nil
}

func openEmptyTestConnection() (*DB, error) { // without a schema.
	schema := NewSchema()
	// Open a connection to the database using the schema and the connection string.
	return Open(context.Background(), schema, getTestConnString())
}

func createTestConnectionSchema() error {
	db, err := openTestConnection(true)
	if err != nil {
		return err
	}

	db.Close()
	return nil
}

// getTestConnString returns a connection string for connecting to a test database.
// It uses constants to define the host, port, user, password, schema, dbname, and sslmode parameters.
func getTestConnString() string {
	const (
		host     = "localhost" // The host name or IP address of the database server.
		port     = 5432        // The port number of the database server.
		user     = "postgres"  // The user name to connect to the database with.
		password = "admin!123" // The password to connect to the database with.
		schema   = "public"    // The schema name to use in the database.
		dbname   = "test_db"   // The database name to connect to.
		sslMode  = "disable"   // The SSL mode to use for the connection. Can be disable, require, verify-ca or verify-full.
	)

	connString := fmt.Sprintf("host=%s port=%d user=%s password=%s search_path=%s dbname=%s sslmode=%s",
		host, port, user, password, schema, dbname, sslMode) // Format the connection string using the parameters.

	return connString // Return the connection string.
}
