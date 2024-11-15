package pg

import (
	"context"
	"fmt"
	"reflect"

	"github.com/kataras/pg/desc"
)

// Select executes a query that returns rows and calls the scanner function on them.
// It takes a context, a query string, a scanner function and a variadic list of arguments as parameters.
// It returns an error if the query fails, the scanner function returns an error or the rows have an error.
func (db *DB) Select(ctx context.Context, scannerFunc func(Rows) error, query string, args ...any) error {
	if scannerFunc == nil {
		return fmt.Errorf("scannerFunc is nil") // check if the scanner function is nil and return an error if so
	}

	rows, err := db.Query(ctx, query, args...) // execute the query using repo.db.Query and pass in the arguments
	if err != nil {
		return err // return nil and the error if the query fails
	}
	defer rows.Close() // close the rows after the function returns

	if err = scannerFunc(rows); err != nil {
		return err // call the scanner function on the rows and return an error if it returns an error
	}

	return rows.Err() // return any error that occurred during or after reading from the rows
}

// SelectByID selects a row from a table by matching the primary key column with the given argument.
func (db *DB) SelectByID(ctx context.Context, destPtr any, id any) error {
	td, err := db.schema.Get(reflect.TypeOf(destPtr)) // get the table definition from the schema by using the type of the result variable
	if err != nil {
		return err // return an error if getting the table definition failed.
	}

	return db.selectTableRecordByID(ctx, td, destPtr, id)
}

func (db *DB) selectTableRecordByID(ctx context.Context, td *desc.Table, destPtr any, id any) error {
	primaryCol, ok := td.PrimaryKey()
	if !ok {
		return fmt.Errorf("no primary key found in table definition: %s", td.Name) // return an error if the table definition does not have a primary key
	}

	query := fmt.Sprintf(`SELECT * FROM "%s"."%s" WHERE "%s" = $1 LIMIT 1;`, db.searchPath, td.Name, primaryCol.Name)
	return db.selectSingleTable(ctx, td, destPtr, query, id)
}

// SelectByUsernameAndPassword selects a row from a table by matching the username and password columns with the given arguments
// and scans the row into the destPtr variable.
func (db *DB) SelectByUsernameAndPassword(ctx context.Context, destPtr any, username, plainPassword string) error {
	td, err := db.schema.Get(reflect.TypeOf(destPtr)) // get the table definition from the schema by using the type of the result variable
	if err != nil {
		return err // return an error if getting the table definition failed.
	}

	return db.selectTableRecordByUsernameAndPassword(ctx, td, destPtr, username, plainPassword)
}

func (db *DB) selectTableRecordByUsernameAndPassword(ctx context.Context, td *desc.Table, destPtr any, username, plainPassword string) error {
	usernameCol := td.GetUsernameColumn() // get the username column from the table definition
	passwordCol := td.GetPasswordColumn() // get the password column from the table definition

	if usernameCol == nil || passwordCol == nil {
		return fmt.Errorf("username or password columns not found") // return an error if either column is nil
	}

	// construct a SQL query to select the row by using placeholders for the arguments
	query := fmt.Sprintf(`SELECT * FROM "%s"."%s" WHERE "%s" = $1 AND password = crypt($2, %s) LIMIT 1;`,
		db.searchPath, td.Name, usernameCol.Name, passwordCol.Name)

	return db.selectSingleTable(ctx, td, destPtr, query, username, plainPassword)
}

func (db *DB) selectSingleTable(ctx context.Context, td *desc.Table, destPtr any, query string, args ...any) error {
	rows, err := db.Query(ctx, query, args...) // execute the query with the given arguments and get the rows
	if err != nil {
		return err // return an error if executing the query failed
	}
	defer rows.Close() // close the rows when the function returns

	if !rows.Next() { // check if there is a next row in the rows
		if err = rows.Err(); err != nil {
			return err // return an error if there was an error in getting the next row
		}

		return ErrNoRows // return an error if there was no row in the rows
	}

	err = desc.ConvertRowsToStruct(td, rows, destPtr) // convert the row to a struct and assign it to destPtr
	return err                                        // return any error from converting the row
}

// Exists returns true if a row exists in the table that matches the given value's non-zero fields or false otherwise.
func (db *DB) Exists(ctx context.Context, value any) (bool, error) {
	structValue := desc.IndirectValue(value)     // get the reflect.Value of the value and dereference it if it is a pointe
	td, err := db.schema.Get(structValue.Type()) // get the table definition from the schema by using the type of the result variable
	if err != nil {
		return false, err // return an error if getting the table definition failed.
	}
	return db.tableRecordExists(ctx, td, structValue)
}

func (db *DB) tableRecordExists(ctx context.Context, td *desc.Table, structValue reflect.Value) (bool, error) {
	query, args, err := desc.BuildExistsQuery(td, structValue)
	if err != nil {
		return false, err // return the error if finding arguments fails
	}

	var exists bool
	err = db.QueryRow(ctx, query, args...).Scan(&exists)
	return exists, err
}

// Insert inserts one or more values into the database by calling db.InsertSingle for each value within a transaction.
func (db *DB) Insert(ctx context.Context, values ...any) error {
	switch len(values) {
	case 0:
		return nil
	case 1:
		return db.InsertSingle(ctx, values[0], nil)
	default:
		// Use db.InTransaction to run a function within a database transaction and handle the commit or rollback
		return db.InTransaction(ctx, func(db *DB) error {
			// Loop over the values and insert each one using db.InsertSingle
			for _, value := range values {
				// Call db.InsertSingle with the value and nil as the options
				err := db.InsertSingle(ctx, value, nil)
				if err != nil {
					return err // return the error and roll back the transaction if db.InsertSingle fails
				}
			}

			return nil // return nil and commit the transaction if no error occurred
		})
	}

}

// InsertSingle inserts a single value into the database by building and
// executing an SQL query based on the value and the table definition.
func (db *DB) InsertSingle(ctx context.Context, value any, idPtr any) error {
	structValue := desc.IndirectValue(value)     // get the reflect.Value of the value and dereference it if it is a pointer
	td, err := db.schema.Get(structValue.Type()) // get the table definition from the schema based on the type of the value
	if err != nil {
		return err // return the error if the table definition is not found
	}

	return db.insertTableRecord(ctx, td, structValue, idPtr, "", false)
}

// Upsert inserts or updates one or more values into the database by calling db.UpsertSingle for each value within a transaction.
func (db *DB) Upsert(ctx context.Context, forceOnConflictExpr string, values ...any) error {
	switch len(values) {
	case 0:
		return nil
	case 1:
		return db.UpsertSingle(ctx, values[0], nil, forceOnConflictExpr)
	default:
		// Use db.InTransaction to run a function within a database transaction and handle the commit or rollback
		return db.InTransaction(ctx, func(db *DB) error {
			// Loop over the values and insert each one using db.InsertSingle
			for _, value := range values {
				// Call db.InsertSingle with the value and nil as the options
				err := db.UpsertSingle(ctx, value, nil, forceOnConflictExpr)
				if err != nil {
					return err // return the error and roll back the transaction if db.InsertSingle fails
				}
			}

			return nil // return nil and commit the transaction if no error occurred
		})
	}

}

// UpsertSingle inserts or updates a single value into the database by building and
// executing an SQL query based on the value and the table definition.
func (db *DB) UpsertSingle(ctx context.Context, value any, idPtr any, forceOnConflictExpr string) error {
	structValue := desc.IndirectValue(value)     // get the reflect.Value of the value and dereference it if it is a pointer
	td, err := db.schema.Get(structValue.Type()) // get the table definition from the schema based on the type of the value
	if err != nil {
		return err // return the error if the table definition is not found
	}

	return db.insertTableRecord(ctx, td, structValue, idPtr, forceOnConflictExpr, true)
}

func (db *DB) insertTableRecord(ctx context.Context, td *desc.Table, structValue reflect.Value, idPtr any, forceOnConflictExpr string, upsert bool) error {
	query, args, err := desc.BuildInsertQuery(td, structValue, idPtr, forceOnConflictExpr, upsert)
	if err != nil {
		return err // return the error if building the query fails
	}

	if idPtr != nil {
		// if returningColumn is not empty, use db.QueryRow to execute the query and scan the returned value into idPtr
		err = db.QueryRow(ctx, query, args...).Scan(idPtr)
		if err != nil {
			return err
		}

		return nil
	}

	// otherwise, use db.Exec to execute the query without scanning any result
	_, err = db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}

	return nil
}

// Mutate executes a query that modifies the database and returns the number of rows affected.
func (db *DB) Mutate(ctx context.Context, query string, args ...any) (int64, error) {
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}

	return tag.RowsAffected(), nil
}

// MutateSingle executes a query that modifies the database and returns true if at least one row was affected.
func (db *DB) MutateSingle(ctx context.Context, query string, args ...any) (bool, error) {
	rowsAffected, err := db.Mutate(ctx, query, args...)
	return rowsAffected > 0, err
}

// Delete deletes one or more values from the database by building and executing an
// SQL query based on the values and the table definition.
func (db *DB) Delete(ctx context.Context, values ...any) (int64, error) {
	if len(values) == 0 {
		return 0, nil // return false and nil if no values are given
	}

	var value = values[0]
	structValue := desc.IndirectValue(value)     // get the reflect.Value of the value and dereference it if it is a pointer
	td, err := db.schema.Get(structValue.Type()) // get the table definition from the schema based on the type of the value
	if err != nil {
		return 0, err // return the error if the table definition is not found
	}

	return db.deleteTableRecords(ctx, td, values)
}

func (db *DB) deleteTableRecords(ctx context.Context, td *desc.Table, values []any) (int64, error) {
	query, ids, err := desc.BuildDeleteQuery(td, values)
	if err != nil {
		return 0, err
	}

	// execute the query using db.Exec and pass in the primary key values as a single input parameter
	tag, err := db.Exec(ctx, query, ids)
	if err != nil {
		return 0, err // return false and the wrapped error if executing fails
	}

	return tag.RowsAffected(), nil // return true and nil if at least one row was affected by the query
}

func (db *DB) deleteByID(ctx context.Context, td *desc.Table, id any) (bool, error) {
	primaryKey, ok := td.PrimaryKey()
	if !ok {
		return false, fmt.Errorf("no primary key found in table definition: %s", td.Name)
	}

	query := fmt.Sprintf(`DELETE FROM "%s"."%s" WHERE "%s" = $1;`, db.searchPath, td.Name, primaryKey.Name)
	tag, err := db.Exec(ctx, query, id)
	if err != nil {
		return false, err
	}

	return tag.RowsAffected() > 0, nil
}

// Update updates one or more values in the database by building and executing an
// SQL query based on the values and the table definition.
func (db *DB) Update(ctx context.Context, values ...any) (int64, error) {
	return db.UpdateOnlyColumns(ctx, nil, values...)
}

// UpdateExceptColumns updates one or more values in the database by building and executing an
// SQL query based on the values and the table definition.
//
// The columnsToExcept parameter can be used to specify which columns should NOT be updated.
func (db *DB) UpdateExceptColumns(ctx context.Context, columnsToExcept []string, values ...any) (int64, error) {
	if len(values) == 0 { // return false and nil if no values are given
		return 0, nil
	}

	td, err := db.schema.Get(desc.IndirectType(reflect.TypeOf(values[0])))
	if err != nil {
		return 0, err
	}

	columnsToUpdate := td.ListColumnNamesExcept(columnsToExcept...)
	return db.updateTableRecords(ctx, td, columnsToUpdate, false, values)
}

// UpdateOnlyColumns updates one or more values in the database by building and executing an
// SQL query based on the values and the table definition.
//
// The columnsToUpdate parameter can be used to specify which columns should be updated.
func (db *DB) UpdateOnlyColumns(ctx context.Context, columnsToUpdate []string, values ...any) (int64, error) {
	if len(values) == 0 { // return false and nil if no values are given
		return 0, nil
	}

	td, err := db.schema.Get(desc.IndirectType(reflect.TypeOf(values[0]))) // get the table definition from the schema based on the type of the value
	if err != nil {
		return 0, err // return the error if the table definition is not found
	}

	return db.updateTableRecords(ctx, td, columnsToUpdate, false, values)
}

func (db *DB) updateTableRecords(ctx context.Context, td *desc.Table, columnsToUpdate []string, reportNotFound bool, values []any) (int64, error) {
	primaryKey, ok := td.PrimaryKey()
	if !ok {
		return 0, fmt.Errorf("no primary key found in table definition: %s", td.Name)
	}

	if len(values) == 1 {
		return db.updateTableRecord(ctx, values[0], columnsToUpdate, reportNotFound, primaryKey)
	}

	// if more than one: update each value inside a transaction.
	var totalRowsAffected int64

	err := db.InTransaction(ctx, func(db *DB) error {
		for _, value := range values {
			rowsAffected, err := db.updateTableRecord(ctx, value, columnsToUpdate, reportNotFound, primaryKey)
			if err != nil {
				return err
			}

			totalRowsAffected += rowsAffected
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return totalRowsAffected, nil
}

func (db *DB) updateTableRecord(ctx context.Context, value any, columnsToUpdate []string, reportNotFound bool, primaryKey *desc.Column) (int64, error) {
	// build the SQL query and arguments using the table definition and its primary key.
	query, args, err := desc.BuildUpdateQuery(value, columnsToUpdate, reportNotFound, primaryKey)
	if err != nil {
		return 0, err
	}

	if reportNotFound {
		scanErr := db.QueryRow(ctx, query, args...).Scan(nil)
		if scanErr != nil {
			return 0, scanErr
		}

		return 1, nil
	}

	// execute the query using db.Exec and pass in the primary key values as a parameter
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}

	return tag.RowsAffected(), nil
}

// Duplicate duplicates a row in the database by building and executing an
// SQL query based on the value's primary key (uses SELECT for insert column values).
// The idPtr parameter can be used to get the primary key value of the inserted row.
// If idPtr is nil, the primary key value is not returned.
// If the value is nil, the method returns nil.
func (db *DB) Duplicate(ctx context.Context, value any, idPtr any) error {
	if value == nil { // return false and nil if no values are given
		return nil
	}

	val := desc.IndirectValue(value)
	td, err := db.schema.Get(desc.IndirectType(val.Type())) // get the table definition from the schema based on the type of the value
	if err != nil {
		return err // return the error if the table definition is not found
	}

	primaryKey, ok := td.PrimaryKey()
	if !ok {
		return fmt.Errorf("duplicate: primary key is required")
	}

	idValue, err := desc.ExtractPrimaryKeyValue(primaryKey, val)
	if err != nil {
		return err
	}

	return db.duplicateTableRecord(ctx, td, idValue, idPtr)
}

func (db *DB) duplicateTableRecord(ctx context.Context, td *desc.Table, id any, newIDPtr any) error {
	if id == nil {
		return fmt.Errorf("duplicate: id is required")
	}

	query, err := desc.BuildDuplicateQuery(td, newIDPtr)
	if err != nil {
		return err
	}

	if newIDPtr != nil {
		// Bind returning id.
		err = db.QueryRow(ctx, query, id).Scan(newIDPtr)
	} else {
		// Otherwise just execution the insert command.
		_, err = db.Exec(ctx, query, id)
	}

	return err
}
