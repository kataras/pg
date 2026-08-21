# Architecture

## The three packages

| Package | Owns | Depends on |
| --- | --- | --- |
| `pg` (root, `github.com/kataras/pg`) | `DB` (connection/pool/transaction/search-path wrapper), the generic `Repository[T]`, `Schema` (the type-to-table registry), `Conditions`/`Where` (WHERE-clause builder), `PageOptions`/`SelectPaginated`, LISTEN/NOTIFY, error classification (`AsConstraintError`, `IsErrDuplicate`, ...), retry (`InTransactionRetry`), introspection (`ListTables`, `ListColumns`, ...), migrations (`DB.Migrate`) | `desc`, `jackc/pgx/v5` |
| `desc` (`github.com/kataras/pg/desc`) | `Table`/`Column` descriptors, `pg:"..."` struct-tag parsing (`ConvertStructToTable`), every SQL query builder (`Build*Query` functions), row scanning (`Table.RowsToStruct`, `ConvertRowsToStruct`), `LooseTable` (schema-independent descriptors for ad-hoc scan targets) | `jackc/pgx/v5` only |
| `gen` (`github.com/kataras/pg/gen`) | Code generation: Go struct definitions from a live database (`GenerateSchemaFromDatabase`), typed column-name constants from a registered `Schema` (`GenerateColumnsFromSchema`) | `pg`, `desc` (transitively) |

`pgtest` (`github.com/kataras/pg/pgtest`) is a fourth, test-only subpackage: `pgtest.New` gives a test an ephemeral, per-test schema. It is meant to be imported only from `_test.go` files. `book/` is a fifth, entirely separate Go module (its own `go.mod`) holding the long-form book and its PDF generator; it never affects the library's own `go.mod` and is not a package other code imports.

Almost every application only imports `pg` directly. `desc` is exported mainly so `gen` and callers with unusual needs (a custom `TableFilter`, a hand-built descriptor) can reach it; most `desc` types are re-exported as aliases from the root package (`pg.Table = desc.Table`, `pg.Column = desc.Column`, `pg.DataType = desc.DataType`, `pg.ColumnFilter = desc.ColumnFilter`, `pg.OnConflict = desc.OnConflict`, `pg.TableFilterFunc = desc.TableFilterFunc`), so most code never writes `desc.` at all.

## Schema, Table and Column

A `*pg.Schema` (`schema.go`) is a registry: `structCache map[reflect.Type]*desc.Table`, `tableNameCache map[string]*desc.Table`, and `orderedTypes` (registration order, used to sort `Tables()`), all guarded by one `sync.RWMutex`. `Schema.Register(tableName, emptyStructValue, opts...)` (or the panicking `MustRegister`) calls `desc.ConvertStructToTable(tableName, reflect.TypeOf(emptyStructValue))`, which validates the table name against `identifierRegex` (`^[A-Za-z_][A-Za-z0-9_$]*$`), walks the struct's exported fields via `lookupFields` (`desc/reflect.go`, which flattens an untagged nested struct's fields but treats a `time.Time` or a `type=json`-tagged nested struct as a single column), and converts each tagged field to a `*desc.Column` via `convertStructFieldToColumnDefinion` (`desc/struct_table.go`), which parses every comma-separated `pg:"..."` tag option (`type`, `primary`, `default`, `unique`, `unique_index`, `ref`, `index`, `check`, `generated`, `conflict`, `password`, `username`, ...) and validates the resolved column name and any `unique_index` name the same way. The resulting `*desc.Table` is cached under both the struct's `reflect.Type` and its table name, and each `*desc.Column.Table` points back at it.

A `*desc.Table` also comes from two other paths that never touch a `*pg.Schema`: `db.ListTables` (`db_information.go`) builds one per table from live `information_schema`/`pg_catalog` introspection queries (used by `gen` and by `DB.CheckSchema`), and `desc.LooseTable` (`desc/loose_table.go`) builds a cached, schema-independent one by reflection alone, for scanning an ad-hoc struct that was never registered (`DB.QueryStructs`/`DB.QueryStruct`/`pg.ScanStructs`).

## The *DB vs Repository[T] split

`*pg.DB` (`db.go`) wraps a `*pgxpool.Pool`, the connection's `*pgx.ConnConfig`, the resolved `search_path`, an optional `pgx.Tx` (nil outside a transaction), a `*Schema`, and a shared `*tableNotifyState` (LISTEN/NOTIFY trigger bookkeeping). It is the connection- and schema-level API: `Query`/`QueryRow`/`Exec`/`Count`/`Mutate`, transaction control (`Begin`, `InTransaction`, `BeginConcurrent`, `InTransactionRetry`), struct-typed CRUD keyed by `reflect.Type` (`db_repository.go`: `Select`, `SelectByID`, `Exists`, `Insert`, `InsertSingle`, `Upsert`, `UpsertSingle`, `Delete`, `Update`, `Duplicate`; every one of these does a `db.schema.Get(reflect.TypeOf(value))` lookup internally), table-name-keyed CRUD (`db_crud.go`: `DeleteByID`, `DeleteBy`, `ExistsBy`, `CountBy`, `SelectSingle`, resolved via `db.schema.GetByTableName`), introspection (`db_information.go`), migrations (`migrate.go`), and LISTEN/NOTIFY (`listener.go`, `db_table_listener.go`).

`Repository[T]` (`repository.go`) is a thin, compile-time-typed wrapper: `NewRepository[T](db)` resolves and caches `T`'s `*desc.Table` once (`db.schema.Get(reflect.TypeOf(value))`), **panicking** if `T` was never registered, a fail-fast choice made once at construction instead of on every call. Every `Repository[T]` method (`Select`, `SelectSingle`, `SelectByID`, `Insert`, `InsertMany`, `Upsert`, `UpsertMany`, `Delete`, `DeleteByID`, `Update`, `UpdateOnlyColumns`, `InTransaction`, `SelectIter`, `SelectPaginated`, `CopyFrom`, `InsertOnConflict`, ...) delegates to the equivalent `*DB` method or `desc` builder using its own cached `td` instead of doing a fresh `Schema` lookup per call, and returns `[]T`/`T` instead of taking a `destPtr`. `Repository[T].DB()` and `.Table()` expose the underlying `*DB` and `*desc.Table` for anything not wrapped.

## The request path of a query

`Repository[T].Select(ctx, query, args...)`:

1. `repo.db.Query(ctx, query, args...)` (`db.go`): if `db.tx != nil` (transaction-scoped `*DB`), routes to `db.tx.Query`; otherwise `db.Pool.Query`. Returns `pgx.Rows`.
2. `repo.td.RowsToStruct[T](rows)` (`desc/scanner.go`): builds a case-insensitive `map[string]*Column` once via `buildColumnLookup(td)` (an O(1) lookup per column instead of `GetColumnByName`'s O(n) linear scan, paid once per query, not once per row), then for each row calls `convertRowsToStruct`.
3. `convertRowsToStruct` calls `findScanTargets`, which walks the row's `pgconn.FieldDescription`s, looks each one up in the column map, and picks a scan target per column: a password column with a decrypting `PasswordHandler` gets a `passwordTextScanner`; a non-pointer nullable `UUID`/`Text`/`CharacterVarying` column gets a `nullableScanner` (which assigns, converts, or - for a field whose pointer implements `encoding.TextUnmarshaler`, such as a `uuid.UUID` - parses the driver's canonical text form, since pgx hands a `sql.Scanner` the uuid *string*, which is neither assignable nor convertible to a `[16]byte`-based type); a non-pointer nullable `JSON`/`JSONB` column (that does not already implement `sql.Scanner`) gets a `jsonScanner` (which decodes with `encoding/json/v2` plus `json.MatchCaseInsensitiveNames(true)`, required because PostgreSQL emits lower-cased column names while the target structs carry `pg` tags rather than `json` ones, and v2 matches names exactly by default); everything else gets the struct field's address directly, via `reflect.Value.FieldByIndex(col.FieldIndex).Addr().Interface()`. A row column with no matching struct field becomes a `noOpScanner` when `td.Strict` is false, or an error when it is true.
4. `rows.Scan(scanTargets...)` does the actual per-column decode; `scanRow` enriches a `pgx.ScanArgError` with the offending struct field and column name before returning it, so a type-mismatch error names both sides.

`Repository[T].SelectSingle` and `db.SelectByID` follow the same path through `desc.Table.RowToStruct`/`ConvertRowsToStruct`, differing only in how many rows are consumed and how the query itself is built. `Repository[T].SelectIter` (`repository_iter.go`) reuses the same `desc.ConvertRowsToStruct` call per row instead of `RowsToStruct`, so it never materializes a `[]T`. It is the streaming form for exports and large scans.

## Where SQL is actually built

Every builder lives in `desc`, takes a `*desc.Table` (plus, for a write, a `reflect.Value` of the struct or a primary-key value), and returns `(query string, args []any, err error)` unless noted. None of them execute anything; the caller (almost always a `*DB`/`Repository[T]` method) passes the result to `db.Query`/`db.QueryRow`/`db.Exec`.

| Builder file | Builds |
| --- | --- |
| `desc/insert_query.go` | `BuildInsertQuery` (single-row INSERT, tag-derived ON CONFLICT), `BuildBulkInsertQuery` (multi-row INSERT), shared column-list/RETURNING/password helpers used by every insert builder below |
| `desc/on_conflict.go` | `BuildInsertQueryOnConflict`, `BuildBulkInsertQueryOnConflict`: INSERT with an explicit `OnConflict{Columns/Constraint, DoNothing, SetColumns, SetWhere}` instead of tag-derived behavior |
| `desc/update_query.go` | `BuildUpdateQuery` (single-row UPDATE by primary key, optional column subset) |
| `desc/delete_query.go` | `BuildDeleteQuery` (`DELETE ... WHERE pk = ANY($1)` for one or more values) |
| `desc/exists_query.go` | `BuildExistsQuery` (`SELECT EXISTS(...)` matching a struct's non-zero fields) |
| `desc/duplicate_query.go` | `BuildDuplicateQuery` (`INSERT ... SELECT` cloning a row by primary key) |
| `desc/create_table_query.go` | `BuildCreateTableQuery` (`CREATE TABLE IF NOT EXISTS`, columns, PRIMARY KEY, UNIQUE constraints, `CREATE INDEX` statements) |
| `desc/alter_table_constraint_query.go` | `BuildAlterTableForeignKeysQueries` (`ALTER TABLE ... ADD CONSTRAINT ... FOREIGN KEY`) |
| `desc/order_by.go` | `Table.OrderBy` (validate-then-quote a caller-supplied sort column into an `ORDER BY` fragment) |
| `desc/select_expr.go` | `Table.SelectColumnsExpr`, `Table.JSONBuildObjectExpr` (hand-written-query helpers that stay in sync with a struct's scannable columns) |
| `desc/copy_from.go` | `BuildCopyPlan`/`CopyPlan.Row` (COPY protocol column list and per-row value extraction; not a SQL string) |
| `where.go` (root package) | `Conditions`/`Where` (placeholder-renumbering WHERE-clause fragment builder, `Build`/`Args`/`NextIndex`) |
| `pagination.go` (root package) | `buildPaginatedQuery` (appends `ORDER BY`/`LIMIT`/`OFFSET` onto a caller's query) |
| `db_crud.go` (root package) | `whereClauseFromPairs` (schema-validated `WHERE "c1" = $1 AND "c2" = $2` for the table-name CRUD) |
| `db_information.go` (root package) | Hand-written introspection queries against `information_schema`/`pg_catalog` (`ListColumns`, `ListConstraints`, `ListUniqueIndexes`, `ListTriggers`) |
| `migrate.go` (root package) | The tracking-table DDL and per-file `Exec` inside `DB.Migrate` |

The golden tests in `desc` (`desc/insert_query_test.go`, `desc/create_table_query_test.go`, and siblings) assert the exact SQL string each of these emits: see `testing.md`.

## How transactions clone the DB

`DB.Begin(ctx)` starts a `pgx.Tx` (`db.tx.Begin` for a nested/savepoint transaction if `db.tx` is already set, otherwise `db.Pool.BeginTx`) and returns `db.clone(tx)`: a new `*DB` sharing the same `Pool`, `ConnectionOptions`, `schema`, `searchPath` and `*tableNotifyState` pointer, with `tx` set to the new transaction. Every `Query`/`QueryRow`/`Exec` on that cloned `*DB` checks `db.tx != nil` and routes to the transaction instead of the pool; `DB.IsTransaction()` reports the same check. `DB.InTransaction(ctx, fn)` wraps `Begin` with commit/rollback bookkeeping: `fn` returning `nil` commits, returning `pg.ErrIntentionalRollback` rolls back and returns `nil`, returning any other error rolls back and returns that error (joined with a rollback error via `errors.Join` if the rollback itself also fails), and a panic inside `fn` rolls back and re-panics. Calling `InTransaction` again on an already-transactional `*DB` does not nest: it short-circuits straight to `fn(db)`, so an inner `ErrIntentionalRollback` propagates out to whichever `InTransaction` call is actually managing the transaction; call `DB.Begin` directly for an independently committable/rollback-able savepoint. `DB.BeginConcurrent` is the same clone, but wraps the `pgx.Tx` in a `*ConcurrentTx` (`concurrent_tx.go`) that mutexes every method, for safely sharing one transaction (and its single underlying connection) across goroutines. A PostgreSQL connection still executes statements serially, so this buys safety, not parallelism, on that transaction. `Repository[T].InTransaction`/`InTransactionRetry` are the same short-circuit-or-clone logic, one level up: they rebuild a `*Repository[T]` around the (possibly newly transactional) `*DB`, reusing the same cached `td`.
