# API Map

Every signature below was read from the current source, not copied from a prior report or
from memory. If a call site does not compile against one of these, the source has moved -
re-check it rather than trusting this table. `pg.` is `github.com/kataras/pg`; `desc.` is
`github.com/kataras/pg/desc`; `gen.` is `github.com/kataras/pg/gen`; `pgtest.` is
`github.com/kataras/pg/pgtest`. Aliases: `pg.Row`/`pg.Rows` = `pgx.Row`/`pgx.Rows`,
`pg.Table`/`pg.Column`/`pg.DataType`/`pg.ColumnFilter`/`pg.TableFilterFunc`/`pg.OnConflict`
are the matching `desc.` types, `pg.Identifier` = `pgx.Identifier`, `pg.CopyFromSource` =
`pgx.CopyFromSource`.

## Connect

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `pg.Open` | `func Open(ctx context.Context, schema *Schema, connString string, opts ...ConnectionOption) (*DB, error)` | Parse `connString`, open a `pgxpool.Pool`, `Ping`, return a `*DB` |
| `pg.OpenPool` | `func OpenPool(schema *Schema, pool *pgxpool.Pool) *DB` | Wrap an already-configured `*pgxpool.Pool` |
| `(*DB).Close` | `func (db *DB) Close()` | Close the pool |
| `pg.ConnectionOption` | `type ConnectionOption func(*pgxpool.Config) error` | Functional option passed to `Open` |
| `pg.WithLogger` | `func(logger tracelog.Logger) ConnectionOption` | Installs a pgx tracer at `tracelog.LogLevelTrace` (logs every statement and bind arg, passwords included); never use in production |
| `pg.WithLoggerLevel` | `func WithLoggerLevel(logger tracelog.Logger, level tracelog.LogLevel) ConnectionOption` | Same tracer, caller-chosen level; `LogLevelNone` is the only level that never logs a bind argument |
| `pg.WithQueryTracer` | `func WithQueryTracer(tracers ...pgx.QueryTracer) ConnectionOption` | Compose additional `pgx.QueryTracer`s (e.g. OpenTelemetry) via `multitracer`; put before `WithLogger`/`WithLoggerLevel` or it gets overwritten |
| `pg.WithDefaultQueryExecMode` | `func WithDefaultQueryExecMode(mode pgx.QueryExecMode) ConnectionOption` | e.g. `pgx.QueryExecModeSimpleProtocol` for PgBouncer transaction pooling |
| `pg.WithStatementCacheCapacity` | `func WithStatementCacheCapacity(n int) ConnectionOption` | 0 disables the prepared-statement cache |
| `pg.WithDescriptionCacheCapacity` | `func WithDescriptionCacheCapacity(n int) ConnectionOption` | Statement-description cache size for describe exec mode |
| `pg.SetDefaultTag` | `func SetDefaultTag(tag string)` | Change the struct tag name later `Register` calls read (default `"pg"`) |
| `pg.SetDefaultSearchPath` | `func SetDefaultSearchPath(searchPath string)` | Change the default search path (default `"public"`) captured at `Register` time |
| `pg.SetDefaultColumnNameMapper` | `func SetDefaultColumnNameMapper(fn func(field reflect.StructField) string)` | Change how a field name becomes a column name when the tag has no `name=` |
| `pg.NoColumnNameMapper`, `pg.JSONColumnNameMapper` | `func(field reflect.StructField) string` (vars) | Alternate mappers: field name as-is, or the field's `json` tag name |
| `(*DB).SearchPath` | `func (db *DB) SearchPath() string` | The resolved search path |
| `(*DB).Schema` | `func (db *DB) Schema() *Schema` | The bound `*Schema` (do not mutate) |
| `(*DB).Query` | `func (db *DB) Query(ctx context.Context, query string, args ...any) (Rows, error)` | Routes to `db.tx` or `db.Pool` |
| `(*DB).QueryRow` | `func (db *DB) QueryRow(ctx context.Context, query string, args ...any) Row` | Errors (including `ErrNoRows`) are deferred to `.Scan` |
| `pg.QuoteIdentifier` | `func QuoteIdentifier(identifier string) string` | `pgx.Identifier{identifier}.Sanitize()`; the one escaping path for identifiers |

## Define schema

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `pg.NewSchema` | `func NewSchema() *Schema` | New registry; `Strict: true`, `UpdatedAtColumnName: "updated_at"`, `SetTimestampTriggerName: "set_timestamp"` |
| `(*Schema).Register` | `func (s *Schema) Register(tableName string, emptyStructValue any, opts ...TableFilterFunc) (*desc.Table, error)` | Validate + parse tags, cache by type and by name |
| `(*Schema).MustRegister` | `func (s *Schema) MustRegister(tableName string, emptyStructValue any, opts ...TableFilterFunc) *Schema` | Same, panics on error, returns `*Schema` for chaining |
| `(*Schema).HandlePassword` | `func (s *Schema) HandlePassword(handler desc.PasswordHandler) *Schema` | Install a Go-side password encrypt/decrypt handler |
| `pg.View` | `var View TableFilterFunc` | Pass as an opt to `Register`: marks the table `TableTypeView` (read-only) |
| `pg.Presenter` | `var Presenter TableFilterFunc` | Marks the table `TableTypePresenter` (not a base table or view) |
| `(*Schema).Get` | `func (s *Schema) Get(typ reflect.Type) (*desc.Table, error)` | Lookup by (dereferenced) `reflect.Type` |
| `(*Schema).GetByTableName` | `func (s *Schema) GetByTableName(tableName string) (*desc.Table, error)` | Lookup by table name (O(1) map) |
| `(*Schema).Tables` | `func (s *Schema) Tables(types ...desc.TableType) []*desc.Table` | Sorted by registration order; filtered by type if given |
| `(*Schema).TableNames` | `func (s *Schema) TableNames(types ...desc.TableType) []string` | Names in the same order as `Tables` |
| `(*Schema).Last` | `func (s *Schema) Last() *desc.Table` | Most recently registered table |
| `(*Schema).HasColumnType` | `func (s *Schema) HasColumnType(dataTypes ...desc.DataType) bool` | Used internally to decide which `CREATE EXTENSION` statements `CreateSchema` needs |
| `(*Schema).HasPassword` | `func (s *Schema) HasPassword() bool` | True if any registered column has `password` set |
| `desc.PasswordHandler` | `struct { Encrypt func(tableName, plainPassword string) (string, error); Decrypt func(tableName, encryptedPassword string) (string, error) }` | Set only `Encrypt` for a one-way (recommended) handler |
| struct tag options (`pg:"..."`) | `name`, `type` (with `(arg)`, e.g. `varchar(255)`), `primary`/`pk`, `identity`, `default=`, `unique`, `unique_index=`, `conflict=`, `username`, `password`, `nullable`/`null`, `ref=`/`reference=`/`references=` (`table(col [action] [deferrable])`), `index=`, `check=`, `generated=`, `auto`, `presenter`, `unscannable` | Parsed by `desc.convertStructFieldToColumnDefinion`; see `security.md` for which of these are developer-authored SQL |

## Read

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `(*Repository[T]).Select` | `func (repo *Repository[T]) Select(ctx context.Context, query string, args ...any) ([]T, error)` | Run `query`, scan every row into `T` |
| `(*Repository[T]).SelectSingle` | `func (repo *Repository[T]) SelectSingle(ctx context.Context, query string, args ...any) (T, error)` | First row, or `ErrNoRows` |
| `(*Repository[T]).SelectByID` | `func (repo *Repository[T]) SelectByID(ctx context.Context, id any) (T, error)` | `SELECT * ... WHERE <pk> = $1` |
| `(*Repository[T]).SelectByUsernameAndPassword` | `func (repo *Repository[T]) SelectByUsernameAndPassword(ctx context.Context, username, plainPassword string) (T, error)` | Server-side `crypt()` comparison against the `password` column |
| `(*Repository[T]).Exists` | `func (repo *Repository[T]) Exists(ctx context.Context, value T) (bool, error)` | Matches `value`'s non-zero fields |
| `(*Repository[T]).Count` | `func (repo *Repository[T]) Count(ctx context.Context, query string, args ...any) (int64, error)` | `ErrNoRows` from `query` counts as 0 |
| `(*DB).Select` | `func (db *DB) Select(ctx context.Context, scannerFunc func(Rows) error, query string, args ...any) error` | Hand-rolled row loop; `scannerFunc` gets the open `Rows` |
| `(*DB).SelectByID` | `func (db *DB) SelectByID(ctx context.Context, destPtr any, id any) error` | `destPtr`'s type resolved via `Schema.Get` |
| `(*DB).SelectByUsernameAndPassword` | `func (db *DB) SelectByUsernameAndPassword(ctx context.Context, destPtr any, username, plainPassword string) error` | |
| `(*DB).SelectSingle` | `func (db *DB) SelectSingle(ctx context.Context, destPtr any, query string, args ...any) error` | `destPtr`'s type must be registered; `query` is caller-authored |
| `(*DB).Exists` | `func (db *DB) Exists(ctx context.Context, value any) (bool, error)` | |
| `(*DB).QueryBoolean` | `func (db *DB) QueryBoolean(ctx context.Context, query string, args ...any) (ok bool, err error)` | Scans a single bool column |
| `(*DB).Count` | `func (db *DB) Count(ctx context.Context, query string, args ...any) (int64, error)` | `ErrNoRows` swallowed to 0 |
| `pg.QueryStructs[T]` | `func QueryStructs[T any](ctx context.Context, db *DB, query string, args ...any) ([]T, error)` | `T` need not be registered; falls back to `desc.LooseTable` |
| `pg.QueryStruct[T]` | `func QueryStruct[T any](ctx context.Context, db *DB, query string, args ...any) (T, error)` | Same, single row |
| `pg.ScanStructs[T]` | `func ScanStructs[T any](rows Rows) ([]T, error)` | Scans an already-open `Rows` (no `*DB`), always via `LooseTable` |
| `desc.LooseTable` | `func LooseTable(typ reflect.Type) (*Table, error)` | Cached, schema-independent descriptor for an ad-hoc struct; every column `Nullable`, no `pg` tag required |
| `pg.QuerySlice[T]` | `func QuerySlice[T any](ctx context.Context, db *DB, query string, args ...any) ([]T, error)` | Single-column scan into `T`; empty strings are dropped when `T` is `string` |
| `pg.QueryTwoSlices[T, V]` | `func QueryTwoSlices[T, V any](ctx context.Context, db *DB, query string, args ...any) ([]T, []V, error)` | Two-column scan into two parallel slices |
| `pg.QueryMap[K, V]` | `func QueryMap[K comparable, V any](ctx context.Context, db *DB, query string, args ...any) (map[K]V, error)` | Two-column scan into a map; later duplicate keys win |
| `pg.QuerySingle[T]` | `func QuerySingle[T any](ctx context.Context, db *DB, query string, args ...any) (entry T, err error)` | Single value, single row |
| `pg.QueryFunc[T]` / `pg.ScanFunc[T]` | `func QueryFunc[T any](ctx context.Context, db *DB, scan ScanFunc[T], query string, args ...any) ([]T, error)`; `type ScanFunc[T any] func(rows Rows) (T, error)` | Ad-hoc row shape that fits neither a scalar nor a registered struct |

## Filter / sort / paginate

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `pg.Where` | `func Where(fragment string, args ...any) *Conditions` | Start a `Conditions` builder |
| `(*Conditions).And` | `func (c *Conditions) And(fragment string, args ...any) *Conditions` | Append an AND-joined raw SQL fragment; `$1..$n` are call-local |
| `(*Conditions).AndIf` | `func (c *Conditions) AndIf(cond bool, fragment string, args ...any) *Conditions` | `And` only when `cond` |
| `(*Conditions).AndAnyOf` | `func (c *Conditions) AndAnyOf(column, elemType string, values any) *Conditions` | `column = ANY($1::elemType[])`, NULL/empty-safe |
| `(*Conditions).AndMatchAnyOf` | `func (c *Conditions) AndMatchAnyOf(match, elemType string, values any) *Conditions` | Same NULL/empty gate, caller-supplied `match` expression (EXISTS/subquery) |
| `(*Conditions).AndNameMatchAnyOf` | `func (c *Conditions) AndNameMatchAnyOf(matchExpr string, names []string) *Conditions` | Multi-name search over `unnest($1::varchar[])` |
| `(*Conditions).AndSearch` | `func (c *Conditions) AndSearch(term string, substring bool, tsvExpr, textExpr string) *Conditions` | `ILIKE` (substring) or `plainto_tsquery` (full-text); disabled when `term == ""` |
| `(*Conditions).AndOptionalEq` | `func (c *Conditions) AndOptionalEq(column string, value any) *Conditions` | Zero-gated equality (empty string / zero number = disabled) |
| `(*Conditions).AndMin` / `AndMax` | `func (c *Conditions) AndMin(column string, value any) *Conditions` (Max analogous) | Bound filter, ignored when `value <= 0` |
| `(*Conditions).Build` | `func (c *Conditions) Build(startIndex int) (clause string, args []any)` | Renders one parenthesized AND-joined clause; empty builder renders `"TRUE"` |
| `(*Conditions).Args` | `func (c *Conditions) Args() []any` | Accumulated args without renumbering |
| `(*Conditions).NextIndex` | `func (c *Conditions) NextIndex(startIndex int) int` | First free `$N` after `Build(startIndex)` |
| `(*Repository[T]).OrderBy` | `func (repo *Repository[T]) OrderBy(column string, descending bool, extraColumns ...string) (string, error)` | Validate-then-quote a caller-supplied sort column; falls back to `created_at`/`updated_at`/primary key when `column == ""` |
| `desc.Table.OrderBy` | `func (td *Table) OrderBy(column string, descending bool, extraColumns ...string) (string, error)` | Same, lower-level |
| `pg.PageOptions` | `struct { Limit, Offset int64; OrderBy string; WithoutTotal bool }` | `Limit`/`Offset` `<= 0` add no clause; `OrderBy` is interpolated, must come from `OrderBy` above or a trusted literal |
| `(*Repository[T]).SelectPaginated` | `func (repo *Repository[T]) SelectPaginated(ctx context.Context, page PageOptions, query string, args ...any) ([]T, int64, error)` | Derives `SELECT COUNT(*) FROM (query)`; total `0` short-circuits without running the page query; `WithoutTotal` reports `-1` |
| `(*Repository[T]).SelectWithTotal` | `func (repo *Repository[T]) SelectWithTotal(ctx context.Context, query string, args ...any) ([]T, int64, error)` | `query` must itself select `COUNT(*) OVER() AS total_count`; no ORDER BY/LIMIT/OFFSET appended |
| `desc.Table.SelectColumnsExpr` | `func (td *Table) SelectColumnsExpr(alias string) string` | `f."id",f."name"` for hand-written SELECTs that must track the struct |
| `desc.Table.JSONBuildObjectExpr` | `func (td *Table) JSONBuildObjectExpr(alias string) string` | `json_build_object('id', f."id", ...)` |

## Write

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `(*Repository[T]).Insert` | `func (repo *Repository[T]) Insert(ctx context.Context, values ...T) error` | 1 value -> `InsertSingle`; 2+ -> `InsertMany` |
| `(*Repository[T]).InsertSingle` | `func (repo *Repository[T]) InsertSingle(ctx context.Context, value T, idPtr any) error` | `idPtr` non-nil scans back the primary key |
| `(*Repository[T]).Upsert` | `func (repo *Repository[T]) Upsert(ctx context.Context, forceOnConflictExpr string, values ...T) error` | 1 value -> `UpsertSingle`; 2+ -> `UpsertMany` |
| `(*Repository[T]).UpsertSingle` | `func (repo *Repository[T]) UpsertSingle(ctx context.Context, forceOnConflictExpr string, value T, idPtr any) error` | `forceOnConflictExpr`: `""` = tag-derived target, `pg.DoNothing` = force DO NOTHING, anything else = a unique index/column name to force a DO UPDATE target |
| `pg.DoNothing` | `const DoNothing = "DO NOTHING"` | Pass as `forceOnConflictExpr` |
| `(*Repository[T]).InsertOnConflict` | `func (repo *Repository[T]) InsertOnConflict(ctx context.Context, oc OnConflict, values ...T) error` | Explicit `OnConflict`, batched, never RETURNING |
| `(*Repository[T]).InsertSingleOnConflict` | `func (repo *Repository[T]) InsertSingleOnConflict(ctx context.Context, oc OnConflict, value T, idPtr any) error` | `idPtr` non-nil always adds RETURNING; a skipped DO NOTHING row surfaces as `ErrNoRows` |
| `pg.OnConflict` | `struct { Columns []string; Constraint string; DoNothing bool; SetColumns []string; SetWhere string }` | `SetWhere` is developer-authored SQL, appended verbatim |
| `pg.UpdateOrInsert[R]` | `func UpdateOrInsert[R any](ctx context.Context, db *DB, updateQuery, insertQuery string, args []any, insertExtraArgs ...any) (R, error)` | Check-then-act: try `updateQuery` first, `insertQuery` (with `args` + `insertExtraArgs`) on `ErrNoRows`; both must `RETURNING` a single `R` |
| `(*Repository[T]).Update` | `func (repo *Repository[T]) Update(ctx context.Context, values ...T) (int64, error)` | Full update by primary key |
| `(*Repository[T]).UpdateExceptColumns` | `func (repo *Repository[T]) UpdateExceptColumns(ctx context.Context, columnsToExcept []string, values ...T) (int64, error)` | |
| `(*Repository[T]).UpdateOnlyColumns` | `func (repo *Repository[T]) UpdateOnlyColumns(ctx context.Context, columnsToUpdate []string, values ...T) (int64, error)` | `nil` means full update |
| `(*Repository[T]).UpdateOnlyColumnsReportNoRows` | `func (repo *Repository[T]) UpdateOnlyColumnsReportNoRows(ctx context.Context, columnsToUpdate []string, values ...T) (bool, error)` | `false, nil` (not an error) when no row matched |
| `(*Repository[T]).Delete` | `func (repo *Repository[T]) Delete(ctx context.Context, values ...T) (int64, error)` | By primary key of each value |
| `(*Repository[T]).DeleteByID` | `func (repo *Repository[T]) DeleteByID(ctx context.Context, id any) (bool, error)` | |
| `(*Repository[T]).Duplicate` | `func (repo *Repository[T]) Duplicate(ctx context.Context, id any, newIDPtr any) error` | `INSERT ... SELECT` clone by primary key |
| `pg.ErrIsReadOnly` | `var ErrIsReadOnly error` | Returned by every write method above when the table is a view/materialized view/presenter |
| `(*DB).Insert`, `InsertSingle`, `Upsert`, `UpsertSingle`, `Update`, `UpdateExceptColumns`, `UpdateOnlyColumns`, `Delete`, `Duplicate` | Same shapes as the `Repository[T]` methods above but take `any`/`...any` and resolve the table via `db.schema.Get(reflect.TypeOf(...))` | Struct-typed CRUD without a `Repository[T]` wrapper |
| `(*DB).DeleteByID` | `func (db *DB) DeleteByID(ctx context.Context, tableName string, id any) (bool, error)` | Table-name form; resolved via `Schema.GetByTableName` |
| `(*DB).DeleteBy` | `func (db *DB) DeleteBy(ctx context.Context, tableName string, colValPairs ...any) (int64, error)` | `colValPairs`: `"col1", v1, "col2", v2, ...`; zero pairs deletes every row |
| `(*DB).ExistsBy` | `func (db *DB) ExistsBy(ctx context.Context, tableName string, colValPairs ...any) (bool, error)` | |
| `(*DB).CountBy` | `func (db *DB) CountBy(ctx context.Context, tableName string, colValPairs ...any) (int64, error)` | |
| `(*DB).UpdateJSONB` | `func (db *DB) UpdateJSONB(ctx context.Context, tableName, columnName, rowID string, values map[string]any, fieldsToUpdate []string) (int64, error)` | Full or partial (`||` merge) JSONB update by primary key |
| `(*DB).Exec` | `func (db *DB) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)` | |
| `(*DB).ExecFiles` | `func (db *DB) ExecFiles(ctx context.Context, fileReader interface{ ReadFile(name string) ([]byte, error) }, filenames ...string) error` | Runs each file's contents in one transaction, e.g. against an `embed.FS` |
| `(*DB).Mutate` | `func (db *DB) Mutate(ctx context.Context, query string, args ...any) (int64, error)` | `Exec` + `RowsAffected()` |
| `(*DB).MutateSingle` | `func (db *DB) MutateSingle(ctx context.Context, query string, args ...any) (bool, error)` | `RowsAffected() > 0` |
| `(*Repository[T]).Exec`, `Mutate`, `MutateSingle` | Delegate to the `*DB` equivalents | |

## Bulk

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `desc.DefaultInsertBatchSize` | `const DefaultInsertBatchSize = 500` | Rows per multi-row `INSERT`/`UPSERT` statement; shrunk further per call to stay under Postgres's 65535-bind-parameter ceiling |
| `desc.Table.NumInsertableColumns` | `func (td *Table) NumInsertableColumns() int` | Columns that occupy a position in a bulk-insert statement; used to size batches |
| `(*Repository[T]).InsertMany` | `func (repo *Repository[T]) InsertMany(ctx context.Context, values ...T) error` | One transaction, batched multi-row `INSERT`; zero-valued defaulted fields emit `DEFAULT` per row |
| `(*Repository[T]).UpsertMany` | `func (repo *Repository[T]) UpsertMany(ctx context.Context, forceOnConflictExpr string, values ...T) error` | Same batching, `INSERT ... ON CONFLICT DO UPDATE`/`DO NOTHING` |
| `(*Repository[T]).CopyFrom` | `func (repo *Repository[T]) CopyFrom(ctx context.Context, values []T) (int64, error)` | COPY protocol; faster than `InsertMany`, no ON CONFLICT/RETURNING/per-row DEFAULT |
| `(*DB).CopyFrom` | `func (db *DB) CopyFrom(ctx context.Context, tableName Identifier, columnNames []string, rowSrc CopyFromSource) (int64, error)` | Lower-level; routes through the active transaction if any |
| `desc.BuildCopyPlan` | `func BuildCopyPlan(td *Table, structValues []reflect.Value) (*CopyPlan, error)` | Resolves the COPY column list; all-or-nothing per-column DEFAULT decision (see doc comment) |
| `(*desc.CopyPlan).Row` | `func (p *CopyPlan) Row(structValue reflect.Value) ([]any, error)` | Per-row value extraction, encrypts a `PasswordHandler`-backed password column |
| `desc.ErrCopyPassword` | `var ErrCopyPassword error` | Returned by `BuildCopyPlan` for a db-side-hashed password column (no `PasswordHandler`) |

## Stream

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `(*Repository[T]).SelectIter` | `func (repo *Repository[T]) SelectIter(ctx context.Context, query string, args ...any) iter.Seq2[T, error]` | Lazy, single-use row-at-a-time iterator; holds a connection until drained or broken out of |
| `pg.QueryIter[T]` | `func QueryIter[T any](ctx context.Context, db *DB, query string, args ...any) iter.Seq2[T, error]` | Single-column streaming analog of `QuerySlice`; does NOT drop empty strings the way `QuerySlice` does |

## Transact

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `(*DB).Begin` | `func (db *DB) Begin(ctx context.Context) (*DB, error)` | New transaction, or a savepoint if already transactional |
| `(*DB).BeginConcurrent` | `func (db *DB) BeginConcurrent(ctx context.Context) (*DB, error)` | Wraps the `pgx.Tx` in a mutexed `*ConcurrentTx` for safe cross-goroutine sharing of one connection |
| `(*DB).Commit` / `Rollback` | `func (db *DB) Commit(ctx context.Context) error`; `Rollback` analogous | Idempotent after the first successful call |
| `(*DB).IsTransaction` | `func (db *DB) IsTransaction() bool` | |
| `(*DB).InTransaction` | `func (db *DB) InTransaction(ctx context.Context, fn func(*DB) error) (err error)` | `nil` commits; `ErrIntentionalRollback` rolls back and returns `nil`; other error rolls back and returns it (joined with a rollback error if that also fails); panic rolls back and re-panics. Short-circuits (no nesting) when `db` is already transactional |
| `(*Repository[T]).InTransaction` | `func (repo *Repository[T]) InTransaction(ctx context.Context, fn func(*Repository[T]) error) error` | Same, rebuilds a `*Repository[T]` around the transactional `*DB` |
| `pg.InTransaction[R]` | `func InTransaction[R any](ctx context.Context, db *DB, wrap func(*DB) R, fn func(R) error) error` | Generic helper for a hand-written repository wrapper type `R` |
| `pg.ErrIntentionalRollback` | `var ErrIntentionalRollback error` | Sentinel `fn` returns to roll back without surfacing an error |
| `pg.RetryOptions` | `struct { MaxAttempts int; BaseDelay, MaxDelay time.Duration; TxOptions pgx.TxOptions; IsRetryable func(error) bool }` | Zero value: 3 attempts, 50ms/1s backoff, `IsErrRetryableTx` |
| `(*DB).InTransactionRetry` | `func (db *DB) InTransactionRetry(ctx context.Context, opts RetryOptions, fn func(*DB) error) error` | Full-jitter exponential backoff; each attempt is a brand new transaction; runs once with no retry if already transactional |
| `(*Repository[T]).InTransactionRetry` | `func (repo *Repository[T]) InTransactionRetry(ctx context.Context, opts RetryOptions, fn func(*Repository[T]) error) error` | Does not gate on `IsReadOnly` (retry applies to reads too) |
| `(*DB).ExecMany` | `func (db *DB) ExecMany(ctx context.Context, queries ...string) error` | One transaction, one `Exec` per statement (extended protocol cannot prepare multi-statement strings) |
| `(*DB).SetConstraintsDeferred` | `func (db *DB) SetConstraintsDeferred(ctx context.Context, constraints ...string) error` | `SET CONSTRAINTS ALL/"c1","c2" DEFERRED`; errors outside a transaction |
| `pg.ConcurrentTx` / `pg.NewConcurrentTx` | `type ConcurrentTx struct { pgx.Tx; ... }`; `func NewConcurrentTx(ctx context.Context, p *pgxpool.Pool) (*ConcurrentTx, error)` | Mutex-guarded `pgx.Tx` wrapper; every method takes the same lock except `LargeObjects()`/`Conn()`'s *returned* value |

## Errors

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `pg.ErrNoRows` | `var ErrNoRows error` (= `pgx.ErrNoRows`) | Compare via `errors.Is`/`IsErrNoRows` |
| `pg.IsErrNoRows` | `func IsErrNoRows(err error) bool` | |
| `pg.ConstraintKind` | `type ConstraintKind string`; consts `ConstraintUnique`, `ConstraintForeignKey`, `ConstraintNotNull`, `ConstraintCheck`, `ConstraintExclusion` | SQLSTATE-class-23 classification |
| `pg.ConstraintError` | `struct { Kind ConstraintKind; ConstraintName, TableName, ColumnName, Detail, Code string }` | `Error()` renders a one-line summary; `Unwrap()` reaches the underlying `*pgconn.PgError` |
| `pg.AsConstraintError` | `func AsConstraintError(err error) (*ConstraintError, bool)` | Extraction-only: the library never wraps its own errors in this for you |
| `pg.IsErrDuplicate` | `func IsErrDuplicate(err error) (string, bool)` | SQLSTATE `23505`; returns the constraint name |
| `pg.IsErrForeignKey` | `func IsErrForeignKey(err error) (string, bool)` | SQLSTATE `23503` |
| `pg.IsErrInputSyntax` | `func IsErrInputSyntax(err error) (string, bool)` | SQLSTATE `22P02`, plus tsquery syntax errors under `42601` |
| `pg.IsErrRetryableTx` | `func IsErrRetryableTx(err error) bool` | SQLSTATE `40001`/`40P01`; the default `RetryOptions.IsRetryable` |
| `pg.IsErrColumnNotExists` | `func IsErrColumnNotExists(err error, col string) bool` | SQLSTATE `42703` naming `col` |
| `pg.ErrIsReadOnly` | see Write | |
| `pg.ErrEmptyPayload` | `var ErrEmptyPayload error` | From `Listener.Accept` when a notification has an empty payload |
| `desc.ErrCopyPassword` | see Bulk | |

## Introspect

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `(*DB).CreateSchema` | `func (db *DB) CreateSchema(ctx context.Context) error` | Runs `CreateSchemaDumpSQL`'s output in one transaction |
| `(*DB).CreateSchemaDumpSQL` | `func (db *DB) CreateSchemaDumpSQL(ctx context.Context) (string, error)` | `CREATE SCHEMA`, extensions, `CREATE TABLE`s, foreign keys, triggers |
| `(*DB).CheckSchema` | `func (db *DB) CheckSchema(ctx context.Context) error` | Compares registered tables against `ListTables`; errors on any mismatch |
| `(*DB).DeleteSchema` | `func (db *DB) DeleteSchema(ctx context.Context) error` | `DROP SCHEMA ... CASCADE` |
| `(*DB).ListTables` | `func (db *DB) ListTables(ctx context.Context, opts ListTablesOptions) ([]*desc.Table, error)` | Builds `*desc.Table`s from live `information_schema`/`pg_catalog` |
| `pg.ListTablesOptions` | `struct { TableNames []string; Filter desc.TableFilter }` | |
| `pg.MapTypeFilter` | `type MapTypeFilter map[string]any` (implements `desc.TableFilter`) | `"table.column": YourType{}` overrides for `ListTables` |
| `(*DB).ListColumns` | `func (db *DB) ListColumns(ctx context.Context, tableNames ...string) ([]*desc.Column, error)` | |
| `(*DB).ListConstraints` | `func (db *DB) ListConstraints(ctx context.Context, tableNames ...string) ([]*desc.Constraint, error)` | Primary key/unique/check/foreign-key/plain-index rows |
| `(*DB).ListUniqueIndexes` | `func (db *DB) ListUniqueIndexes(ctx context.Context, tableNames ...string) ([]*desc.UniqueIndex, error)` | |
| `(*DB).ListTriggers` | `func (db *DB) ListTriggers(ctx context.Context) ([]*desc.Trigger, error)` | |
| `(*DB).ListColumnsInformationSchema` | `func (db *DB) ListColumnsInformationSchema(ctx context.Context, tableNames ...string) ([]*desc.ColumnBasicInfo, error)` | Lower-level than `ListColumns` (no constraints/unique indexes merged in) |
| `(*DB).GetVersion` | `func (db *DB) GetVersion(ctx context.Context) (string, error)` | Parsed from `SELECT version()` |
| `(*DB).GetSize` | `func (db *DB) GetSize(ctx context.Context) (t SizeInfo, err error)` | Sum of all table sizes |
| `(*DB).ListTableSizes` | `func (db *DB) ListTableSizes(ctx context.Context) ([]TableSizeInfo, error)` | Per-table, includes non-registered tables |
| `(*DB).IsAutoVacuumEnabled`, `DisableAutoVacuum`, `DisableTableAutoVacuum` | `func (db *DB) IsAutoVacuumEnabled(ctx context.Context) (bool, error)`; `DisableAutoVacuum(ctx context.Context) error`; `DisableTableAutoVacuum(ctx context.Context, tableName string) error` | |
| `desc.TableFilter` / `desc.Expressions` / `desc.NewExpression` | `interface { FilterTable(*Table) bool }`; `type Expressions []Expression`; `func NewExpression(input string, fieldType reflect.Type) Expression` | Lower-level filter machinery behind `MapTypeFilter` |

## Generate

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `gen.GenerateSchemaFromDatabase` | `func GenerateSchemaFromDatabase(ctx context.Context, i ImportOptions, e ExportOptions) error` | Live database -> Go structs + `schema.go` |
| `gen.ImportOptions` | `struct { ConnString string; ListTables pg.ListTablesOptions }` | |
| `gen.GenerateColumnsFromSchema` | `func GenerateColumnsFromSchema(s *pg.Schema, e ExportOptions) error` | Registered `*Schema` -> typed column-name constants, consumed by `Conditions`/`OrderBy`/`PageOptions` instead of hand-typed strings |
| `gen.ExportOptions` | `struct { RootDir string; FileMode fs.FileMode; ToSingular func(string) string; GetFileName func(rootDir, tableName string) string; GetPackageName func(tableName string) string }` | Defaults: one file per table under `RootDir`, `0o644` |
| `gen.EachTableToItsOwnPackage` | `func EachTableToItsOwnPackage(rootDir, tableName string) string` | A `GetFileName` strategy: `rootDir/customer/customer.go` |
| `gen.EachTableGroupToItsOwnPackage` | `func EachTableGroupToItsOwnPackage() func(rootDir, tableName string) string` | Groups `customer_address` into the `customer` package |
| `gen.GoImportsTool` | `var GoImportsTool string = "goimports"` | Resolved via `exec.LookPath`; run over the output dir if found |

## Migrate

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `(*DB).Migrate` | `func (db *DB) Migrate(ctx context.Context, fsys fs.FS, opts *MigrateOptions) (applied []string, err error)` | Applies pending `*.sql` files in lexical order, one transaction, advisory-locked (`pg_advisory_xact_lock`) so concurrent instances never double-apply |
| `pg.MigrateOptions` | `struct { TableName string; Pattern string }` | Defaults `"schema_migrations"` / `"*.sql"`; `opts` may be `nil` |

No down/rollback direction, no checksum verification of an already-applied file, no
detection of an out-of-order filename. Reach for `golang-migrate/migrate` or
`pressly/goose` for more.

## Observe

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `(*DB).Ping` | `func (db *DB) Ping(ctx context.Context) error` | Always goes through `db.Pool`, even on a transaction-scoped `*DB` |
| `(*DB).Health` | `func (db *DB) Health(ctx context.Context) (Health, error)` | `{ServerVersion, Pool PoolStat}` |
| `(*DB).PoolStat` | `func (db *DB) PoolStat() PoolStat` | Snapshot of `pgxpool` stats (`AcquireCount`, `IdleConns`, `TotalConns`, ...), JSON-taggable |
| `(*DB).Listen` | `func (db *DB) Listen(ctx context.Context, channel string) (*Listener, error)` | `channel` is quoted before `LISTEN` |
| `(*DB).Notify` | `func (db *DB) Notify(ctx context.Context, channel string, payload any) error` | `string`/`[]byte` sent as-is via `pg_notify`; anything else JSON-marshaled first |
| `(*DB).Unlisten` | `func (db *DB) Unlisten(ctx context.Context, channel string) error` | `"*"` unlistens every channel, unquoted |
| `(*Listener).Accept` | `func (l *Listener) Accept(ctx context.Context) (*Notification, error)` | Blocks for the next notification |
| `(*Listener).Close` | `func (l *Listener) Close(ctx context.Context) error` | `UNLISTEN` then release the pooled connection; safe to call more than once |
| `pg.UnmarshalNotification[T]` | `func UnmarshalNotification[T any](n *Notification) (T, error)` | JSON-decodes `n.Payload` |
| `(*DB).PrepareListenTable` | `func (db *DB) PrepareListenTable(ctx context.Context, opts *ListenTableOptions) error` | Creates the shared notify function and per-table trigger, once each |
| `(*DB).ListenTable` | `func (db *DB) ListenTable(ctx context.Context, opts *ListenTableOptions, callback func(TableNotificationJSON, error) error) (Closer, error)` | Delivers INSERT/UPDATE/DELETE as JSON; callback runs on its own goroutine |
| `(*Repository[T]).ListenTable` | `func (repo *Repository[T]) ListenTable(ctx context.Context, callback func(TableNotification[T], error) error) (Closer, error)` | Typed `New`/`Old` decoded into `T` |
| `pg.ListenTableOptions` | `struct { Tables map[string][]TableChangeType; Channel string; Function string }` | Defaults: `{"*": [INSERT, UPDATE, DELETE]}`, `"table_change_notifications"`, `"table_change_notify"` |
| `pg.TableChangeType` | consts `TableChangeTypeInsert`, `TableChangeTypeUpdate`, `TableChangeTypeDelete` | |
| `pg.WithLogger` / `WithLoggerLevel` / `WithQueryTracer` | see Connect | Logging/tracing is configured at `Open` time |

## Test

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `pgtest.ConnString` | `func ConnString(tb testing.TB) string` | Reads `PG_CONNSTRING`; `t.Skipf`s (no hardcoded fallback) when unset |
| `pgtest.New` | `func New(tb testing.TB, schema *pg.Schema, connString string) *pg.DB` | Ephemeral `pgtest_<16 hex>` schema, materializes registered tables, `t.Cleanup` drops it; mutates `schema`'s tables in place, so one `*pg.Schema` per test |

See `testing.md` for the shared-`Schema` constraint, which suites are DB-free, and how CI
runs this.

## Miscellaneous helpers

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `pg.Ptr[T]` | `func Ptr[T any](v T) *T` | Turn a literal into the pointer form a nullable query argument needs |
| `pg.NullIfZero[T]` | `func NullIfZero[T comparable](v T) *T` | `nil` when `v` is `T`'s zero value, else `&v` |
| `pg.Identifier` | alias of `pgx.Identifier` | Qualified name for `CopyFrom`'s `tableName` |
| `pg.CopyFromSource` | alias of `pgx.CopyFromSource` | Row source for `CopyFrom`; pgx ships `CopyFromRows`/`CopyFromSlice`/`CopyFromFunc` |
