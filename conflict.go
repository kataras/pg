package pg

import (
	"context"
	"reflect"

	"github.com/kataras/pg/desc"
)

// InsertOnConflict inserts one or more values with the given ON CONFLICT behavior (see
// OnConflict), batching into multi-row INSERT ... ON CONFLICT statements exactly like
// InsertMany: one transaction, batches capped at desc.DefaultInsertBatchSize rows (shrunk
// further for wide tables to stay under PostgreSQL's 65535 bind-parameter ceiling. See
// desc.Table.NumInsertableColumns).
//
// It builds each batch's statement via desc.BuildBulkInsertQueryOnConflict, which never appends
// RETURNING; use InsertSingleOnConflict when a primary key needs to be scanned back.
func (repo *Repository[T]) InsertOnConflict(ctx context.Context, oc OnConflict, values ...T) error {
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

			query, args, err := desc.BuildBulkInsertQueryOnConflict(repo.td, structValues, oc)
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

// InsertSingleOnConflict inserts a single value with the given ON CONFLICT behavior (see
// OnConflict).
//
// When idPtr is nil the statement is run with Exec. When idPtr is non-nil, the statement always
// carries a RETURNING <primary key> clause (see desc.BuildInsertQueryOnConflict) and the result
// is scanned into idPtr: for a DO UPDATE action this is the inserted-or-updated row's primary
// key, and for a DO NOTHING action that skipped an existing conflicting row, the query returns
// zero rows and this method returns ErrNoRows instead of leaving idPtr unset.
func (repo *Repository[T]) InsertSingleOnConflict(ctx context.Context, oc OnConflict, value T, idPtr any) error {
	if repo.IsReadOnly() {
		return ErrIsReadOnly
	}

	query, args, err := desc.BuildInsertQueryOnConflict(repo.td, desc.IndirectValue(value), idPtr, oc)
	if err != nil {
		return err
	}

	if idPtr != nil {
		return repo.db.QueryRow(ctx, query, args...).Scan(idPtr)
	}

	_, err = repo.db.Exec(ctx, query, args...)
	return err
}

// UpdateOrInsert is the write half of a check-then-act upsert whose UPDATE matches on business
// identity (e.g. a natural key looked up with a plain WHERE, not necessarily the row's ON
// CONFLICT target) while the INSERT still carries its own ON CONFLICT clause to stay correct
// against writers racing the same check.
//
// It executes updateQuery (with args) via db.QueryRow, scanning its single RETURNING value into
// R. If that matches no row (ErrNoRows), it executes insertQuery instead, with args followed by
// insertExtraArgs as its bind parameters, again scanning a single RETURNING value into R.
// Any other error from either query is returned as-is.
//
// updateQuery and insertQuery are developer-authored SQL, executed verbatim; UpdateOrInsert does
// not build, validate or quote any part of them. Callers are responsible for parameterizing
// user-supplied values via $N placeholders in args/insertExtraArgs, never by string-concatenating
// them into the query text.
func UpdateOrInsert[R any](ctx context.Context, db *DB, updateQuery, insertQuery string, args []any, insertExtraArgs ...any) (R, error) {
	var result R

	err := db.QueryRow(ctx, updateQuery, args...).Scan(&result)
	if err == nil {
		return result, nil
	}
	if !IsErrNoRows(err) {
		return result, err
	}

	insertArgs := make([]any, 0, len(args)+len(insertExtraArgs))
	insertArgs = append(insertArgs, args...)
	insertArgs = append(insertArgs, insertExtraArgs...)

	err = db.QueryRow(ctx, insertQuery, insertArgs...).Scan(&result)
	return result, err
}
