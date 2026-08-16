package pg

import (
	"context"
	"fmt"
	"strings"
)

// This file adds two small transaction-related helpers to *DB, ExecMany and
// SetConstraintsDeferred. They are kept apart from db_crud.go's table-name CRUD API (and from
// db.go's Exec/ExecFiles, which they are conceptually closest to) purely to keep each new file
// scoped to one change: this one to executing multiple statements/deferring constraints inside
// a transaction, db_crud.go to schema-validated single-table CRUD.

// ExecMany executes each statement in order inside a single transaction (joining the
// current one when the DB is already transactional); statements are sent one Exec at
// a time because the extended protocol cannot prepare multi-statement strings.
//
// It is InTransaction (see db.go) plus a loop: if any statement fails, the whole transaction is
// rolled back (or, when it joined an already-open transaction, that transaction is left in its
// failed, aborted state for the caller who started it to roll back: the same semantics as any
// other error surfaced from a function passed to InTransaction) and the statements that already
// ran do not persist. Given zero queries, it does nothing and returns nil without opening a
// transaction.
func (db *DB) ExecMany(ctx context.Context, queries ...string) error {
	if len(queries) == 0 {
		return nil
	}

	return db.InTransaction(ctx, func(db *DB) error {
		for _, query := range queries {
			if _, err := db.Exec(ctx, query); err != nil {
				return err
			}
		}

		return nil
	})
}

// SetConstraintsDeferred defers the named deferrable constraints (ALL when none are
// given) for the remainder of the current transaction; it errors when the DB is not
// inside a transaction.
//
// It issues `SET CONSTRAINTS ALL DEFERRED` when constraints is empty, or
// `SET CONSTRAINTS "c1", "c2" DEFERRED` otherwise, with every name quoted via QuoteIdentifier.
// PostgreSQL rejects SET CONSTRAINTS outside of a transaction block anyway, but since that
// statement is meaningless there (there is no "remainder of the current transaction" to defer
// within), SetConstraintsDeferred checks DB.IsTransaction itself and returns a descriptive
// error before issuing any SQL, rather than relying on the server's error text.
//
// Note that naming a constraint that is not itself declared DEFERRABLE, or one that does not
// exist, is still rejected by PostgreSQL. SetConstraintsDeferred does not validate constraint
// names against the Schema the way the table-name CRUD methods in db_crud.go validate table and
// column names, since constraints are not modeled there.
func (db *DB) SetConstraintsDeferred(ctx context.Context, constraints ...string) error {
	if !db.IsTransaction() {
		return fmt.Errorf("pg: set constraints deferred: not inside a transaction")
	}

	target := "ALL"
	if len(constraints) > 0 {
		quoted := make([]string, len(constraints))
		for i, constraint := range constraints {
			quoted[i] = QuoteIdentifier(constraint)
		}

		target = strings.Join(quoted, ", ")
	}

	_, err := db.Exec(ctx, fmt.Sprintf("SET CONSTRAINTS %s DEFERRED;", target))
	return err
}
