package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type (
	// Identifier is a type alias for pgx.Identifier: a qualified name (e.g. Identifier{"public",
	// "users"}) that addresses a database object such as a table, with each part sanitized
	// (double-quoted, embedded quotes doubled) individually when the driver renders it into SQL.
	// CopyFrom's tableName parameter takes one of these.
	Identifier = pgx.Identifier

	// CopyFromSource is a type alias for pgx.CopyFromSource: the row-iterator interface CopyFrom
	// consumes to stream rows into a COPY ... FROM STDIN. pgx ships ready-made implementations:
	// pgx.CopyFromRows for an in-memory [][]any, pgx.CopyFromSlice for a func(int) ([]any,
	// error), pgx.CopyFromFunc for a pull-until-nil func() ([]any, error). Repository.CopyFrom
	// builds a pgx.CopyFromSlice source around desc.CopyPlan.Row.
	CopyFromSource = pgx.CopyFromSource
)

// CopyFrom streams rows into the given table using the PostgreSQL COPY protocol, routing through
// the active transaction when there is one, the same pool-vs-tx routing Query/Exec use (see
// IsTransaction), and returns the number of rows copied.
//
// CopyFrom is a thin wrapper over pgx's CopyFrom (pgxpool.Pool.CopyFrom outside a transaction,
// pgx.Tx.CopyFrom inside one): tableName and columnNames are passed through verbatim, and rowSrc
// supplies the row data. Callers normally reach this indirectly through Repository.CopyFrom,
// which also builds tableName/columnNames/rowSrc for a registered struct type via
// desc.BuildCopyPlan; call this directly only when driving COPY against an unregistered table or
// a hand-built CopyFromSource.
//
// If this *DB was obtained from BeginConcurrent, its transaction is a *ConcurrentTx, whose
// CopyFrom wrapper takes the same mutex as every other query issued through that transaction -
// so a CopyFrom that streams many rows serializes with (and blocks) any concurrent goroutine
// sharing that transaction for the whole streaming duration, not just for the final commit-style
// round trip. Plan accordingly for large loads run inside a BeginConcurrent transaction.
func (db *DB) CopyFrom(ctx context.Context, tableName Identifier, columnNames []string, rowSrc CopyFromSource) (int64, error) {
	if db.tx != nil {
		n, err := db.tx.CopyFrom(ctx, tableName, columnNames, rowSrc)
		if err != nil {
			return n, fmt.Errorf("transaction: copy from: %w", err)
		}

		return n, nil
	}

	n, err := db.Pool.CopyFrom(ctx, tableName, columnNames, rowSrc)
	if err != nil {
		return n, fmt.Errorf("copy from: %w", err)
	}

	return n, nil
}
