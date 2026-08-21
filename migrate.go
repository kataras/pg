package pg

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"regexp"
	"slices"
)

// migrateLockKey is the fixed advisory-lock key Migrate takes for the duration of its
// transaction: computed once, at package init time, from the FNV-1a hash of
// "kataras/pg/migrate" (fixed string, no randomness, so it is stable across processes and
// restarts). See Migrate's doc for why it takes this lock.
var migrateLockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("kataras/pg/migrate"))
	return int64(h.Sum64())
}()

// migrateTableNameRegex matches a bare, unquoted PostgreSQL identifier: it must start with a
// letter or underscore, followed by letters, digits, underscores or dollar signs only. Same
// character class as desc's (unexported) identifierRegex (see desc/struct_table.go), borrowed
// here because MigrateOptions.TableName is interpolated directly into DDL (CREATE TABLE,
// SELECT, INSERT) rather than passed as a bind parameter, so it must be validated before any
// of that SQL is built.
var migrateTableNameRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

const (
	defaultMigrateTableName = "schema_migrations"
	defaultMigratePattern   = "*.sql"
)

// MigrateOptions configures DB.Migrate. The zero value applies the documented defaults.
type MigrateOptions struct {
	// TableName is the migration-tracking table, created on demand if it does not already
	// exist. Defaults to "schema_migrations" when empty. Must be a bare identifier (see
	// migrateTableNameRegex); Migrate rejects anything else before running any SQL, since the
	// name is quoted with QuoteIdentifier and interpolated into DDL rather than bound as a
	// parameter.
	TableName string
	// Pattern is the fs.Glob pattern selecting migration filenames within the fs.FS passed to
	// Migrate. Defaults to "*.sql" when empty.
	Pattern string
}

// Migrate applies the not-yet-applied .sql files found in fsys, in ascending lexical order of
// their filenames, and returns the filenames it applied, in the order they were applied (an
// empty/nil slice, with a nil error, if none were pending). Lexical order is why migrations
// should be named with a zero-padded numeric prefix, e.g. "0001_init.sql", "0002_add_users.sql".
// That convention keeps lexical and intended order identical.
//
// The whole call runs inside a SINGLE database transaction (via DB.InTransaction): PostgreSQL's
// DDL is transactional, so if any pending file fails (a syntax error, a constraint violation,
// anything) the transaction rolls back and every earlier file applied by this same call is
// undone along with it. Nothing is left half-applied, and the tracking table ends up exactly as
// it was before the call; see DB.InTransaction's doc for exactly how the commit/rollback/error
// propagation works. A file's contents are executed as-is via Exec (mirroring how ExecFiles
// runs a file's contents), so a file containing several ';'-separated statements runs all of
// them as a single simple-protocol Exec, the same way ExecFiles does.
//
// Before touching any file, the transaction takes a Postgres advisory lock
// (pg_advisory_xact_lock) under a fixed key (see migrateLockKey). This is what makes it safe
// for several instances of an application (e.g. replicas starting up concurrently) to call
// Migrate against the same database at the same time: only one of them acquires the lock and
// actually runs the pending files, while the rest block until it commits or rolls back, then
// see the now-applied versions already in the tracking table and apply nothing themselves.
// Concurrent callers must never double-apply the same file. The lock is transaction-scoped
// ("xact"), so it is released automatically the instant the transaction ends (commit or
// rollback), and a crashed or killed process can never leave it stuck. Then the transaction
// creates the tracking table (opts.TableName, default "schema_migrations") if missing, with
// `CREATE TABLE IF NOT EXISTS <table> (version text PRIMARY KEY, applied_at timestamptz NOT
// NULL DEFAULT now())`, reads the versions already recorded there, and executes+records only
// the files not already present, recording each one's bare filename (as matched by
// opts.Pattern, e.g. "0001_init.sql", not a full path) as its version. A file with empty
// contents (after reading) is not executed (there is nothing to run) but is still recorded
// as applied, the same as any other file, so it counts toward "already applied" on the next
// call and is never retried.
//
// fsys is any fs.FS, typically an embed.FS. For migrations kept in a subdirectory of an
// embedded tree, pass an fs.Sub view rooted at that subdirectory (fsys, err := fs.Sub(embedded,
// "migrations")) so that opts.Pattern matches the migration filenames directly instead of
// needing a directory prefix.
//
// opts may be nil, in which case the documented defaults apply.
//
// Deliberately excluded: there is no down/rollback direction, no checksum recorded or verified
// for a file that was already applied (so silently editing an already-applied file has no
// effect on future runs), and no detection of or special handling for a file that lands
// lexically before one already applied ("out-of-order" migrations just apply wherever their
// filename sorts, with no warning). This is meant to be the smallest honest migration runner,
// not a full migration framework: reach for golang-migrate/migrate or pressly/goose if you
// need any of the above.
func (db *DB) Migrate(ctx context.Context, fsys fs.FS, opts *MigrateOptions) (applied []string, err error) {
	tableName := defaultMigrateTableName
	pattern := defaultMigratePattern
	if opts != nil {
		if opts.TableName != "" {
			tableName = opts.TableName
		}
		if opts.Pattern != "" {
			pattern = opts.Pattern
		}
	}

	if !migrateTableNameRegex.MatchString(tableName) {
		return nil, fmt.Errorf("migrate: invalid table name: %q: must match %s", tableName, migrateTableNameRegex.String())
	}

	names, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil, fmt.Errorf("migrate: glob %q: %w", pattern, err)
	}
	slices.Sort(names)

	quotedTable := QuoteIdentifier(tableName)

	err = db.InTransaction(ctx, func(db *DB) error {
		if _, err := db.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrateLockKey); err != nil {
			return fmt.Errorf("migrate: advisory lock: %w", err)
		}

		createSQL := fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())",
			quotedTable)
		if _, err := db.Exec(ctx, createSQL); err != nil {
			return fmt.Errorf("migrate: create tracking table: %w", err)
		}

		alreadyApplied, err := db.QuerySlice[string](ctx, "SELECT version FROM "+quotedTable)
		if err != nil {
			return fmt.Errorf("migrate: read tracking table: %w", err)
		}

		appliedSet := make(map[string]struct{}, len(alreadyApplied))
		for _, version := range alreadyApplied {
			appliedSet[version] = struct{}{}
		}

		insertSQL := "INSERT INTO " + quotedTable + " (version) VALUES ($1)"

		for _, name := range names {
			if _, ok := appliedSet[name]; ok {
				continue
			}

			contents, err := fs.ReadFile(fsys, name)
			if err != nil {
				return fmt.Errorf("migrate: read %s: %w", name, err)
			}

			if len(contents) > 0 {
				if _, err = db.Exec(ctx, string(contents)); err != nil {
					return fmt.Errorf("migrate: exec %s: %w", name, err)
				}
			}

			if _, err = db.Exec(ctx, insertSQL, name); err != nil {
				return fmt.Errorf("migrate: record %s: %w", name, err)
			}

			applied = append(applied, name)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return applied, nil
}
