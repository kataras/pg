package pg

import "context"

// InTransactionWrap runs fn inside a database transaction, using wrap to rebuild the caller's
// typed repository wrapper R around the transactional *DB before calling fn with it.
//
// It exists to remove the boilerplate every typed repository wrapper otherwise has to
// hand-write: a method that opens a transaction on the underlying *DB, reconstructs the
// wrapper type around the transactional *DB, and calls the caller's function with that
// reconstructed wrapper. R is typically a struct that embeds *Repository[T] (as returned by
// NewRepository) or a hand-written aggregate of several repositories; wrap is typically the
// wrapper's own constructor function.
//
// It is named InTransactionWrap rather than InTransaction because DB.InTransaction already
// exists, taking the plain func(*DB) error this one wraps.
//
// If db is already inside a transaction, fn's statements join that transaction instead of
// starting a nested one (DB.InTransaction short-circuits nesting), and wrap still runs so fn
// is called with a wrapper built from the same (already-transactional) *DB.
//
// It replaces hand-written wrapper boilerplate such as:
//
//	func (r *Repository) InTransaction(ctx context.Context, fn func(*Repository) error) error {
//	    return r.repo.DB().InTransactionWrap(ctx, NewRepository, fn)
//	}
func (db *DB) InTransactionWrap[R any](ctx context.Context, wrap func(*DB) R, fn func(R) error) error {
	return db.InTransaction(ctx, func(tx *DB) error { return fn(wrap(tx)) })
}
