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

// Compile-time assertion that *ConcurrentTx still implements pgx.Tx in full,
// i.e. that no method promoted from the embedded pgx.Tx is left unwrapped by mistake.
var _ pgx.Tx = (*ConcurrentTx)(nil)

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
func (ct *ConcurrentTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
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

// Exec is a wrapper around pgx.Tx.Exec that provides a mutex to synchronize
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

// CopyFrom is a wrapper around pgx.Tx.CopyFrom that provides a mutex to synchronize
// access to the underlying pgx.Tx.
func (ct *ConcurrentTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return ct.Tx.CopyFrom(ctx, tableName, columnNames, rowSrc)
}

// LargeObjects is a wrapper around pgx.Tx.LargeObjects that provides a mutex to synchronize
// access to the underlying pgx.Tx for the duration of this call only.
//
// The returned pgx.LargeObjects value is NOT synchronized by ConcurrentTx: any large
// object operations performed through it bypass this type's mutex entirely, so a
// caller sharing a ConcurrentTx across goroutines must not use the returned value
// concurrently without its own additional synchronization.
func (ct *ConcurrentTx) LargeObjects() pgx.LargeObjects {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return ct.Tx.LargeObjects()
}

// Conn is a wrapper around pgx.Tx.Conn that provides a mutex to synchronize
// access to the underlying pgx.Tx for the duration of this call only.
//
// The returned *pgx.Conn is NOT synchronized by ConcurrentTx: any operations performed
// directly on it bypass this type's mutex entirely, so a caller sharing a ConcurrentTx
// across goroutines must not use the returned value concurrently without its own
// additional synchronization.
func (ct *ConcurrentTx) Conn() *pgx.Conn {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return ct.Tx.Conn()
}
