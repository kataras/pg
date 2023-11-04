package pg

import (
	"context"
	"fmt"
)

// Repositories.

// CustomerRepository is a struct that wraps a generic Repository instance with the Customer type parameter.
// It provides methods for accessing and manipulating customer data in the database.
type CustomerRepository struct {
	*Repository[Customer]
}

// NewCustomerRepository creates and returns a new CustomerRepository instance with the given DB instance.
func NewCustomerRepository(db *DB) *CustomerRepository {
	return &CustomerRepository{
		Repository: NewRepository[Customer](db),
	}
}

// InTransaction overrides the pg Repository's InTransaction method to include the custom type of CustomerRepository.
// It takes a context and a function as arguments and executes the function within a database transaction,
// passing it a CustomerRepository instance that uses the transactional DB instance.
func (r *CustomerRepository) InTransaction(ctx context.Context, fn func(*CustomerRepository) error) (err error) {
	if r.DB().IsTransaction() {
		return fn(r)
	}

	return r.DB().InTransaction(ctx, func(db *DB) error {
		txRepository := NewCustomerRepository(db)
		return fn(txRepository)
	})
}

// Exists is a custom method that uses the pg repository's Database instance to execute a query and return a result.
// It takes a context and a cognitoUserID as arguments and checks if there is any customer row with that cognitoUserID in the database.
func (r *CustomerRepository) Exists(ctx context.Context, cognitoUserID string) (exists bool, err error) {
	// query := `SELECT EXISTS(SELECT 1 FROM customers WHERE cognito_user_id = $1)`
	// err = r.QueryRow(ctx, query, cognitoUserID).Scan(&exists)
	// OR:

	exists, err = r.Repository.Exists(ctx, Customer{CognitoUserID: cognitoUserID})
	return
}

// Registry is (optional) a struct that holds references to different repositories for accessing and manipulating data in the database.
// It has a db field that is a pointer to a DB instance, and a customers field that is a pointer to a CustomerRepository instance.
type Registry struct {
	db *DB

	customers *CustomerRepository
	blogs     *Repository[Blog]
	blogPosts *Repository[BlogPost]
}

// NewRegistry creates and returns a new Registry instance with the given DB instance.
// It also initializes the customers field with a new CustomerRepository instance that uses the same DB instance.
func NewRegistry(db *DB) *Registry {
	return &Registry{
		db: db,

		customers: NewCustomerRepository(db),
		blogs:     NewRepository[Blog](db),
		blogPosts: NewRepository[BlogPost](db),
	}
}

// InTransaction overrides the pg Repository's InTransaction method to include the custom type of Registry.
// It takes a context and a function as arguments and executes the function within a database transaction,
// passing it a Registry instance that uses the transactional DB instance.
func (r *Registry) InTransaction(ctx context.Context, fn func(*Registry) error) (err error) {
	if r.db.IsTransaction() {
		return fn(r)
	}

	return r.db.InTransaction(ctx, func(db *DB) error {
		txRegistry := NewRegistry(db)
		return fn(txRegistry)
	})
}

// Customers returns the CustomerRepository instance of the Registry.
func (r *Registry) Customers() *CustomerRepository {
	return r.customers
}

// Blogs returns the Repository instance of the Blog entity.
func (r *Registry) Blogs() *Repository[Blog] {
	return r.blogs
}

// BlogPosts returns the Repository instance of the BlogPost entity.
func (r *Registry) BlogPosts() *Repository[BlogPost] {
	return r.blogPosts
}

func ExampleNewRepository() {
	db, err := openTestConnection(true)
	if err != nil {
		handleExampleError(err)
		return
	}
	defer db.Close()

	registry := NewRegistry(db)       // Create a new Registry instance with the DB instance.
	customers := registry.Customers() // Get the CustomerRepository instance from the Registry.

	// Repository example code.
	customerToInsert := Customer{ // Create a Customer struct to be inserted into the database.
		CognitoUserID: "373f90eb-00ac-410f-9fe0-1a7058d090ba",
		Email:         "kataras2006@hotmail.com",
		Name:          "kataras",
	}

	err = customers.InsertSingle(context.Background(), customerToInsert, &customerToInsert.ID)
	if err != nil {
		handleExampleError(err)
		return
	}

	fmt.Println(customerToInsert.ID)
}
