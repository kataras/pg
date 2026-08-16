// Package pg is a PostgreSQL client library built on top of pgx. It maps Go structs to
// database tables through `pg:"..."` struct tags, generates the SQL needed to create and
// verify a schema, and exposes a generic repository for CRUD operations, so most
// applications never hand-write SQL for everyday queries.
//
// # Declaring a schema
//
// An entity is a plain Go struct whose fields carry a `pg` struct tag describing the
// column: its name, PostgreSQL type, constraints and default value. A Schema collects
// entities under table names:
//
//	type Customer struct {
//		ID        string    `pg:"type=uuid,primary"`
//		Firstname string    `pg:"type=varchar(255)"`
//	}
//
//	schema := pg.NewSchema()
//	schema.MustRegister("customers", Customer{})
//
// SetDefaultTag changes the struct tag name used by later Register calls, and
// SetDefaultColumnNameMapper changes how a field name becomes a column name when its tag
// omits an explicit "name" option.
//
// # Connecting and managing the schema
//
// Open parses a PostgreSQL connection string and returns a *DB bound to the given Schema;
// OpenPool builds a *DB from an already-configured pgxpool.Pool. CreateSchema issues the
// DDL for every registered table, and CheckSchema verifies that a live database's schema
// still matches the registered Go structs.
//
// # Repository CRUD
//
// NewRepository[T] returns a typed Repository[T] bound to T's registered table, exposing
// Select/SelectSingle/SelectByID, Insert/InsertSingle, Update/UpdateOnlyColumns,
// Upsert/UpsertSingle and Delete/DeleteByID. Multi-row Insert and Upsert calls (and their
// explicit InsertMany/UpsertMany counterparts) batch rows into multi-row statements
// instead of issuing one round-trip per row.
//
// # Transactions
//
// DB.InTransaction (and Repository[T].InTransaction) run a function inside a database
// transaction, committing on a nil return and rolling back otherwise; returning
// ErrIntentionalRollback rolls back without surfacing an error to the caller. Calling
// InTransaction again from inside an already-open transaction does not start a new
// transaction or savepoint: it simply joins the existing one and runs fn directly, so an
// inner error (including ErrIntentionalRollback) propagates out to whichever
// InTransaction call is managing that transaction, rather than rolling back only the
// inner call's work. For an independent, separately committable/rollback-able unit of
// work nested inside an existing transaction, call DB.Begin directly: on an already
// transactional *DB it opens a savepoint-backed subtransaction (via pgx's nested
// Tx.Begin) with its own Commit/Rollback.
//
// # LISTEN/NOTIFY
//
// DB.Listen, DB.Notify and DB.Unlisten wrap PostgreSQL's LISTEN/NOTIFY. DB.ListenTable
// (and Repository[T].ListenTable) go further: they install a trigger and notify function
// per table and deliver each INSERT/UPDATE/DELETE as a typed TableNotification value.
//
// # Introspection and code generation
//
// DB also exposes introspection helpers (ListTables, ListColumns, ListConstraints,
// GetVersion and others) for inspecting a live database. The descriptors and SQL
// builders behind all of the above (table/column definitions, `pg` tag parsing, query
// builders and row scanning) live in the desc subpackage, used by most callers only
// indirectly through this package. The gen subpackage generates Go struct definitions
// from a live database schema, or column-constant files from an already-registered
// Schema.
package pg
