package pg

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Notification is a type alias of pgconn.Notification type.
type Notification = pgconn.Notification

// Closer is the interface which is implemented by the Listener.
// It's used to close the underline connection.
type Closer interface {
	Close(ctx context.Context) error
}

// Listener represents a postgres database LISTEN connection.
type Listener struct {
	conn *pgxpool.Conn

	channel string
	closed  atomic.Bool

	// mu serializes actual use of conn. The underlying pgconn.PgConn is not safe for
	// concurrent use, and Accept and Close are routinely called from different goroutines:
	// DB.ListenTable returns the caller the same Listener its background goroutine is
	// parked in Accept on.
	//
	// Accept holds mu for the whole, potentially unbounded WaitForNotification call, so
	// Close cannot simply wait for it: it cancels closeCtx first (which unblocks Accept)
	// and only then acquires mu.
	mu          sync.Mutex
	closeCtx    context.Context
	closeCancel context.CancelFunc
}

var _ Closer = (*Listener)(nil)

// ErrEmptyPayload is returned when the notification payload is empty.
var ErrEmptyPayload = errors.New("empty payload")

// ErrListenerClosed is returned by Listener.Accept when the listener has been closed, either
// before the call or while it was waiting for a notification. It reports an orderly shutdown,
// not a failure: a listen loop should return on it rather than log it.
var ErrListenerClosed = errors.New("listener closed")

// Accept waits for a notification and returns it.
//
// Accept is safe to call while another goroutine calls Close: a Close concurrent with a
// waiting Accept unblocks it and Accept then reports ErrListenerClosed. Concurrent Accept
// calls on the same Listener are serialized, as a connection can only serve one waiter.
func (l *Listener) Accept(ctx context.Context) (*Notification, error) {
	if l.closed.Load() {
		return nil, ErrListenerClosed
	}

	// WaitForNotification blocks until a notification arrives, which may be never; wire
	// Close's cancellation into this call's context so Close does not have to wait for one.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := context.AfterFunc(l.closeCtx, cancel)
	defer stop()

	l.mu.Lock()
	defer l.mu.Unlock()

	// Re-check under mu: Close may have won the race for it and already released the
	// connection back to the pool, in which case it is no longer ours to read from.
	if l.closed.Load() {
		return nil, ErrListenerClosed
	}

	nf, err := l.conn.Conn().WaitForNotification(ctx)
	if err != nil {
		if l.closed.Load() {
			// The wait was cancelled by Close, not by the caller's own context.
			return nil, ErrListenerClosed
		}

		return nil, err
	}

	/* Sadly this is not possible due to the Go's limitations.
	var payload T
	if s, ok := payload.(string); ok {
		// use nativeAccept.
	}
	*/

	if len(nf.Payload) == 0 {
		return nil, ErrEmptyPayload
	}

	return nf, nil
}

// Close closes the listener connection. It unsubscribes from the channel with an UNLISTEN
// statement and releases the underlying pooled connection back to the pool.
//
// Close is safe to call multiple times (and concurrently): only the first call does any work,
// subsequent calls are no-ops that return nil.
//
// Close is also safe to call while another goroutine is waiting inside Accept: it first
// cancels that wait, then takes the connection over once Accept has stopped using it. The
// interrupted Accept reports ErrListenerClosed.
func (l *Listener) Close(ctx context.Context) error {
	if l == nil {
		return nil
	}

	if l.conn == nil {
		return nil
	}

	if l.closed.CompareAndSwap(false, true) {
		// Unblock an in-flight Accept, then wait for it to hand the connection back:
		// UNLISTEN and Release below must not run concurrently with it.
		l.closeCancel()

		l.mu.Lock()
		defer l.mu.Unlock()

		query := "UNLISTEN " + QuoteIdentifier(l.channel)
		_, err := l.conn.Exec(ctx, query)
		if err != nil {
			// The connection is still subscribed to the channel: closing it (instead of
			// releasing it back to the pool) makes pgxpool destroy it rather than recycle
			// it, so the next borrower does not silently keep receiving notifications.
			l.conn.Conn().Close(ctx)
			l.conn.Release()
			return err
		}

		l.conn.Release()
	}

	return nil
}

// notifyJSON sends a notification of any type to the underline database listener.
func notifyJSON(ctx context.Context, db *DB, channel string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return notifyNative(ctx, db, channel, b)
}

// NotifyNative sends a raw notification to the underline database listener,
// it accepts string or a slice of bytes because that's the only raw types that are allowed to be delivered.
func notifyNative[T string | []byte](ctx context.Context, db *DB, channel string, payload T) error {
	query := `SELECT pg_notify($1, $2)`
	_, err := db.Pool.Exec(ctx, query, channel, payload) // Always on top.
	return err
}

// jsonDecodeOptions keeps encoding/json v1's case-insensitive field matching for JSON that
// PostgreSQL - or a hand-written pg_notify payload - produced. Row JSON uses lower-cased column
// names, while the Go structs it decodes into are the caller's entities, which carry pg tags
// rather than json ones. encoding/json/v2 matches names exactly by default, which would leave
// those fields silently at their zero values instead of erroring.
//
// It is deliberately applied only where the destination is a caller-supplied type. Payloads
// decoded into this package's own structs (TableNotification, which tags every field
// explicitly) keep v2's exact matching.
var jsonDecodeOptions = json.MatchCaseInsensitiveNames(true)

// UnmarshalNotification returns the notification payload as a custom type of T.
func UnmarshalNotification[T any](n *Notification) (T, error) {
	var payload T

	err := json.Unmarshal([]byte(n.Payload), &payload, jsonDecodeOptions)
	if err != nil {
		return payload, err
	}

	return payload, nil
}
