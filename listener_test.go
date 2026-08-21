package pg

import (
	"context"
	json "encoding/json/v2"
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

// rowTimestampEntity mirrors the shape of a registered entity: snake_case column names in the
// payload, CamelCase Go fields, and both flavors of timestamp column.
type rowTimestampEntity struct {
	ID        string    `pg:"type=uuid,primary"`
	CreatedAt time.Time `pg:"type=timestamp"`
	UpdatedAt time.Time `pg:"type=timestamptz"`
	DeletedAt time.Time `pg:"type=timestamp"`
	Name      string    `pg:"type=varchar(255)"`
}

// TestUnmarshalNotificationRowTimestamp covers the payload PostgreSQL actually produces for a
// row: json_build_object renders a `timestamp without time zone` column as ISO 8601 with no
// offset, which is not RFC 3339 and which time.Time refuses on its own. pgx solves this the
// same way for pgtype.Timestamp, and the two paths have to agree: a column read through a
// repository and the same column arriving over LISTEN must produce the same time.Time.
func TestUnmarshalNotificationRowTimestamp(t *testing.T) {
	const payload = `{"id":"11111111-1111-1111-1111-111111111111",` +
		`"created_at":"2026-08-21T13:16:03.161728",` +
		`"updated_at":"2026-08-21T13:16:03.161728+00:00",` +
		`"deleted_at":null,` +
		`"name":"Makis"}`

	got, err := UnmarshalNotification[rowTimestampEntity](&Notification{Payload: payload})
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := time.Date(2026, 8, 21, 13, 16, 3, 161728000, time.UTC)

	if !got.CreatedAt.Equal(want) {
		t.Errorf("created_at: got %v, want %v", got.CreatedAt, want)
	}
	if !got.UpdatedAt.Equal(want) {
		t.Errorf("updated_at: got %v, want %v", got.UpdatedAt, want)
	}
	if !got.DeletedAt.IsZero() {
		t.Errorf("deleted_at: got %v, want the zero time for a JSON null", got.DeletedAt)
	}
	if got.Name != "Makis" {
		t.Errorf("name: got %q, want %q", got.Name, "Makis")
	}
}

// TestListenTableDecodeChainRowTimestamp walks the whole decode chain ExampleRepository_ListenTable
// exercises, without a server: the trigger's json_build_object payload into TableNotificationJSON
// (exact name matching, this package's own tags), then its "new" member into the caller's entity
// (case-insensitive matching, pg tags). The customers table's created_at and updated_at are
// `pg:"type=timestamp"`, so both arrive without an offset.
func TestListenTableDecodeChainRowTimestamp(t *testing.T) {
	const payload = `{"table":"customers","change":"INSERT","old":null,"new":{` +
		`"id":"11111111-1111-1111-1111-111111111111",` +
		`"created_at":"2026-08-21T13:16:03.161728",` +
		`"updated_at":"2026-08-21T13:16:03.161728",` +
		`"cognito_user_id":"766064d4-a2a7-442d-aa75-33493bb4dbb9",` +
		`"email":"kataras2024@hotmail.com",` +
		`"name":"Makis",` +
		`"username":""}}`

	var outer TableNotificationJSON
	if err := json.Unmarshal([]byte(payload), &outer); err != nil {
		t.Fatalf("decode notification envelope: %v", err)
	}

	if outer.Table != "customers" {
		t.Errorf("table: got %q, want %q", outer.Table, "customers")
	}
	if outer.Change != TableChangeTypeInsert {
		t.Errorf("change: got %q, want %q", outer.Change, TableChangeTypeInsert)
	}
	if len(outer.Old) > 0 && string(outer.Old) != "null" {
		t.Errorf("old: got %s, want null for an INSERT", outer.Old)
	}

	var got Customer
	if err := json.Unmarshal(outer.New, &got, jsonDecodeOptions); err != nil {
		t.Fatalf("decode new row into Customer: %v", err)
	}

	if got.Name != "Makis" {
		t.Errorf("name: got %q, want %q", got.Name, "Makis")
	}
	if got.Email != "kataras2024@hotmail.com" {
		t.Errorf("email: got %q, want %q", got.Email, "kataras2024@hotmail.com")
	}
	if got.CognitoUserID != "766064d4-a2a7-442d-aa75-33493bb4dbb9" {
		t.Errorf("cognito_user_id: got %q", got.CognitoUserID)
	}

	want := time.Date(2026, 8, 21, 13, 16, 3, 161728000, time.UTC)
	if !got.CreatedAt.Equal(want) {
		t.Errorf("created_at: got %v, want %v", got.CreatedAt, want)
	}
	if !got.UpdatedAt.Equal(want) {
		t.Errorf("updated_at: got %v, want %v", got.UpdatedAt, want)
	}
}
