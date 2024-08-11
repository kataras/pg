package pg

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConcurrentTx is a wrapper around pgx.Tx that provides a mutex to synchronize access
// to the underlying pgx.Tx. This is useful when you want to use a pgx.Tx from
// multiple goroutines.
type ConcurrentTx struct {
	pgx.Tx
	mu sync.Mutex
}

// NewConcurrentTx is a wrapper around pgxpool.Pool.Begin that provides a mutex to synchronize
// access to the underlying pgx.Tx.
// It returns a TxSync that wraps the pgx.Tx.
// The TxSync must be closed when done with it.
func NewConcurrentTx(ctx context.Context, p *pgxpool.Pool) (*ConcurrentTx, error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return nil, err
	}

	return &ConcurrentTx{Tx: tx}, nil
}

// Rollback is a wrapper around pgx.Tx.Rollback that provides a mutex to synchronize
// access to the underlying pgx.Tx.
func (ct *ConcurrentTx) Rollback(ctx context.Context) error {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return ct.Tx.Rollback(ctx)
}

// Commit is a wrapper around pgx.Tx.Commit that provides a mutex to synchronize
// access to the underlying pgx.Tx.
func (ct *ConcurrentTx) Commit(ctx context.Context) error {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return ct.Tx.Commit(ctx)
}

// QueryRow is a wrapper around pgx.Tx.QueryRow that provides a mutex to synchronize
// access to the underlying pgx.Tx.
func (ct *ConcurrentTx) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return ct.Tx.QueryRow(ctx, sql, args...)
}

// Query is a wrapper around pgx.Tx.Query that provides a mutex to synchronize
// access to the underlying pgx.Tx.
func (ct *ConcurrentTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return ct.Tx.Query(ctx, sql, args...)
}

// QueryRow is a wrapper around pgx.Tx.QueryRow that provides a mutex to synchronize
// access to the underlying pgx.Tx.
func (ct *ConcurrentTx) Exec(ctx context.Context, sql string, args ...any) (commandTag pgconn.CommandTag, err error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return ct.Tx.Exec(ctx, sql, args...)
}

// Prepare is a wrapper around pgx.Tx.Prepare that provides a mutex to synchronize
// access to the underlying pgx.Tx.
func (ct *ConcurrentTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return ct.Tx.Prepare(ctx, name, sql)
}

// SendBatch is a wrapper around pgx.Tx.SendBatch that provides a mutex to synchronize
// access to the underlying pgx.Tx.
func (ct *ConcurrentTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return ct.Tx.SendBatch(ctx, b)
}

// Begin is a wrapper around pgx.Tx.Begin that provides a mutex to synchronize
// access to the underlying pgx.Tx.
func (ct *ConcurrentTx) Begin(ctx context.Context) (pgx.Tx, error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	tx, err := ct.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}

	return &ConcurrentTx{Tx: tx}, nil
}
