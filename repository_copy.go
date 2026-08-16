package pg

import (
	"context"
	"reflect"

	"github.com/kataras/pg/desc"

	"github.com/jackc/pgx/v5"
)

// CopyFrom bulk-loads values into the repository's table via the PostgreSQL COPY protocol and
// returns the number of rows copied. Compared with InsertMany it is much faster for large plain
// loads and has no 65535-bind-parameter ceiling, but it supports no ON CONFLICT, no RETURNING, no
// per-row DEFAULT for a column that is zero in some rows and set in others (see desc.CopyPlan's
// doc for exactly how that all-or-nothing decision is made, and its loudly documented
// zero-value-is-stored-as-zero-not-DEFAULT caveat for a column BuildCopyPlan decides to include),
// and is all-or-nothing: the whole COPY either succeeds in full or the driver aborts it and no
// rows are stored. Prefer InsertMany whenever you need conflict handling, RETURNING, or a DB
// default to fire for individual zero-valued rows of a column that is not all-zero across the
// whole batch.
//
// CopyFrom returns ErrIsReadOnly for a read-only repository (a view, materialized view or
// presenter table), and (0, nil) for an empty values slice without touching the database. A
// table whose password column is hashed in the database via crypt()/gen_salt() (i.e. it has no
// Go-side desc.PasswordHandler installed via Schema.HandlePassword) cannot be copied into at
// all: CopyFrom then returns desc.ErrCopyPassword, since COPY cannot invoke a SQL function per
// row the way a regular INSERT can. A PasswordHandler-backed password column is encrypted, in
// Go, once per row instead.
//
// Like InsertMany, CopyFrom routes through the repository's current transaction when there is
// one (see DB.CopyFrom); unlike InsertMany it does not open one of its own, so a caller that
// wants the copy to roll back together with other statements must wrap the call itself, e.g.
// via Repository.InTransaction.
func (repo *Repository[T]) CopyFrom(ctx context.Context, values []T) (int64, error) {
	if repo.IsReadOnly() {
		return 0, ErrIsReadOnly
	}
	if len(values) == 0 {
		return 0, nil
	}

	structValues := make([]reflect.Value, len(values))
	for i := range values {
		structValues[i] = desc.IndirectValue(values[i])
	}

	plan, err := desc.BuildCopyPlan(repo.td, structValues)
	if err != nil {
		return 0, err
	}

	rowSrc := pgx.CopyFromSlice(len(structValues), func(i int) ([]any, error) {
		return plan.Row(structValues[i])
	})

	tableName := Identifier{repo.td.SearchPath, repo.td.Name}
	return repo.db.CopyFrom(ctx, tableName, plan.ColumnNames, rowSrc)
}
