package pg

import (
	"context"
	"errors"
	"testing"
	"time"
)

// These tests are DB-free: they exercise the Listener's close bookkeeping, not its SQL. The
// round trip against a real server lives in listener_live_test.go.

// newTestListener returns a Listener with no connection at all. Any code path that reaches the
// connection nil-panics, which is the point: it turns "this must not touch conn" into an
// assertion the test can actually make rather than a comment.
func newTestListener() *Listener {
	closeCtx, closeCancel := context.WithCancel(context.Background())
	return &Listener{
		channel:     "test_channel",
		closeCtx:    closeCtx,
		closeCancel: closeCancel,
	}
}

// TestListenerAcceptAfterClose verifies that Accept reports ErrListenerClosed once the listener
// is closed, and gets there without dereferencing the connection. After Close the connection has
// been released back to the pool and belongs to whoever borrows it next, so reading a
// notification off it would be a use-after-release rather than merely a late call.
func TestListenerAcceptAfterClose(t *testing.T) {
	l := newTestListener()
	l.closed.Store(true)

	if _, err := l.Accept(context.Background()); !errors.Is(err, ErrListenerClosed) {
		t.Fatalf("Accept after Close: expected ErrListenerClosed, got: %v", err)
	}
}

// TestListenerAcceptLosingTheCloseRace covers the interleaving that the closed re-check inside
// Accept's critical section exists for: Accept passes the initial closed check, then blocks on
// the mutex behind a Close that goes on to release the connection. Without the re-check, Accept
// would win the mutex afterwards and read from a connection the pool has already handed on.
//
// Holding l.mu here stands in for that Close.
func TestListenerAcceptLosingTheCloseRace(t *testing.T) {
	l := newTestListener()

	l.mu.Lock()

	result := make(chan error, 1)
	go func() {
		_, err := l.Accept(context.Background())
		result <- err
	}()

	// Best-effort margin for Accept to clear its initial closed check and park on the mutex,
	// which is the interleaving under test. The assertion below holds either way; this only
	// makes the interesting ordering the likely one.
	time.Sleep(100 * time.Millisecond)

	// Finish the simulated Close: mark the listener closed, cancel the in-flight wait, and
	// hand the connection back.
	l.closed.Store(true)
	l.closeCancel()
	l.mu.Unlock()

	select {
	case err := <-result:
		if !errors.Is(err, ErrListenerClosed) {
			t.Fatalf("Accept losing the race with Close: expected ErrListenerClosed, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return after Close released the connection")
	}
}

// TestListenerCloseIdempotent verifies the documented contract that Close is a no-op returning
// nil on a nil receiver, on a listener with no connection, and on every call after the first.
func TestListenerCloseIdempotent(t *testing.T) {
	var nilListener *Listener
	if err := nilListener.Close(context.Background()); err != nil {
		t.Fatalf("Close on a nil *Listener: expected nil, got: %v", err)
	}

	l := newTestListener()
	for i := 1; i <= 3; i++ {
		if err := l.Close(context.Background()); err != nil {
			t.Fatalf("Close call #%d: expected nil, got: %v", i, err)
		}
	}
}
