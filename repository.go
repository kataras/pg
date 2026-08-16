package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// QueryBoolean executes a query that returns a single boolean value and returns it as a bool and an error.
func (repo *Repository[T]) QueryBoolean(ctx context.Context, query string, args ...any) (bool, error) {
	return repo.db.QueryBoolean(ctx, query, args...)
}

// Count executes a query that returns a single numeric value (typically a COUNT(*) or other
// aggregate over the repository's table) and returns it as an int64. A query that yields no
// rows counts as zero; see DB.Count.
func (repo *Repository[T]) Count(ctx context.Context, query string, args ...any) (int64, error) {
	return repo.db.Count(ctx, query, args...)
}

// OrderBy validates a user-supplied sort column against the repository's table descriptor
// and returns a quoted `"column" ASC|DESC` fragment ready to splice into an ORDER BY clause
// (e.g. a caller's own PageOptions.OrderBy field). Callers that alias the table in the query
// may prefix the returned fragment with the alias themselves, e.g. `"f." + fragment`.
//
// It is a thin, repository-scoped wrapper over desc.Table.OrderBy: see that method's doc for
// the full validation rules (case-insensitive match against the table's columns, or exact
// membership in extraColumns for computed/aliased columns), the empty-column fallback chain
// (created_at, then updated_at, then the primary key), and why this validate-then-quote
// approach is required instead of a bind parameter (dynamic ORDER BY cannot be one; see
// jackc/pgx#885).
func (repo *Repository[T]) OrderBy(column string, descending bool, extraColumns ...string) (string, error) {
	return repo.td.OrderBy(column, descending, extraColumns...)
}

// Query executes a query that returns multiple rows and returns them as a Rows instance and an error.
func (repo *Repository[T]) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	return repo.db.Query(ctx, query, args...)
}

// Exec executes a query that does not return rows and returns a command tag and an error.
func (repo *Repository[T]) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return repo.db.Exec(ctx, query, args...)
}

// Mutate executes a query that returns the number of affected rows and returns it and an error.
func (repo *Repository[T]) Mutate(ctx context.Context, query string, args ...any) (int64, error) {
	return repo.db.Mutate(ctx, query, args...)
}

// MutateSingle executes a query that modifies the database and returns true if at least one row was affected.
func (repo *Repository[T]) MutateSingle(ctx context.Context, query string, args ...any) (bool, error) {
	return repo.db.MutateSingle(ctx, query, args...)
}

// === //

// InTransaction runs a function within a database transaction and commits or
// rolls back depending on the error value returned by the function.
// The given ctx governs the transaction's begin, commit and rollback calls, so an
// already-canceled or expired ctx makes InTransaction return promptly without
// running fn.
func (repo *Repository[T]) InTransaction(ctx context.Context, fn func(*Repository[T]) error) error {
	if repo.db.IsTransaction() {
		return fn(repo)
	}

	return repo.db.InTransaction(ctx, func(db *DB) error {
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

// Insert inserts one or more values of type T into the database.
//
// For a single value, it delegates to InsertSingle. For multiple values
// it delegates to InsertMany, which issues one multi-row INSERT per
// desc.DefaultInsertBatchSize rows rather than one round-trip per row.
// The previous implementation compounded network latency and could turn
// a few-thousand-row catalog sync into a multi-minute operation.
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
		return repo.InsertMany(ctx, values...)
	}
}

// InsertMany bulk-inserts values via multi-row VALUES statements in
// batches of desc.DefaultInsertBatchSize. The whole operation runs in
// one transaction so any batch failure rolls back all earlier batches.
//
// Per-row semantics match InsertSingle: zero-valued fields on columns
// that carry a DB default emit the DEFAULT keyword instead of a
// parameter, so clock_timestamp() / gen_random_uuid() etc. fire as
// expected.
func (repo *Repository[T]) InsertMany(ctx context.Context, values ...T) error {
	if repo.IsReadOnly() {
		return ErrIsReadOnly
	}
	if len(values) == 0 {
		return nil
	}

	return repo.InTransaction(ctx, func(repo *Repository[T]) error {
		// PostgreSQL allows at most 65535 bind parameters per prepared statement. A wide table
		// (many insertable columns) combined with DefaultInsertBatchSize rows could otherwise
		// overflow that ceiling at runtime with no way for the caller to override it, so shrink
		// the batch size to fit whenever the table is wide enough for that to matter.
		batchSize := desc.DefaultInsertBatchSize
		if n := repo.td.NumInsertableColumns(); n > 0 {
			batchSize = min(batchSize, 65535/n)
		}

		for start := 0; start < len(values); start += batchSize {
			end := min(start+batchSize, len(values))
			batch := values[start:end]

			structValues := make([]reflect.Value, len(batch))
			for i := range batch {
				structValues[i] = desc.IndirectValue(batch[i])
			}

			query, args, err := desc.BuildBulkInsertQuery(repo.td, structValues, "", false)
			if err != nil {
				return err
			}
			if _, err := repo.db.Exec(ctx, query, args...); err != nil {
				return err
			}
		}
		return nil
	})
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

// DoNothing is a constant that can be used as the forceOnConflictExpr argument of
// Upsert/UpsertMany/UpsertSingle (and DB.Upsert/DB.UpsertSingle) to emit a real
// "ON CONFLICT ... DO NOTHING" instead of the usual "ON CONFLICT ... DO UPDATE SET ...".
// The target is derived the same way a normal Upsert call derives it (the struct's
// unique/unique_index tags), or, if the struct declares none, DO NOTHING is emitted with no
// target at all ("ON CONFLICT DO NOTHING", which applies to a conflict against any unique
// constraint on the table). The match against forceOnConflictExpr is case-insensitive, but
// this exact value ("DO NOTHING") is always recognized.
//
// The same action can be requested via a `conflict=DO NOTHING` struct tag (Column.Conflict)
// instead of passing DoNothing explicitly, but only on an Upsert-family call (forceOnConflictExpr
// == "", upsert == true); a plain InsertSingle/DB.InsertSingle call ignores the tag and lets a
// duplicate raise the database's own unique-violation error, same as it always has.
//
// UpsertSingle/DB.UpsertSingle called with idPtr set (single-row only: UpsertMany/DB.Upsert's
// multi-value form never scans a row back) and forceOnConflictExpr == DoNothing always carries
// RETURNING <primary key>, even though a DO NOTHING action would not otherwise get one (a
// skipped conflicting row would otherwise come back indistinguishable from a query error, so
// plain DO NOTHING, e.g. via a `conflict=<raw SQL>` tag unrelated to DoNothing, still omits
// it): a row inserted with no conflict populates idPtr as usual, and a skipped conflicting row
// is reported as ErrNoRows instead of a stale/zero idPtr: the same contract
// Repository.InsertSingleOnConflict already guarantees for OnConflict{DoNothing: true}, which
// remains the richer alternative (partial SetColumns, SetWhere, a named Constraint target, or
// bulk DO NOTHING via InsertOnConflict) when this simpler forceOnConflictExpr form isn't enough.
const DoNothing = "DO NOTHING"

// Upsert inserts or updates one or more values of type T into the database.
// Upsert inserts or updates one or more values of type T. Single-row
// calls delegate to UpsertSingle; multi-row calls delegate to
// UpsertMany, which issues batched multi-row INSERT ... ON CONFLICT
// DO UPDATE statements rather than one round-trip per row.
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
		return repo.UpsertMany(ctx, forceOnConflictExpr, values...)
	}
}

// UpsertMany bulk-upserts values via multi-row INSERT ... ON CONFLICT
// DO UPDATE (or, with forceOnConflictExpr set to DoNothing, DO NOTHING) statements in
// batches of desc.DefaultInsertBatchSize. forceOnConflictExpr behaves exactly as on
// UpsertSingle: empty uses the struct's declared conflict target (unique_index tag),
// DoNothing forces a DO NOTHING action, and any other non-empty value names a specific
// unique index to target for a DO UPDATE.
//
// The whole call runs in one transaction. Per-row DEFAULT semantics
// match InsertMany: zero-valued fields on defaulted columns emit the
// DEFAULT keyword so DB defaults fire.
func (repo *Repository[T]) UpsertMany(ctx context.Context, forceOnConflictExpr string, values ...T) error {
	if repo.IsReadOnly() {
		return ErrIsReadOnly
	}
	if len(values) == 0 {
		return nil
	}

	return repo.InTransaction(ctx, func(repo *Repository[T]) error {
		// PostgreSQL allows at most 65535 bind parameters per prepared statement. A wide table
		// (many insertable columns) combined with DefaultInsertBatchSize rows could otherwise
		// overflow that ceiling at runtime with no way for the caller to override it, so shrink
		// the batch size to fit whenever the table is wide enough for that to matter.
		batchSize := desc.DefaultInsertBatchSize
		if n := repo.td.NumInsertableColumns(); n > 0 {
			batchSize = min(batchSize, 65535/n)
		}

		for start := 0; start < len(values); start += batchSize {
			end := min(start+batchSize, len(values))
			batch := values[start:end]

			structValues := make([]reflect.Value, len(batch))
			for i := range batch {
				structValues[i] = desc.IndirectValue(batch[i])
			}

			query, args, err := desc.BuildBulkInsertQuery(repo.td, structValues, forceOnConflictExpr, true)
			if err != nil {
				return err
			}
			if _, err := repo.db.Exec(ctx, query, args...); err != nil {
				return err
			}
		}
		return nil
	})
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
	return repo.db.updateTableRecords(ctx, repo.td, columnsToUpdate, false, valuesAsInterfaces)
}

// UpdateOnlyColumnsReportNoRows updates one or more values of type T in the database by their primary key values.
// It returns an ErrNoRows if there is no matching row on the given criteria.
func (repo *Repository[T]) UpdateOnlyColumnsReportNoRows(ctx context.Context, columnsToUpdate []string, values ...T) (bool, error) {
	if repo.IsReadOnly() {
		return false, ErrIsReadOnly
	}

	if len(values) == 0 {
		return false, nil
	}

	valuesAsInterfaces := toInterfaces(values)
	_, err := repo.db.updateTableRecords(ctx, repo.td, columnsToUpdate, true, valuesAsInterfaces)
	if err != nil {
		if errors.Is(err, ErrNoRows) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func toInterfaces[T any](values []T) []any {
	valuesAsInterfaces := make([]any, len(values)) // create a slice of interfaces to store the values
	for i, value := range values {
		valuesAsInterfaces[i] = value // assign each value to the slice
	}

	return valuesAsInterfaces
}

// Duplicate duplicates a row from a table by matching the id column with the given argument.
// The idPtr parameter can be used to get the primary key value of the inserted row.
// If idPtr is nil, the primary key value is not returned.
// If the value is nil, the method returns nil.
func (repo *Repository[T]) Duplicate(ctx context.Context, id any, newIDPtr any) error {
	return repo.db.duplicateTableRecord(ctx, repo.td, id, newIDPtr)
}

// ListenTable registers a function which notifies on the current table's changes (INSERT, UPDATE, DELETE),
// the subscribed postgres channel is named 'table_change_notifications'.
// The callback function is called on a separate goroutine.
//
// The callback function can return a non-nil error to stop the listener, which is then
// reported by the listener's goroutine. Returning nil continues listening.
// Call the returned Closer to stop the listener from the outside.
func (repo *Repository[T]) ListenTable(ctx context.Context, callback func(TableNotification[T], error) error) (Closer, error) {
	opts := &ListenTableOptions{
		Tables: map[string][]TableChangeType{repo.td.Name: defaultChangesToWatch},
	}
	return repo.db.ListenTable(ctx, opts, func(tableEvt TableNotificationJSON, err error) error {
		if err != nil {
			if tableEvt.Table == repo.td.Name {
				failEvt := TableNotification[T]{
					Table:  repo.td.Name,
					Change: tableEvt.Change, // may empty.
				}

				return callback(failEvt, err)
			}

			return err
		}

		evt := TableNotification[T]{
			Table:   tableEvt.Table,
			Change:  tableEvt.Change,
			payload: tableEvt.payload,
		}

		if len(tableEvt.Old) > 0 {
			err := json.Unmarshal(tableEvt.Old, &evt.Old)
			if err != nil {
				return callback(evt, fmt.Errorf("table: %s: unmarshal old: %w", tableEvt.Table, err))
			}
		}

		if len(tableEvt.New) > 0 {
			err := json.Unmarshal(tableEvt.New, &evt.New)
			if err != nil {
				return callback(evt, fmt.Errorf("table: %s: unmarshal new: %w", tableEvt.Table, err))
			}
		}

		return callback(evt, nil)
	})
}
