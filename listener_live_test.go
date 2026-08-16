package pg

import (
	"context"
	"errors"
	"testing"
	"time"
)

// These tests require a live PostgreSQL server, see getTestConnString (db_example_test.go) and
// the PG_CONNSTRING environment variable. Run with, e.g.:
//
//	PG_CONNSTRING="..." go test -run 'TestListenNotifyRoundtrip|TestListenMixedCaseChannel' -v .

// TestListenNotifyRoundtrip verifies that DB.Listen followed by DB.Notify delivers the
// notification to Listener.Accept, and that Listener.Close returns a nil error. Before the fix,
// Close always executed the invalid statement "SELECT UNLISTEN $1;" (UNLISTEN is not valid
// inside SELECT and does not accept a bind parameter), so Close always errored.
func TestListenNotifyRoundtrip(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	const channel = "test_listen_notify_roundtrip"

	listener, err := db.Listen(context.Background(), channel)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	if err = db.Notify(context.Background(), channel, "hello"); err != nil {
		t.Fatalf("notify: %v", err)
	}

	acceptCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	notification, err := listener.Accept(acceptCtx)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	if notification.Payload != "hello" {
		t.Fatalf("unexpected payload: got %q, want %q", notification.Payload, "hello")
	}

	if err = listener.Close(context.Background()); err != nil {
		t.Fatalf("close: expected nil error, got: %v", err)
	}
}

// TestListenMixedCaseChannel verifies that DB.Listen quotes the channel identifier, so that a
// mixed-case channel name matches notifications sent (via raw pg_notify) using the same
// mixed-case spelling. Before the fix, "LISTEN "+channel (unquoted) was folded to lowercase by
// Postgres while pg_notify's channel argument is not folded, so a mixed-case channel passed to
// Listen would never receive notifications sent to the same, differently-cased channel.
func TestListenMixedCaseChannel(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	const channel = "MyChan"

	listener, err := db.Listen(context.Background(), channel)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close(context.Background())

	// Raw pg_notify call to precisely control the channel casing sent to Postgres, mirroring
	// what another client (not necessarily using this library's Notify helper) might send.
	if _, err = db.Exec(context.Background(), "SELECT pg_notify('MyChan', 'x');"); err != nil {
		t.Fatalf("pg_notify: %v", err)
	}

	acceptCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	notification, err := listener.Accept(acceptCtx)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	if notification.Payload != "x" {
		t.Fatalf("unexpected payload: got %q, want %q", notification.Payload, "x")
	}
}

// TestListenTableEmptyPayloadNoCrash verifies that an empty-payload notification on a
// DB.ListenTable channel reaches the callback as ErrEmptyPayload instead of nil-pointer
// panicking the background goroutine, and that the goroutine keeps listening afterwards.
//
// Before the fix, Listener.Accept returns (nil, ErrEmptyPayload) for an empty payload; the
// ListenTable goroutine's Accept-error branch invoked the callback but then always fell through
// to "evt.payload = notification.Payload", dereferencing the nil notification and crashing the
// (unrecovered) background goroutine.
func TestListenTableEmptyPayloadNoCrash(t *testing.T) {
	db, err := openTestConnection(true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	type received struct {
		evt TableNotificationJSON
		err error
	}
	events := make(chan received, 4)

	opts := &ListenTableOptions{
		Tables: map[string][]TableChangeType{"customers": defaultChangesToWatch},
	}
	closer, err := db.ListenTable(context.Background(), opts, func(evt TableNotificationJSON, err error) error {
		events <- received{evt, err}
		return nil // keep listening, exactly the case that used to crash.
	})
	if err != nil {
		t.Fatalf("ListenTable: %v", err)
	}
	defer closer.Close(context.Background())

	// Give the background goroutine a moment to reach Accept before notifying; this is a
	// best-effort safety margin, not a correctness requirement (the LISTEN registration
	// itself already happened synchronously inside ListenTable, before this test resumes).
	time.Sleep(500 * time.Millisecond)

	// Send an empty-payload notification directly on the table-change channel: this is what
	// used to crash the background goroutine (and, unrecovered, the whole process).
	if _, err = db.Exec(context.Background(), "SELECT pg_notify('table_change_notifications', '');"); err != nil {
		t.Fatalf("pg_notify empty payload: %v", err)
	}

	select {
	case got := <-events:
		if got.err == nil {
			t.Fatalf("expected a non-nil error for the empty-payload notification, got nil (evt=%+v)", got.evt)
		}

		if !errors.Is(got.err, ErrEmptyPayload) {
			t.Fatalf("expected ErrEmptyPayload, got: %v", got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the empty-payload callback")
	}

	// Prove the goroutine did not crash/return by triggering a real table change and asserting
	// it is still delivered.
	newCustomer := Customer{
		CognitoUserID: "11111111-1111-1111-1111-111111111111",
		Email:         "empty-payload-no-crash@example.com",
		Name:          "EmptyPayloadNoCrash",
	}
	if err = db.InsertSingle(context.Background(), newCustomer, &newCustomer.ID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}

	select {
	case got := <-events:
		if got.err != nil {
			t.Fatalf("expected a nil error for the normal notification, got: %v", got.err)
		}

		if got.evt.Table != "customers" || got.evt.Change != TableChangeTypeInsert {
			t.Fatalf("unexpected event after empty-payload notification: %+v", got.evt)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the normal-notification callback; the listener goroutine may have stopped")
	}
}

// TestPrepareListenTableOnTransaction verifies that PrepareListenTable works when called on a
// transaction-scoped *DB (obtained from InTransaction/Begin). Before the fix, DB.clone dropped
// the tableChangeNotifyOnceMutex field, so a transaction-scoped *DB nil-panicked the first time
// prepareListenTable tried to lock it.
func TestPrepareListenTableOnTransaction(t *testing.T) {
	db, err := openTestConnection(true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	opts := &ListenTableOptions{
		Tables: map[string][]TableChangeType{"customers": defaultChangesToWatch},
	}

	err = db.InTransaction(context.Background(), func(tx *DB) error {
		return tx.PrepareListenTable(context.Background(), opts)
	})
	if err != nil {
		t.Fatalf("PrepareListenTable inside InTransaction: %v", err)
	}
}
