package pg

import (
	"context"
	"errors"
	"fmt"
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

// SetKeepColumnName sets the default column name conversion function.
// If true then the column name will not be converted to snake_case.
//
// Further modifications can be done by setting the `desc.ToColumnName` package-level variable function.
//
// Defaults to snake_case.
func SetKeepColumnName(b bool) {
	if b {
		desc.ToColumnName = func(fieldName string) string { return fieldName }
	} else {
		desc.ToColumnName = desc.SnakeCase
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
	// TableFilter is a type alias for desc.TableFilter.
	TableFilterFunc = desc.TableFilterFunc
)

// DB represents a database connection that can execute queries and transactions.
// It wraps a pgxpool.Pool and a pgx.ConnConfig to manage the connection options and the search path.
// It also holds a reference to a Schema that defines the database schema and migrations.
type DB struct {
	Pool              *pgxpool.Pool
	ConnectionOptions *pgx.ConnConfig
	searchPath        string

	tx         pgx.Tx
	dbTxClosed bool

	schema *Schema
}

// ConnectionOption is a function that takes a *pgxpool.Config and returns an error.
// It is used to set the connection options for the connection pool.
// It is used by the Open function.
//
// See `WithLogger` package-level function too.
type ConnectionOption func(*pgxpool.Config) error

// WithLogger is a ConnectionOption. It sets the logger for the connection pool.
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

// Open creates a new DB instance by parsing the connection string and establishing a connection pool.
// It also sets the search path to the one specified in the connection string or to the default one if not specified.
// It takes a context and a schema as arguments and returns the DB instance or an error if any.
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
		return nil, err
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
		return nil, fmt.Errorf("open: %w: full connection string: <%s>", err, connString)
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
	}

	return clone
}

func (db *DB) SearchPath() string {
	return db.searchPath
}

// ErrIntentionalRollback is an error that can be returned by a transaction function to rollback the transaction.
var ErrIntentionalRollback = errors.New("skip error: intentional rollback")

// InTransaction runs a function within a database transaction and commits or rolls back depending on
// the error value returned by the function.
// Note that:
// After the first error in the transaction, the transaction is rolled back.
// After the first error in query execution, the transaction is aborted and
// no new commands should be sent through the same transaction.
func (db *DB) InTransaction(ctx context.Context, fn func(*DB) error) error {
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
				err = fmt.Errorf("%w: %s", err, rollbackErr.Error())
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
func (db *DB) Listen(ctx context.Context, channel string) (*Listener, error) {
	conn, err := db.Pool.Acquire(ctx) // Always on top.
	if err != nil {
		return nil, err
	}

	query := `LISTEN ` + channel
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
func (db *DB) Unlisten(ctx context.Context, channel string) error {
	query := `SELECT UNLISTEN $1;`
	_, err := db.Exec(ctx, query, channel)
	return err
}
