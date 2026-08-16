package pg

import (
	"context"
	"encoding/json"
	"errors"
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
}

var _ Closer = (*Listener)(nil)

// ErrEmptyPayload is returned when the notification payload is empty.
var ErrEmptyPayload = errors.New("empty payload")

// Accept waits for a notification and returns it.
func (l *Listener) Accept(ctx context.Context) (*Notification, error) {
	nf, err := l.conn.Conn().WaitForNotification(ctx)
	if err != nil {
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
func (l *Listener) Close(ctx context.Context) error {
	if l == nil {
		return nil
	}

	if l.conn == nil {
		return nil
	}

	if l.closed.CompareAndSwap(false, true) {
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

// UnmarshalNotification returns the notification payload as a custom type of T.
func UnmarshalNotification[T any](n *Notification) (T, error) {
	var payload T

	err := json.Unmarshal([]byte(n.Payload), &payload)
	if err != nil {
		return payload, err
	}

	return payload, nil
}
