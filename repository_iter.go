package pg

import (
	"context"
	"iter"

	"github.com/kataras/pg/desc"
)

// SelectIter executes the query and returns a lazy, single-use iterator that decodes one row
// of T at a time, reusing the same per-row struct-scanning machinery Repository.Select
// itself uses (desc.ConvertRowsToStruct with repo's own cached table descriptor), without
// ever materializing the whole result set as a []T. Use it instead of Select for exports and
// large scans where a []T of the full result would not fit comfortably in memory; for
// anything that already fits, Select remains simpler to use (a slice, not an iterator you
// have to range over) and pays no worse a cost.
//
// # Result semantics
//
//   - A query error (the initial repo.db.Query call itself failing, e.g. bad SQL or a lost
//     connection) makes the iterator yield exactly one pair (a zero T, that error) and
//     then stop; the loop body sees it once, on the first iteration, and range then ends.
//   - A row-scan error, or a rows.Err() surfaced after the last row (e.g. a network error
//     mid-stream), is reported the same way: exactly one terminal (zero T, err) yield, after
//     which the iterator stops. Every row already yielded before that point is unaffected.
//     Only rows after the failure are lost.
//   - A yielded error is always paired with the T zero value, never a partially populated
//     one, so a caller can safely check err first without needing to also check the value.
//
// # Single-use, and what that means precisely
//
// The returned iter.Seq2 is a closure that runs repo.db.Query fresh every time it is ranged
// over (ranging over the same returned value twice would run the query twice, not replay
// the first run), so "single-use" here is not about a panic on reuse. What actually matters,
// and the reason to still treat it as single-use in practice, is that only one iteration
// against a given *DB may be in flight at a time: while unconsumed rows remain, SelectIter
// holds a connection checked out from the pool (see "Connection lifetime" below), and pgx
// connections execute statements serially: a second statement issued on that same *DB before
// the first iteration is fully drained or broken out of will block, or fail outright, rather
// than run concurrently (the same "conn busy" constraint BeginConcurrent's doc describes).
// On a plain (non-transactional) *DB backed by a pool with more than one connection, a second,
// unrelated query on that *DB can still proceed fine on a different pooled connection while
// one SelectIter is in flight (the constraint is per-connection, not per-*DB), but on a
// tx-scoped *DB (see below), there is exactly one connection to contend over, so it really is
// "finish this iteration first".
//
// # Connection lifetime
//
// Whichever connection backs the query is held for the entire time rows remain unconsumed,
// exactly as it would be for any other pgx.Rows: a connection acquired from repo.db.Pool for a
// plain *DB, or the single connection a transaction already holds for a tx-scoped *DB (the *DB
// InTransaction/InTransactionRetry/Begin hand to fn). It is released deterministically
// in exactly one of these ways, each triggering rows.Close() via a defer inside the iterator
// closure:
//
//   - The loop runs to completion (rows.Next() returns false and rows.Err() is checked).
//   - The query itself, or a row scan, fails: see "Result semantics" above.
//   - The caller's range loop exits early (a `break`, a `return`, or the loop body itself
//     returning false to `yield`), which range-over-func delivers to the iterator as `yield`
//     returning false; the closure then returns immediately, running its deferred
//     rows.Close() and releasing the connection back to the pool (or, on a tx-scoped *DB,
//     simply leaving that connection free for the transaction's next statement) before
//     SelectIter's caller regains control. A caller that breaks out of a SelectIter loop can
//     immediately issue another query against the same *DB with no special cleanup of their
//     own.
//
// # What SelectIter does not do
//
// Server-side cursors are deliberately not part of this API. pgx already streams query
// results row-by-row over the wire as rows.Next() is called; it does not buffer the entire
// result set in memory before SelectIter (or Select) ever sees the first row, which is the
// property SelectIter's own memory behavior relies on, so there is no missing piece for the
// common "stream a big SELECT without blowing up memory" case. A real, standalone SQL CURSOR
// is a different, narrower tool: explicit server-side control over how much of a possibly very
// expensive query plan PostgreSQL itself materializes per fetch, or holding a query open across
// multiple round trips interleaved with other statements on the same transaction. A caller who
// specifically needs that can still reach for it by hand, inside a transaction (a cursor's
// lifetime is tied to its transaction):
//
//	repo := pg.NewRepository[BigRow](db)
//
//	err := repo.InTransaction(ctx, func(tx *pg.Repository[BigRow]) error {
//		if _, err := tx.Exec(ctx, "DECLARE export_cursor CURSOR FOR SELECT * FROM big_table"); err != nil {
//			return err
//		}
//
//		for {
//			rows, err := tx.Query(ctx, "FETCH 1000 FROM export_cursor")
//			if err != nil {
//				return err
//			}
//
//			batch, err := desc.RowsToStruct[BigRow](tx.Table(), rows) // closes rows itself.
//			if err != nil {
//				return err
//			}
//			if len(batch) == 0 {
//				return nil // exhausted.
//			}
//
//			// ... consume batch ...
//		}
//	})
func (repo *Repository[T]) SelectIter(ctx context.Context, query string, args ...any) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T

		rows, err := repo.db.Query(ctx, query, args...)
		if err != nil {
			yield(zero, err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var value T
			if err := desc.ConvertRowsToStruct(repo.td, rows, &value); err != nil {
				yield(zero, err)
				return
			}

			if !yield(value, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(zero, err)
		}
	}
}

// QueryIter executes the query and returns a lazy, single-use iterator over single-column
// rows scanned directly into T (the type of T should be a database-scannable type, e.g.
// string, int, time.Time, not a registered struct; use SelectIter for that): the streaming
// analog of QuerySlice, for the same "an export or large scan shouldn't have to build the
// whole []T first" reason SelectIter exists for struct rows. Its result, single-use and
// connection-lifetime semantics are identical to SelectIter's: see that method's doc for the
// full detail on what a query error, a scan error, and an early break each do.
//
// The one behavioral difference from QuerySlice: QueryIter does NOT replicate QuerySlice's
// skip-empty-string quirk (QuerySlice silently drops every "" result when T is string).
// QueryIter yields every row QueryIter's query returns, empty strings included, exactly as
// SelectIter/Select yield every row regardless of column content, so switching an existing
// QuerySlice[string] call over to QueryIter[string] can change what the caller observes if it
// was (even unknowingly) relying on that quirk to filter out empty values.
func QueryIter[T any](ctx context.Context, db *DB, query string, args ...any) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T

		rows, err := db.Query(ctx, query, args...)
		if err != nil {
			yield(zero, err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var entry T
			if err := rows.Scan(&entry); err != nil {
				yield(zero, err)
				return
			}

			if !yield(entry, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil && !IsErrNoRows(err) {
			yield(zero, err)
		}
	}
}
