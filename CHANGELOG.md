# Changelog

All notable changes to this project are documented here.

## [1.0.13] - 2026-08-16

A security, correctness and documentation pass over the whole library, plus a large set of
additive helper APIs and a book. Every fix below was reviewed independently and the full test
suite runs green against a live PostgreSQL 16 server.

### Breaking

- **`DB.UpsertSingle`'s parameter order changed** to
  `UpsertSingle(ctx, forceOnConflictExpr string, value any, idPtr any)`. It previously took the
  conflict expression last, while `Repository[T].UpsertSingle` took it first, so the same call
  read differently on the two types. Every method in the family now takes its conflict
  specification immediately after `ctx`: `Upsert`, `UpsertMany`, `UpsertSingle`,
  `InsertOnConflict` and `InsertSingleOnConflict`. The three variadic methods could never put it
  anywhere else, since `values ...T` has to come last, so the single-row methods moved to match.
  The break is compile-time: passing the old order fails to build rather than misbehaving.

### Security

- **Fixed SQL injection in `DB.Listen`**, which concatenated the channel name directly into
  `LISTEN`. Channel names now go through `QuoteIdentifier`, which also makes mixed-case channels
  behave consistently between `LISTEN` and `pg_notify`.
- **Fixed SQL injection in `PrepareListenTable` and `ListenTable`.** The caller-supplied channel
  was interpolated inside a single-quoted PL/pgSQL literal, so a quote character could append
  arbitrary code to a trigger that runs on every write. Channel, function and table names are now
  validated against `^[A-Za-z_][A-Za-z0-9_$]*$` before any DDL is built.
- **Fixed an unvalidated column name in `DB.UpdateJSONB`.** The column is now resolved through
  the table descriptor and rejected when unknown; table, column and primary key are quoted.
- **Stopped leaking the connection string.** A failed `Open` embedded the full DSN, password
  included, in the returned error; the code generator printed it to stdout. Both now omit it.
- **Identifier validation at registration.** `ConvertStructToTable` rejects table, column and
  unique-index names outside the safe character set, so an unsafe name from a struct tag or a
  name mapper cannot reach a query builder.
- **Correct SQL identifier quoting.** The builders used `strconv.Quote`, which is Go string
  quoting: it escapes an embedded quote as `\"` where PostgreSQL requires `""`, and mangles
  non-ASCII. They now use pgx's identifier sanitizer.
- **Quoted the remaining DDL sinks**: `DeleteSchema`, `DisableAutoVacuum`,
  `DisableTableAutoVacuum`, and the select, delete and authentication queries. The search path is
  validated before `CREATE SCHEMA`.
- **`PasswordAlg` is validated** against an allowlist (`bf`, `md5`, `xdes`, `des`) before it is
  interpolated into `gen_salt(...)`, on the insert, bulk-insert and update paths.
- **Added `WithLoggerLevel`.** `WithLogger` installs pgx tracelog at trace level, which logs every
  statement and every bind argument, passwords included; its documentation now says so.
- **Hardened the code generator**: generated files are `0644` and directories `0755` instead of
  world-writable `0777`, and table names that are not safe as file names are rejected before
  `filepath.Join`, closing a path-traversal route from a hostile database.

### Fixed

- **`InTransaction` silently discarded commit errors.** The result was unnamed, so the deferred
  `err = tx.Commit(ctx)` wrote to a dead local and a failed COMMIT returned `nil`: callers
  believed writes were persisted when they had been rolled back. Rollback errors are now joined
  with `errors.Join`, and returning `ErrIntentionalRollback` correctly yields `nil`.
- **`pg.DoNothing` did not do nothing.** Passed as `forceOnConflictExpr` it matched no unique
  index, so it either failed with `can't find unique index with name: DO NOTHING` or silently
  emitted `DO UPDATE SET`. It now emits `ON CONFLICT (target) DO NOTHING`, or a bare
  `ON CONFLICT DO NOTHING` when no target can be derived, and the `conflict=DO NOTHING` struct tag
  works on the Upsert family too. The single-row path keeps `RETURNING`, so a successful insert
  populates `idPtr` and `ErrNoRows` unambiguously means the row was skipped.
- **A nil-pointer dereference crashed the process.** In the `ListenTable` goroutine, an `Accept`
  error whose callback returned nil fell through to dereference a nil notification. An empty
  `pg_notify` payload was enough to trigger it. The unmarshal-error path also invoked the callback
  twice.
- **`UNLISTEN` never worked.** Both `DB.Unlisten` and `Listener.Close` executed
  `SELECT UNLISTEN $1`, which is invalid twice over: `UNLISTEN` is a statement, and its channel
  cannot be a bind parameter. `Close` therefore returned a still-subscribed connection to the
  pool. It now emits a correct statement and destroys the connection if the unlisten fails.
- **`clone()` dropped a mutex**, so `prepareListenTable` panicked on any transaction-scoped `DB`.
  The three notify-state fields are now one shared pointer that cannot be half-copied.
- **The table-change trigger ignored a custom `Function` name**, creating a trigger that pointed
  at a function that did not exist.
- **`Repository.InTransaction` discarded the caller's context**, using `context.Background()`, so
  cancellation could not release a pooled connection.
- **Zero-value detection was wrong for unrecognized types.** `isZero`'s type switch returned
  `false` by default, so `[16]byte` UUIDs, named string types and custom structs were treated as
  non-zero and their column defaults never fired. It is now reflection-based.
- **Constraint parsing could panic** at connect time, from `CheckSchema`, on a composite foreign
  key or a multi-line `CHECK` whose definition did not match the parser's regex.
- **Scanners could panic** on a driver value whose type did not match the destination field.
  `nullableScanner` now converts where possible and returns a descriptive error otherwise.
- **Malformed filter expressions panicked** out of `ListTables`; the panic is recovered into an
  error, and an unchecked type assertion became a comma-ok.
- **A malformed `type=` tag panicked** with a slice-bounds error instead of returning one.
- **Bulk inserts could exceed PostgreSQL's 65535 bind-parameter limit.** The batch size is now
  capped using the new `Table.NumInsertableColumns`.
- **`Schema` was not safe for concurrent use.** Registration and lookups now share a mutex, and
  `GetByTableName` is a map lookup rather than a linear scan.
- **`ConcurrentTx` left `CopyFrom`, `LargeObjects` and `Conn` unsynchronized**, promoting them
  straight from the embedded `pgx.Tx` and defeating the type's purpose.
- **Error classification no longer depends on English message text.** `IsErrDuplicate`,
  `IsErrForeignKey`, `IsErrInputSyntax` and `IsErrColumnNotExists` match SQLSTATE codes first and
  fall back to the old text matching.
- **`SelectByUsernameAndPassword` used a hardcoded `password` column name**, so a schema whose
  password column is tagged differently could never authenticate.
- Removed an `unsafe` string alias handed to `json.Unmarshal` on the notification path.
- Fixed a doc comment promising an `ErrStop` sentinel that does not exist, and an example that
  passed a pointer where `gen.GenerateColumnsFromSchema` takes a value.

### Added

Query helpers:

- `Conditions`, a WHERE-fragment builder whose bind parameters renumber automatically, so one
  filter set can drive both a page query and its COUNT twin. `Where`, `And`, `AndIf`, `AndAnyOf`,
  `AndMatchAnyOf`, `AndNameMatchAnyOf`, `AndSearch`, `AndOptionalEq`, `AndMin`, `AndMax`, `Build`,
  `Args`, `NextIndex`.
- `desc.Table.OrderBy` and `Repository[T].OrderBy`: validate a user-supplied sort column against
  the descriptor and return a quoted fragment, because a dynamic ORDER BY cannot be a bind
  parameter.
- `PageOptions`, `Repository[T].SelectPaginated` and `SelectWithTotal`, plus
  `desc.RowsToStructWithTotal` for a `COUNT(*) OVER()` window column.
- `QueryStructs`, `QueryStruct` and `ScanStructs`, backed by `desc.LooseTable`, for scanning
  ad-hoc read models that are not registered in a schema.
- `QueryMap`, `QueryFunc`, `ScanFunc`, and `Count` on both `DB` and `Repository[T]`.
- `DB.DeleteByID`, `DeleteBy`, `ExistsBy`, `CountBy` and `SelectSingle`: table-name CRUD that
  resolves the table and every column against the registered schema before building SQL.
- `pg.InTransaction[R]`, which rebuilds a caller's typed wrapper from the transactional `DB`.
- `DB.ExecMany` and `DB.SetConstraintsDeferred`.
- `desc.Table.SelectColumnsExpr` and `JSONBuildObjectExpr`.

Writing:

- `desc.OnConflict` with `BuildInsertQueryOnConflict` and `BuildBulkInsertQueryOnConflict`, plus
  `Repository[T].InsertOnConflict` and `InsertSingleOnConflict`, for an explicit conflict target
  and a partial `DO UPDATE SET`.
- `UpdateOrInsert[R]` for check-then-act writes.
- COPY-protocol bulk loading: `desc.CopyPlan`, `BuildCopyPlan`, `DB.CopyFrom` and
  `Repository[T].CopyFrom`.

Errors:

- `ConstraintError`, `ConstraintKind` and `AsConstraintError`, a typed view over SQLSTATE class
  23 violations, so callers stop parsing error text.
- `IsErrRetryableTx` for SQLSTATE 40001 and 40P01.

Operations:

- `RetryOptions` with `DB.InTransactionRetry` and `Repository[T].InTransactionRetry`,
  full-jitter backoff, and correct handling of failures that surface at COMMIT.
- `Repository[T].SelectIter` and `QueryIter`, returning `iter.Seq2[T, error]` so large result
  sets stream instead of materializing.
- `DB.Migrate` and `MigrateOptions`: ordered `.sql` files from an `fs.FS`, applied in one
  transaction under an advisory lock, recorded in a ledger table.
- `DB.Ping` and `DB.Health`.
- `WithQueryTracer`, `WithDefaultQueryExecMode`, `WithStatementCacheCapacity` and
  `WithDescriptionCacheCapacity`, for OpenTelemetry-style tracing and PgBouncer compatibility.
  No OpenTelemetry dependency is added.
- `Ptr` and `NullIfZero` for optional parameters.
- The `pgtest` package: an ephemeral, randomly named schema per test, dropped on cleanup.

### Changed

- Package documentation for all three packages, and godoc for every previously undocumented
  exported identifier. Four doc comments that named the wrong identifier were corrected.
- Modernized to current Go idioms: `slices` and `cmp` in place of `sort` and hand-rolled loops,
  `errors.Is` and `errors.Join`, `reflect.Pointer`, `atomic.Bool`, range-over-int.
- Row scanning builds a column lookup once per result set instead of scanning the column list for
  every column of every row.
- CI runs `go vet` and the race detector, and adds a lint job. The example sub-modules and the
  README's Go version note were brought up to date.
- The test connection string can be overridden with `PG_CONNSTRING`.

### Documentation

- **The pg book**: a preface, 16 chapters and an epilogue, rendered to HTML and a 165-page PDF by
  a generator in its own module under `book/`.
- A `CLAUDE.md` and a `.claude/` tree recording the architecture, the security invariants, the
  testing setup and the book's conventions for future contributors and AI assistants.
