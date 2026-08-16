package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"

	"github.com/kataras/pg/desc"
)

// TableChangeType is the type of the table change.
// Available values: INSERT, UPDATE, DELETE.
type TableChangeType string

const (
	// TableChangeTypeInsert is the INSERT table change type.
	TableChangeTypeInsert TableChangeType = "INSERT"
	// TableChangeTypeUpdate is the UPDATE table change type.
	TableChangeTypeUpdate TableChangeType = "UPDATE"
	// TableChangeTypeDelete is the DELETE table change type.
	TableChangeTypeDelete TableChangeType = "DELETE"
)

func changesToString(changes []TableChangeType) string {
	if len(changes) == 0 {
		return ""
	}

	parts := make([]string, len(changes))
	for i, change := range changes {
		parts[i] = string(change)
	}

	return strings.Join(parts, " OR ")
}

type (
	// TableNotification is the notification message sent by the postgresql server
	// when a table change occurs.
	// The subscribed postgres channel is named 'table_change_notifications'.
	// The "old" and "new" fields are the old and new values of the row.
	// The "old" field is only available for UPDATE and DELETE table change types.
	// The "new" field is only available for INSERT and UPDATE table change types.
	// The "old" and "new" fields are raw json values, use the "json.Unmarshal" to decode them.
	// See "DB.ListenTable" method.
	TableNotification[T any] struct {
		// Table is the name of the table the change occurred on.
		Table  string          `json:"table"`
		Change TableChangeType `json:"change"` // INSERT, UPDATE, DELETE.

		// New is the row's value after the change. It is only populated for INSERT and
		// UPDATE notifications; it is the zero value of T for DELETE.
		New T `json:"new"`
		// Old is the row's value before the change. It is only populated for UPDATE and
		// DELETE notifications; it is the zero value of T for INSERT.
		Old T `json:"old"`

		payload string `json:"-"` /* just in case */
	}

	// TableNotificationJSON is the generic version of the TableNotification.
	TableNotificationJSON = TableNotification[json.RawMessage]
)

// GetPayload returns the raw payload of the notification.
func (tn TableNotification[T]) GetPayload() string {
	return tn.payload
}

// ListenTableOptions is the options for the "DB.ListenTable" method.
type ListenTableOptions struct {
	// Tables map of table name and changes to listen for.
	//
	// Key is the table to listen on for changes.
	// Value is changes is the list of table changes to listen for.
	// Defaults to {"*": ["INSERT", "UPDATE", "DELETE"] }.
	//
	// Every key other than the "*" wildcard must match listenTableIdentifierPattern
	// (^[A-Za-z_][A-Za-z0-9_$]*$), the same restriction placed on Channel and Function,
	// because table names are embedded as raw SQL identifiers when building the
	// CREATE TRIGGER statement.
	Tables map[string][]TableChangeType

	// Channel is the name of the postgres channel to listen on.
	// Default: "table_change_notifications".
	//
	// Must match listenTableIdentifierPattern (^[A-Za-z_][A-Za-z0-9_$]*$) because it is
	// embedded both as a PL/pgSQL single-quoted string literal and as a LISTEN target.
	Channel string

	// Function is the name of the postgres function
	// which is used to notify on table changes, the
	// trigger name is <table_name>_<Function>.
	// Defaults to "table_change_notify".
	//
	// Must match listenTableIdentifierPattern (^[A-Za-z_][A-Za-z0-9_$]*$) because it is
	// embedded as a raw SQL function/trigger-name identifier.
	Function string
}

// listenTableIdentifierPattern restricts the ListenTableOptions.Channel, ListenTableOptions.Function
// and (non-wildcard) ListenTableOptions.Tables keys to plain SQL identifiers, so that they can be
// safely embedded as: a PL/pgSQL single-quoted string literal (the notify channel), a raw function
// or trigger-name identifier, and a LISTEN/UNLISTEN target. See validateListenTableIdentifier.
var listenTableIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

// validateListenTableIdentifier reports a descriptive error if value does not satisfy
// listenTableIdentifierPattern. kind is a short label (e.g. "channel", "function", "table")
// used to name the offending field in the returned error.
func validateListenTableIdentifier(kind, value string) error {
	if !listenTableIdentifierPattern.MatchString(value) {
		return fmt.Errorf("pg: listen table: invalid %s %q: must match %s", kind, value, listenTableIdentifierPattern.String())
	}

	return nil
}

// tableNotifyState holds the mutex-guarded, once-only bookkeeping shared by all *DB instances
// cloned from the same root DB (see DB.clone) so that DB.prepareListenTable creates the
// table-change notify function and each per-table trigger at most once, even when called
// concurrently or from a transaction-scoped *DB.
type tableNotifyState struct {
	mu              sync.Mutex
	functionCreated bool
	triggers        map[string]struct{} // table name -> trigger installed.
}

var defaultChangesToWatch = []TableChangeType{TableChangeTypeInsert, TableChangeTypeUpdate, TableChangeTypeDelete}

func (opts *ListenTableOptions) setDefaults() {
	if opts.Channel == "" {
		opts.Channel = "table_change_notifications"
	}

	if opts.Function == "" {
		opts.Function = "table_change_notify"
	}

	if len(opts.Tables) == 0 {
		opts.Tables = map[string][]TableChangeType{wildcardTableStr: defaultChangesToWatch}
	}
}

const wildcardTableStr = "*"

// PrepareListenTable prepares the table for listening for live table updates.
// See "db.ListenTable" method for more.
func (db *DB) PrepareListenTable(ctx context.Context, opts *ListenTableOptions) error {
	opts.setDefaults()

	// Validate every user-controlled identifier BEFORE touching the database: Channel and
	// Function are embedded directly into DDL/PL-pgSQL text (see prepareListenTable), and
	// each non-wildcard table name is embedded into the CREATE TRIGGER statement.
	if err := validateListenTableIdentifier("channel", opts.Channel); err != nil {
		return err
	}

	if err := validateListenTableIdentifier("function", opts.Function); err != nil {
		return err
	}

	for table := range opts.Tables {
		if table == wildcardTableStr {
			continue
		}

		if err := validateListenTableIdentifier("table", table); err != nil {
			return err
		}
	}

	_, isWildcard := opts.Tables[wildcardTableStr]
	if isWildcard {
		changesToWatch := opts.Tables[wildcardTableStr]
		if len(changesToWatch) == 0 {
			return nil
		}

		delete(opts.Tables, wildcardTableStr) // remove the wildcard entry and replace with table names in registered schema.
		for _, table := range db.schema.TableNames(desc.TableTypeBase) {
			opts.Tables[table] = changesToWatch
		}
	}

	if len(opts.Tables) == 0 {
		return nil
	}

	for table, changes := range opts.Tables {
		if err := db.prepareListenTable(ctx, opts.Channel, opts.Function, table, changes); err != nil {
			return err
		}
	}

	return nil
}

// prepareListenTable creates the shared notify function (once) and the trigger for a single
// table (once per table), then records both in db.notifyState. It is called by
// PrepareListenTable for each table in the resolved Tables map.
func (db *DB) prepareListenTable(ctx context.Context, channel, function, table string, changes []TableChangeType) error {
	if table == "" {
		return errors.New("empty table name")
	}

	if len(changes) == 0 {
		return nil
	}

	// Serialize the whole function+trigger creation under a single mutex: this DDL path only
	// runs rarely (once per function, once per table), so serializing it is a fine trade-off
	// for correctness across concurrent callers and transaction-scoped *DB clones that share
	// this same notifyState pointer.
	db.notifyState.mu.Lock()
	defer db.notifyState.mu.Unlock()

	if !db.notifyState.functionCreated {
		// First, check and create the notify function shared by all tables' triggers.
		query := fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger AS $$
			DECLARE
			payload text;
			channel text := '%s';

			BEGIN
			SELECT json_build_object('table', TG_TABLE_NAME, 'change', TG_OP, 'old', OLD, 'new', NEW)::text
			INTO payload;
			PERFORM pg_notify(channel, payload);
			IF (TG_OP = 'DELETE') THEN
				RETURN OLD;
		  	ELSE
				RETURN NEW;
		  	END IF;
		END;
		$$
		LANGUAGE plpgsql;`, function, channel)

		_, err := db.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("create or replace function %s: %w", function, err)
		}

		db.notifyState.functionCreated = true
	}

	if _, triggerCreated := db.notifyState.triggers[table]; !triggerCreated {
		query := fmt.Sprintf(`CREATE OR REPLACE TRIGGER %s_%s
        AFTER %s
        ON %s
        FOR EACH ROW
        EXECUTE FUNCTION %s();`, table, function, changesToString(changes), table, function)

		_, err := db.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("create trigger %s_%s: %w", table, function, err)
		}

		db.notifyState.triggers[table] = struct{}{}
	}

	return nil
}

// ListenTable registers a function which notifies on the given "table" changes (INSERT, UPDATE, DELETE),
// the subscribed postgres channel is named 'table_change_notifications'.
//
// The callback function can return any other error to stop the listener.
// The callback function can return nil to continue listening.
//
// TableNotification's New and Old fields are raw json values, use the "json.Unmarshal" to decode them
// to the actual type.
func (db *DB) ListenTable(ctx context.Context, opts *ListenTableOptions, callback func(TableNotificationJSON, error) error) (Closer, error) {
	if err := db.PrepareListenTable(ctx, opts); err != nil {
		return nil, err
	}

	conn, err := db.Listen(ctx, opts.Channel)
	if err != nil {
		return nil, err
	}

	go func() {
		defer conn.Close(ctx)

		for {
			var evt TableNotificationJSON

			notification, err := conn.Accept(ctx)
			if err != nil {
				if errors.Is(err, ErrListenerClosed) {
					return // the returned Closer was closed; an orderly shutdown, not an error.
				}

				if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
					return // the connection went away underneath us, e.g. the pool was closed.
				}

				if callback(evt, err) != nil {
					return
				}

				// notification is nil here (e.g. ErrEmptyPayload from Accept): never
				// dereference it, just keep listening.
				continue
			}

			// make payload available for debugging on errors.
			evt.payload = notification.Payload

			if err = json.Unmarshal([]byte(notification.Payload), &evt); err != nil {
				if callback(evt, err) != nil {
					return
				}

				// evt is only half-populated at this point (Unmarshal failed partway
				// through); skip the success callback below instead of invoking it twice.
				continue
			}

			if err = callback(evt, nil); err != nil {
				return
			}
		}
	}()

	return conn, nil
}
