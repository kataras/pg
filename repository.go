package pg

import (
	"context"
	"errors"
	"reflect"

	"github.com/kataras/pg/desc"

	"github.com/jackc/pgx/v5/pgconn"
)

// Repository is a generic type that represents a repository for a specific type T.
type Repository[T any] struct {
	db *DB // a field that holds a pointer to a DB instance.

	td *desc.Table // cache table definition to make it even faster on serve-time.
}

// NewRepository creates and returns a new Repository instance for a given type T and a DB instance.
// It panics if the T was not registered to the schema.
func NewRepository[T any](db *DB) *Repository[T] {
	// Check if the table definion exists and cache it.
	var value T
	td, err := db.schema.Get(reflect.TypeOf(value))
	if err != nil {
		panic(err) // panic as soon as possible before any call at serve-time.
	}

	return &Repository[T]{
		db: db, // assign the db parameter to the db field
		td: td,
	}
}

// ==== //

// DB returns the DB instance associated with the Repository instance.
func (repo *Repository[T]) DB() *DB {
	return repo.db // return the db field
}

// Table returns the Table definition instance associated with the Repository instance.
// It should NOT be modified by the caller.
func (repo *Repository[T]) Table() *desc.Table {
	return repo.td
}

// QueryRow executes a query that returns at most one row and returns it as a Row instance.
func (repo *Repository[T]) QueryRow(ctx context.Context, query string, args ...any) Row {
	return repo.db.QueryRow(ctx, query, args...)
}

// Query executes a query that returns multiple rows and returns them as a Rows instance and an error.
func (repo *Repository[T]) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	return repo.db.Query(ctx, query, args...)
}

// Exec executes a query that does not return rows and returns a command tag and an error.
func (repo *Repository[T]) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return repo.db.Exec(ctx, query, args...)
}

// === //

// InTransaction runs a function within a database transaction and commits or
// rolls back depending on the error value returned by the function.
func (repo *Repository[T]) InTransaction(ctx context.Context, fn func(*Repository[T]) error) error {
	if repo.db.IsTransaction() {
		return fn(repo)
	}

	return repo.db.InTransaction(context.Background(), func(db *DB) error {
		txRepo := &Repository[T]{
			db: db,
			td: repo.td,
		}

		return fn(txRepo)
	})
}

// IsTransaction returns true if the underline database is already in a transaction or false otherwise.
func (repo *Repository[T]) IsTransaction() bool {
	return repo.db.IsTransaction()
}

// IsReadOnly returns true if the underline repository's table is read-only or false otherwise.
func (repo *Repository[T]) IsReadOnly() bool {
	return repo.td.IsReadOnly()
}

// Select executes a SQL query and returns a slice of values of type T that match the query results.
func (repo *Repository[T]) Select(ctx context.Context, query string, args ...any) ([]T, error) {
	rows, err := repo.db.Query(ctx, query, args...) // execute the query using repo.db.Query and pass in the arguments
	if err != nil {
		return nil, err // return nil and the error if the query fails
	}

	list, err := desc.RowsToStruct[T](repo.td, rows) // convert the rows returned by the query to a slice of values of type T using rowsToStruct
	if err != nil {
		return nil, err // return nil and the error if the conversion fails
	}

	return list, nil // return the slice of values and nil as no error occurred
}

// SelectSingle executes a SQL query and returns a single value of type T that matches the query result.
func (repo *Repository[T]) SelectSingle(ctx context.Context, query string, args ...any) (T, error) {
	var value T // declare a zero value of type T

	rows, err := repo.db.Query(ctx, query, args...) // execute the query using repo.db.Query and pass in the arguments
	if err != nil {
		return value, err // return the zero value and the error if the query fails
	}

	value, err = desc.RowToStruct[T](repo.td, rows) // convert the first row returned by the query to a value of type T using rowToStruct
	return value, err                               // return the value and the error from rowToStruct (nil or not)
}

// SelectByID selects a row from a table by matching the id column with the given argument and returns the row or ErrNoRows.
func (repo *Repository[T]) SelectByID(ctx context.Context, id any) (T, error) {
	var value T // declare a zero value of type T

	err := repo.db.selectTableRecordByID(ctx, repo.td, &value, id)
	return value, err
}

// SelectByUsernameAndPassword selects a row from a table by matching the username and password columns with the given arguments
// and returns the row or ErrNoRows.
func (repo *Repository[T]) SelectByUsernameAndPassword(ctx context.Context, username, plainPassword string) (T, error) {
	var value T // declare a zero value of type T

	err := repo.db.selectTableRecordByUsernameAndPassword(ctx, repo.td, &value, username, plainPassword)
	return value, err
}

// Exists returns true if a row exists in the table that matches the given value's non-zero fields or false otherwise.
func (repo *Repository[T]) Exists(ctx context.Context, value T) (bool, error) {
	return repo.db.tableRecordExists(ctx, repo.td, desc.IndirectValue(value))
}

// ErrIsReadOnly is returned by Insert and InsertSingle if the repository is read-only.
var ErrIsReadOnly = errors.New("repository is read-only")

// Insert inserts one or more values of type T into the database by calling repo.InsertSingle for each value within a transaction.
func (repo *Repository[T]) Insert(ctx context.Context, values ...T) error {
	if repo.IsReadOnly() {
		return ErrIsReadOnly
	}

	switch len(values) {
	case 0:
		return nil
	case 1:
		return repo.InsertSingle(ctx, values[0], nil)
	default:
		// Use repo.InTransaction to run a function within a database transaction and handle the commit or rollback
		return repo.InTransaction(ctx, func(repo *Repository[T]) error {
			// Loop over the values and insert each one using repo.InsertSingle
			for _, value := range values {
				// Call repo.InsertSingle with the value and nil as the idPtr
				err := repo.InsertSingle(ctx, value, nil)
				if err != nil {
					return err // return the error and roll back the transaction if repo.InsertSingle fails
				}
			}

			return nil // return nil and commit the transaction if no error occurred
		})
	}
}

// InsertSingle inserts a single value of type T into the database by calling repo.db.InsertSingle with the value and the idPtr.
//
// If it is not null then the value is updated by its primary key value.
func (repo *Repository[T]) InsertSingle(ctx context.Context, value T, idPtr any) error {
	if repo.IsReadOnly() {
		return ErrIsReadOnly
	}

	return repo.db.insertTableRecord(ctx, repo.td, desc.IndirectValue(value), idPtr, "", false) // delegate the insertion to repo.db.insertTableRecord and return its result
}

// Upsert inserts or updates one or more values of type T into the database.
func (repo *Repository[T]) Upsert(ctx context.Context, forceOnConflictExpr string, values ...T) error {
	if repo.IsReadOnly() {
		return ErrIsReadOnly
	}

	switch len(values) {
	case 0:
		return nil
	case 1:
		return repo.UpsertSingle(ctx, forceOnConflictExpr, values[0], nil)
	default:
		// Use repo.InTransaction to run a function within a database transaction and handle the commit or rollback
		return repo.InTransaction(ctx, func(repo *Repository[T]) error {
			// Loop over the values and insert each one using repo.UpsertSingle
			for _, value := range values {
				// Call repo.UpsertSingle with the value and nil as the idPtr
				err := repo.UpsertSingle(ctx, forceOnConflictExpr, value, nil)
				if err != nil {
					return err // return the error and roll back the transaction if repo.UpsertSingle fails
				}
			}

			return nil // return nil and commit the transaction if no error occurred
		})
	}
}

// UpsertSingle inserts or updates a single value of type T into the database.
//
// If idPtr is not null then the value is updated by its primary key value.
func (repo *Repository[T]) UpsertSingle(ctx context.Context, forceOnConflictExpr string, value T, idPtr any) error {
	if repo.IsReadOnly() {
		return ErrIsReadOnly
	}

	return repo.db.insertTableRecord(ctx, repo.td, desc.IndirectValue(value), idPtr, forceOnConflictExpr, true)
}

// Delete deletes one or more values of type T from the database by their primary key values.
func (repo *Repository[T]) Delete(ctx context.Context, values ...T) (int64, error) {
	if repo.IsReadOnly() {
		return 0, ErrIsReadOnly
	}

	if len(values) == 0 {
		return 0, nil
	}

	valuesAsInterfaces := toInterfaces(values)
	return repo.db.deleteTableRecords(ctx, repo.td, valuesAsInterfaces)
}

// DeleteByID deletes a single row from a table by matching the id column with the given argument and
// reports whether the entry was removed or not.
//
// The difference between Delete and DeleteByID is that
// DeleteByID accepts just the id value instead of the whole entity structure value.
func (repo *Repository[T]) DeleteByID(ctx context.Context, id any) (bool, error) {
	return repo.db.deleteByID(ctx, repo.td, id)
}

// Update updates one or more values of type T in the database by their primary key values.
func (repo *Repository[T]) Update(ctx context.Context, values ...T) (int64, error) {
	return repo.UpdateOnlyColumns(ctx, nil, values...)
}

// UpdateExceptColumns updates one or more values of type T in the database by their primary key values.
// The columnsToExcept parameter can be used to specify which columns should NOT be updated.
func (repo *Repository[T]) UpdateExceptColumns(ctx context.Context, columnsToExcept []string, values ...T) (int64, error) {
	columnsToUpdate := repo.td.ListColumnNamesExcept(columnsToExcept...)
	return repo.UpdateOnlyColumns(ctx, columnsToUpdate, values...)
}

// UpdateOnlyColumns updates one or more values of type T in the database by their primary key values.
//
// The columnsToUpdate parameter can be used to specify which columns should be updated.
func (repo *Repository[T]) UpdateOnlyColumns(ctx context.Context, columnsToUpdate []string, values ...T) (int64, error) {
	if repo.IsReadOnly() {
		return 0, ErrIsReadOnly
	}

	if len(values) == 0 {
		return 0, nil
	}

	valuesAsInterfaces := toInterfaces(values)
	return repo.db.updateTableRecords(ctx, repo.td, columnsToUpdate, valuesAsInterfaces)
}

func toInterfaces[T any](values []T) []any {
	valuesAsInterfaces := make([]any, len(values)) // create a slice of interfaces to store the values
	for i, value := range values {
		valuesAsInterfaces[i] = value // assign each value to the slice
	}

	return valuesAsInterfaces
}
