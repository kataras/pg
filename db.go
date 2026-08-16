package pg

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/kataras/pg/desc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
)

// SetDefaultTag sets the default tag name for the struct fields.
func SetDefaultTag(tag string) {
	desc.DefaultTag = tag
}

// SetDefaultSearchPath sets the default search path for the database.
func SetDefaultSearchPath(searchPath string) {
	desc.DefaultSearchPath = searchPath
}

var (
	// DefaultColumnNameMapper is the default column name conversion function.
	// It converts the struct field name to snake_case.
	//
	// Further modifications can be calling the `SetDefaultColumnNameMapper` package-level function.
	defaultColumnNameMapper = func(field reflect.StructField) string { return desc.SnakeCase(field.Name) }
	// NoColumnNameMapper is a column name conversion function.
	// It converts the column name to the same as its struct field name.
	NoColumnNameMapper = func(field reflect.StructField) string { return field.Name }
	// JSONColumnNameMapper is a column name conversion function.
	// It converts the column name to the same as its json tag name
	// and fallbacks to field name (if json tag is missing or "-").
	JSONColumnNameMapper = func(field reflect.StructField) string {
		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			return field.Name // fallbacks to field name.
		}

		return strings.SplitN(jsonTag, ",", 2)[0]
	}
)

// SetDefaultColumnNameMapper sets the default column name conversion function.
// This is used when the "name" pg tag option is missing for one or more struct fields.
// Set to nil function to use the default column name conversion function.
func SetDefaultColumnNameMapper(fn func(field reflect.StructField) string) {
	if fn == nil {
		desc.ToColumnName = defaultColumnNameMapper
	} else {
		desc.ToColumnName = fn
	}
}

type (
	// Row is a type alias for pgx.Row.
	Row = pgx.Row
	// Rows is a type alias for pgx.Rows.
	Rows = pgx.Rows

	// Table is a type alias for desc.Table.
	Table = desc.Table
	// Column is a type alias for desc.Column.
	Column = desc.Column
	// ColumnFilter is a type alias for desc.ColumnFilter.
	ColumnFilter = desc.ColumnFilter
	// DataType is a type alias for desc.DataType.
	DataType = desc.DataType
	// TableFilterFunc is a type alias for desc.TableFilterFunc.
	TableFilterFunc = desc.TableFilterFunc
	// OnConflict is a type alias for desc.OnConflict: the conflict target (Columns or a named
	// Constraint) and DO NOTHING/DO UPDATE SET action for Repository.InsertOnConflict and
	// Repository.InsertSingleOnConflict.
	OnConflict = desc.OnConflict
)

// DB represents a database connection that can execute queries and transactions.
// It wraps a pgxpool.Pool and a pgx.ConnConfig to manage the connection options and the search path.
// It also holds a reference to a Schema that defines the database schema and migrations.
type DB struct {
	// Pool is the underlying pgx connection pool this DB executes queries through
	// (directly, or via its current transaction: see IsTransaction).
	Pool *pgxpool.Pool
	// ConnectionOptions is the connection configuration this DB was opened with, as
	// copied from Pool's config by OpenPool. It should not be mutated by the caller.
	ConnectionOptions *pgx.ConnConfig
	searchPath        string

	tx         pgx.Tx
	dbTxClosed bool

	// notifyState holds the mutex-guarded, once-only bookkeeping used by PrepareListenTable
	// to create the table-change notify function and per-table triggers at most once.
	// It is a single pointer so that clone (Begin/InTransaction/BeginConcurrent) never has to
	// copy more than one field, which previously left transaction-scoped *DB instances with a
	// nil mutex. See db_table_listener.go for the tableNotifyState type.
	notifyState *tableNotifyState

	schema *Schema
}

// ConnectionOption is a function that takes a *pgxpool.Config and returns an error.
// It is used to set the connection options for the connection pool.
// It is used by the Open function.
//
// See `WithLogger` package-level function too.
type ConnectionOption func(*pgxpool.Config) error

// WithLogger is a ConnectionOption. It sets the logger for the connection pool.
//
// It installs a pgx tracelog.TraceLog tracer hardcoded to tracelog.LogLevelTrace, the most
// verbose level: pgx will log every SQL statement it executes together with all of its bind
// arguments, including sensitive values such as plaintext passwords (e.g. those passed to
// SelectByUsernameAndPassword). Do not use WithLogger against a production logger; prefer
// WithLoggerLevel instead so the verbosity (and therefore what ends up in your logs) can be
// chosen deliberately.
var WithLogger = func(logger tracelog.Logger) ConnectionOption {
	return func(poolConfig *pgxpool.Config) error {
		tracer := &tracelog.TraceLog{
			Logger:   logger,
			LogLevel: tracelog.LogLevelTrace,
		}

		poolConfig.ConnConfig.Tracer = tracer
		return nil
	}
}

// WithLoggerLevel is a ConnectionOption. It sets the logger for the connection pool, same as
// WithLogger, but lets the caller choose the tracelog.LogLevel instead of WithLogger's hardcoded
// tracelog.LogLevelTrace.
//
// Use a lower level (e.g. tracelog.LogLevelWarn or tracelog.LogLevelError) in production to
// reduce how much pgx logs, or tracelog.LogLevelNone to disable pgx's tracer logging entirely.
// Note that pgx still logs the SQL statement and its bind arguments for a failed query at every
// level down to and including tracelog.LogLevelError; only tracelog.LogLevelNone guarantees that
// sensitive bind arguments (such as plaintext passwords) are never written to the logger.
func WithLoggerLevel(logger tracelog.Logger, level tracelog.LogLevel) ConnectionOption {
	return func(poolConfig *pgxpool.Config) error {
		tracer := &tracelog.TraceLog{
			Logger:   logger,
			LogLevel: level,
		}

		poolConfig.ConnConfig.Tracer = tracer
		return nil
	}
}

// Open creates a new DB instance by parsing the connection string and establishing a connection pool.
// It also sets the search path to the one specified in the connection string or to the default one if not specified.
// It takes a context and a schema as arguments and returns the DB instance or an error if any.
//
// For production connections, prefer "sslmode=verify-full" (or, at a minimum,
// "sslmode=require") over the "sslmode=disable" used in the example below.
//
// Example Code:
//
//	const (
//
//		host     = "localhost" // The host name or IP address of the database server.
//		port     = 5432        // The port number of the database server.
//		user     = "postgres"  // The user name to connect to the database with.
//		password = "admin!123" // The password to connect to the database with.
//		schema   = "public"    // The schema name to use in the database.
//		dbname   = "test_db"   // The database name to connect to.
//		sslMode  = "disable"   // The SSL mode to use for the connection. Can be disable, require, verify-ca or verify-full.
//
//	)
//
//	connString := fmt.Sprintf("host=%s port=%d user=%s password=%s search_path=%s dbname=%s sslmode=%s pool_max_conns=%d pool_min_conns=%d pool_max_conn_lifetime=%s pool_max_conn_idle_time=%s pool_health_check_period=%s", ...)
//	OR
//	connString := "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable&search_path=public"
//
//	db, err := Open(context.Background(), schema, connString)
func Open(ctx context.Context, schema *Schema, connString string, opts ...ConnectionOption) (*DB, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		if err = opt(config); err != nil {
			return nil, err
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		// Only safe, non-sensitive fields are included here: never connString (or any part of
		// it derived from it) as it may hold the plaintext password.
		return nil, fmt.Errorf("open: host=%s dbname=%s: %w", config.ConnConfig.Host, config.ConnConfig.Database, err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}

	db := OpenPool(schema, pool)
	return db, nil
}

// OpenPool creates a new DB instance with the given context, schema and pool.
// It copies the connection config from the pool and sets the search path and schema fields of the DB instance.
// It returns a pointer to the DB instance.
//
// Use the `Open` function to create a new DB instance of a connection string instead.
func OpenPool(schema *Schema, pool *pgxpool.Pool) *DB {
	config := pool.Config().ConnConfig.Copy() // copy the connection config from the pool

	searchPath, ok := config.RuntimeParams["search_path"] // get the search path from the config
	if !ok || strings.TrimSpace(searchPath) == "" {       // check if the search path is empty or not set
		searchPath = desc.DefaultSearchPath // use the default search path if so
	}

	db := &DB{ // create a new DB instance with the fields
		Pool:              pool,       // set the pool field
		ConnectionOptions: config,     // set the connection options field
		searchPath:        searchPath, // set the search path field
		schema:            schema,     // set the schema field
		notifyState:       &tableNotifyState{triggers: make(map[string]struct{})},
	}

	return db // return the DB instance
}

// Close closes the database connection pool and its transactions.
func (db *DB) Close() {
	db.Pool.Close()
}

// Clone copies all fields from the current "db" instance
// and returns a new DB pointer to instance.
func (db *DB) clone(tx pgx.Tx) *DB {
	clone := &DB{
		Pool:              db.Pool,
		ConnectionOptions: db.ConnectionOptions,
		tx:                tx,
		schema:            db.schema,
		searchPath:        db.searchPath,
		notifyState:       db.notifyState, // shared pointer: safe to copy as-is, unlike the old split fields.
	}

	return clone
}

// SearchPath returns the search path of the database.
func (db *DB) SearchPath() string {
	return db.searchPath
}

// Schema returns the Schema instance of the database.
// It should NOT be modified by the caller.
func (db *DB) Schema() *Schema {
	return db.schema
}

// ErrIntentionalRollback is an error that can be returned by a transaction function to rollback the transaction.
// When returned from the function passed to DB.InTransaction (or Repository.InTransaction), it makes the
// transaction roll back and InTransaction itself returns nil on a successful rollback, or the rollback
// error if the rollback fails.
var ErrIntentionalRollback = errors.New("skip error: intentional rollback")

// InTransaction runs a function within a database transaction and commits or rolls back depending on
// the error value returned by the function.
//   - If fn returns nil, the transaction is committed and any commit error is returned to the caller.
//   - If fn returns ErrIntentionalRollback, the transaction is rolled back and InTransaction returns nil
//     on a successful rollback, or the rollback error otherwise.
//   - If fn returns any other non-nil error, the transaction is rolled back and that error is returned;
//     if the rollback itself also fails, both errors are joined (via errors.Join) so callers can still
//     inspect either one with errors.Is/errors.As.
//   - If fn panics, the transaction is rolled back and the panic is re-thrown.
//
// Note that:
// After the first error in the transaction, the transaction is rolled back.
// After the first error in query execution, the transaction is aborted and
// no new commands should be sent through the same transaction.
func (db *DB) InTransaction(ctx context.Context, fn func(*DB) error) (err error) {
	if db.IsTransaction() {
		return fn(db)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p) // re-throw panic after RollbackDatabase.
		} else if err != nil {
			if errors.Is(err, ErrIntentionalRollback) {
				err = tx.Rollback(ctx)
				return
			}

			rollbackErr := tx.Rollback(ctx)
			if rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
		} else {
			err = tx.Commit(ctx)
		}
	}()

	err = fn(tx)
	return err
}

// IsTransaction reports whether this database instance is in transaction.
func (db *DB) IsTransaction() bool {
	return db.tx != nil
}

// Begin starts a new database transaction and returns a new DB instance that operates within that transaction.
func (db *DB) Begin(ctx context.Context) (*DB, error) {
	var (
		tx  pgx.Tx // a variable to store the transaction instance
		err error  // a variable to store any error
	)
	if db.tx != nil {
		// If the DB instance already has a transaction, start a nested transaction using db.tx.Begin
		tx, err = db.tx.Begin(ctx)
	} else {
		// Otherwise, start a new transaction using db.Pool.BeginTx with the default options
		tx, err = db.Pool.BeginTx(ctx, pgx.TxOptions{
			// IsoLevel:       pgx.ReadCommitted,
			// AccessMode:     pgx.ReadWrite,
			// DeferrableMode: pgx.Deferrable,
		})
	}
	if err != nil {
		return nil, err // return nil and the wrapped error if starting the transaction fails
	}

	txDB := db.clone(tx) // clone the DB instance and assign the transaction instance to its tx field
	return txDB, nil     // return the cloned DB instance and nil as no error occurred
}

// BeginConcurrent starts a new database transaction and returns a new DB instance that operates within that transaction.
// It uses the same connection as the parent DB instance.
// It is useful when you want to execute multiple queries concurrently within the same transaction.
// This helps to avoid the "conn busy" errors when trying to read from a query while waiting for the previous one in the same connection.
// Note that a PostgreSQL connection executes statements serially. You can't concurrently send queries to the same connection.
// There is no way to have multiple concurrent writers to the same transaction natively.
func (db *DB) BeginConcurrent(ctx context.Context) (*DB, error) {
	var (
		tx  pgx.Tx // a variable to store the transaction instance
		err error  // a variable to store any error
	)
	if db.tx != nil {
		// If the DB instance already has a transaction, start a nested transaction using db.tx.Begin
		tx, err = db.tx.Begin(ctx)
	} else {
		// Otherwise, start a new concurrent transaction using db.Pool with the default transaction options.
		tx, err = NewConcurrentTx(ctx, db.Pool)
	}
	if err != nil {
		return nil, err // return nil and the wrapped error if starting the transaction fails
	}

	txDB := db.clone(tx) // clone the DB instance and assign the transaction instance to its tx field
	return txDB, nil     // return the cloned DB instance and nil as no error occurred
}

// Rollback rolls back the current database transaction and returns any error that occurs.
func (db *DB) Rollback(ctx context.Context) error {
	if db.dbTxClosed {
		return nil // return nil if the transaction is already closed due to an error or a commit
	}

	if db.tx != nil {
		// If the DB instance has a transaction, use db.tx.Rollback to roll it back
		err := db.tx.Rollback(ctx)
		if err == nil {
			// If no error occurred, set db.tx to nil and db.dbTxClosed to true
			db.tx = nil
			db.dbTxClosed = true
		}
		return err // return the error from db.tx.Rollback (nil or not)
	}

	// If the DB instance does not have a transaction, return an error indicating that rollback is not possible
	return fmt.Errorf("rollback outside of a transaction")
}

// Commit commits the current database transaction and returns any error that occurs.
func (db *DB) Commit(ctx context.Context) error {
	if db.dbTxClosed {
		return nil // return nil if the transaction is already closed due to an error or a rollback
	}

	if db.tx != nil {
		// If the DB instance has a transaction, use db.tx.Commit to commit it
		err := db.tx.Commit(ctx)
		if err == nil {
			// If no error occurred, set db.tx to nil and db.dbTxClosed to true
			db.tx = nil
			db.dbTxClosed = true
		}
		return err // return the error from db.tx.Commit (nil or not)
	}

	// If the DB instance does not have a transaction, return an error indicating that commit is not possible
	return fmt.Errorf("commit outside of a transaction")
}

// Query executes the given "query" with args.
// If there is an error the returned Rows will be returned in an error state.
func (db *DB) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	// fmt.Println(query, args)

	if db.tx != nil {
		rows, err := db.tx.Query(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("transaction: query: %w", err)
		}

		return rows, nil
	}

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	return rows, nil
}

// QueryRow is a convenience wrapper over QueryRow. Any error that occurs while
// querying is deferred until calling Scan on the returned Row. That Row will
// error with ErrNoRows if no rows are returned.
func (db *DB) QueryRow(ctx context.Context, query string, args ...any) Row {
	// fmt.Println(query, args)

	if db.tx != nil {
		return db.tx.QueryRow(ctx, query, args...)
	}

	return db.Pool.QueryRow(ctx, query, args...)
}

// QueryBoolean executes a query that returns a single boolean value and returns it as a bool and an error.
func (db *DB) QueryBoolean(ctx context.Context, query string, args ...any) (ok bool, err error) {
	err = db.QueryRow(ctx, query, args...).Scan(&ok)
	return
}

// Count executes a query that returns a single numeric value, typically a COUNT(*) or other
// aggregate, and returns it as an int64. A query that yields no rows (e.g. a COUNT wrapped in
// a GROUP BY that has nothing to group) counts as zero: ErrNoRows is swallowed and (0, nil) is
// returned instead of forcing every caller to special-case it.
func (db *DB) Count(ctx context.Context, query string, args ...any) (int64, error) {
	var count int64

	err := db.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		if IsErrNoRows(err) {
			return 0, nil
		}

		return 0, err
	}

	return count, nil
}

// Exec executes SQL. The query can be either a prepared statement name or an SQL string.
// Arguments should be referenced positionally from the sql "query" string as $1, $2, etc.
func (db *DB) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	// fmt.Println(query, args)

	if db.tx != nil {
		tag, err := db.tx.Exec(ctx, query, args...)
		if err != nil {
			return tag, fmt.Errorf("transaction: exec: %w", err)
		}

		return tag, nil
	}

	tag, err := db.Pool.Exec(ctx, query, args...)
	if err != nil {
		return tag, fmt.Errorf("exec: %w", err)
	}

	return tag, nil
}

// ExecFiles executes the SQL statements in the given files.
//
// Example:
//
//	//go:embed _embed
//	var embedDir embed.FS
//
//	[...]
//	err := db.ExecFiles(context.Background(), embedDir, "_embed/triggers.sql", "_embed/functions.sql")
func (db *DB) ExecFiles(ctx context.Context, fileReader interface {
	ReadFile(name string) ([]byte, error)
}, filenames ...string) error {
	if fileReader == nil || len(filenames) == 0 {
		return nil
	}

	type file struct {
		name     string
		contents string
	}

	files := make([]file, 0, len(filenames))
	for _, filename := range filenames {
		b, err := fileReader.ReadFile(filename)
		if err != nil {
			return err
		}

		if len(b) == 0 {
			continue
		}

		files = append(files, file{name: filename, contents: string(b)})
	}

	return db.InTransaction(ctx, func(db *DB) error {
		for _, f := range files {
			_, err := db.Exec(ctx, f.contents)
			if err != nil {
				return fmt.Errorf("exec file %s: %w", f.name, err)
			}
		}

		return nil
	})
}

// Listen listens for notifications on the given channel and returns a Listener instance.
//
// Example Code:
//
//	conn, err := db.Listen(context.Background(), channel)
//	if err != nil {
//		fmt.Println(fmt.Errorf("listen: %w\n", err))
//		return
//	}
//
//	// To just terminate this listener's connection and unlisten from the channel:
//	defer conn.Close(context.Background())
//
//	for {
//		notification, err := conn.Accept(context.Background())
//		if err != nil {
//			fmt.Println(fmt.Errorf("accept: %w\n", err))
//			return
//		}
//
//		fmt.Printf("channel: %s, payload: %s\n", notification.Channel, notification.Payload)
//	}
//
// The channel name is quoted with QuoteIdentifier before being embedded in the LISTEN
// statement. This makes Listen safe against SQL injection from a caller-supplied channel
// and also makes it consistent with Notify/pg_notify: an unquoted, unescaped LISTEN channel
// is folded to lowercase by Postgres while pg_notify's channel argument is not, so without
// quoting here a mixed-case channel passed to Listen would never match notifications sent
// via Notify.
func (db *DB) Listen(ctx context.Context, channel string) (*Listener, error) {
	conn, err := db.Pool.Acquire(ctx) // Always on top.
	if err != nil {
		return nil, err
	}

	query := "LISTEN " + QuoteIdentifier(channel)
	_, err = conn.Exec(ctx, query)
	if err != nil {
		conn.Release()
		return nil, err
	}

	l := &Listener{
		conn:    conn,
		channel: channel,
	}
	return l, nil
}

// Notify sends a notification using pg_notify to the database.
//
// See the `Listen` package-level function too.
func (db *DB) Notify(ctx context.Context, channel string, payload any) error {
	switch v := payload.(type) {
	case string:
		return notifyNative(ctx, db, channel, v)
	case []byte:
		return notifyNative(ctx, db, channel, v)
	default:
		return notifyJSON(ctx, db, channel, v)
	}
}

// Unlisten removes the given channel from the list of channels that the database is listening on.
// Available channels:
// - Any custom one
// - * (for all)
//
// The channel is quoted with QuoteIdentifier before being embedded in the UNLISTEN statement,
// since UNLISTEN's channel argument is a statement-level identifier and cannot be passed as a
// bind parameter. The special "*" wildcard is emitted as-is (unquoted), matching the "for all"
// behavior documented above; quoting it would instead target a channel literally named "*".
//
// Note that Unlisten executes on whichever connection the pool hands out for this call, which
// is not necessarily the connection a prior Listen call is subscribed on. To unsubscribe a
// specific Listener (and its specific connection), call Listener.Close instead.
func (db *DB) Unlisten(ctx context.Context, channel string) error {
	target := channel
	if channel != "*" { // "*" (UNLISTEN all channels) is a keyword-like target, not an identifier to quote.
		target = QuoteIdentifier(channel)
	}

	query := "UNLISTEN " + target
	_, err := db.Exec(ctx, query)
	return err
}

// UpdateJSONB updates a JSONB column (full or partial) in the database by building and executing an
// SQL query based on the provided values and the given tableName and columnName.
// The values parameter is a map of key-value pairs where the key is the json field name and the value is its new value,
// new keys are accepted. The tableName and columnName are resolved against the DB's schema first:
// an unknown table or column returns a descriptive error instead of reaching SQL, and the table,
// column and primary key names are all quoted with QuoteIdentifier before being embedded in the
// generated UPDATE statement.
func (db *DB) UpdateJSONB(ctx context.Context, tableName, columnName, rowID string, values map[string]any, fieldsToUpdate []string) (int64, error) {
	td, err := db.schema.GetByTableName(tableName)
	if err != nil {
		return 0, err
	}

	col := td.GetColumnByName(columnName)
	if col == nil {
		return 0, fmt.Errorf("update jsonb: unknown column %q on table %q", columnName, tableName)
	}

	primaryKey, ok := td.PrimaryKey()
	if !ok {
		return 0, fmt.Errorf("primary key is required in order to perform update jsonb on table: %s", tableName)
	}

	var (
		tag pgconn.CommandTag

		quotedTable      = QuoteIdentifier(td.Name)
		quotedColumn     = QuoteIdentifier(col.Name)
		quotedPrimaryKey = QuoteIdentifier(primaryKey.Name)
	)

	// We could extract the id from the column and do a select based on that but let's keep things simple and do it per row id.
	// id, ok := values[primaryKey.Name]
	// if !ok {
	// 	return 0, fmt.Errorf("missing primary key value")
	// }

	// Partial Update.
	if len(fieldsToUpdate) > 0 {
		/*
			// Loop over the keys and construct the path and value arrays.
			path := []string{}
			value := []any{}
			for _, key := range fieldsToUpdate {
				// Get the value for the key from the map.
				v, ok := values[key]
				if !ok {
					return 0, fmt.Errorf("missing value for key: %s", key)
				}
				// Append the key to the path array.
				path = append(path, key)
				// Append the value to the value array.
				value = append(value, v)
			}

			// Convert the path and value arrays to JSON.
			// pathJSON, jsonErr := json.Marshal(path)
			// if jsonErr != nil {
			// 	return 0, fmt.Errorf("error converting path to json: %w", jsonErr)
			// }
			valueJSON, jsonErr := json.Marshal(value)
			if jsonErr != nil {
				return 0, fmt.Errorf("error converting value to json: %w", jsonErr)
			}

			// Construct the query using jsonb_set.
			query := fmt.Sprintf("UPDATE %s SET %s = jsonb_set (%s, $1::text[], $2::jsonb, true) WHERE id = $3;", tableName, columnName, columnName)

			fmt.Println(query, path, string(valueJSON), rowID)

			// Execute the query with the path, value and id parameters.
			tag, err = db.Exec(ctx, query, path, string(valueJSON), rowID)
		*/

		// Check if all the keys are present in the map.
		for _, key := range fieldsToUpdate {
			// Get the value for the key from the map.
			_, ok := values[key]
			if !ok {
				return 0, fmt.Errorf("missing value for key: %s", key)
			}
		}

		// Delete the keys that are not present in the fieldsToUpdate slice.
		for key := range values {
			if !slices.Contains(fieldsToUpdate, key) {
				delete(values, key)
			}
		}

		query := fmt.Sprintf("UPDATE %s SET %s = %s || $1 WHERE %s = $2;", quotedTable, quotedColumn, quotedColumn, quotedPrimaryKey)
		tag, err = db.Exec(ctx, query, values, rowID)
	} else {
		// Full Update.
		query := fmt.Sprintf("UPDATE %s SET %s = $1 WHERE %s = $2;", quotedTable, quotedColumn, quotedPrimaryKey)
		tag, err = db.Exec(ctx, query, values, rowID)
	}
	if err != nil {
		return 0, fmt.Errorf("update jsonb: %w", err)
	}

	return tag.RowsAffected(), nil
}
