package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

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

	var b strings.Builder
	for i, change := range changes {
		b.WriteString(string(change))
		if i < len(changes)-1 {
			b.WriteString(" OR ")
		}
	}

	return b.String()
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
		Table  string          `json:"table"`
		Change TableChangeType `json:"change"` // INSERT, UPDATE, DELETE.

		New T `json:"new"`
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
	Tables map[string][]TableChangeType

	// Channel is the name of the postgres channel to listen on.
	// Default: "table_change_notifications".
	Channel string

	// Function is the name of the postgres function
	// which is used to notify on table changes, the
	// trigger name is <table_name>_<Function>.
	// Defaults to "table_change_notify".
	Function string
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

	isWildcard := false
	for table := range opts.Tables {
		if table == wildcardTableStr {
			isWildcard = true
			break
		}
	}

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

// PrepareListenTable prepares the table for listening for live table updates.
// See "db.ListenTable" method for more.
func (db *DB) prepareListenTable(ctx context.Context, channel, function, table string, changes []TableChangeType) error {
	if table == "" {
		return errors.New("empty table name")
	}

	if len(changes) == 0 {
		return nil
	}

	if atomic.LoadUint32(db.tableChangeNotifyFunctionOnce) == 0 {
		// First, check and create the trigger for all tables.
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
			return fmt.Errorf("create or replace function table_change_notify: %w", err)
		}

		atomic.StoreUint32(db.tableChangeNotifyFunctionOnce, 1)
	}

	db.tableChangeNotifyOnceMutex.RLock()
	_, triggerCreated := db.tableChangeNotifyTriggerOnce[table]
	db.tableChangeNotifyOnceMutex.RUnlock()
	if !triggerCreated {
		query := fmt.Sprintf(`CREATE OR REPLACE TRIGGER %s_%s
        AFTER %s
        ON %s
        FOR EACH ROW
        EXECUTE FUNCTION table_change_notify();`, table, function, changesToString(changes), table)

		_, err := db.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("create trigger %s_table_change_notify: %w", table, err)
		}

		db.tableChangeNotifyOnceMutex.Lock()
		db.tableChangeNotifyTriggerOnce[table] = struct{}{}
		db.tableChangeNotifyOnceMutex.Unlock()
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
				if errors.Is(err, io.ErrUnexpectedEOF) {
					return // may produced by close.
				}

				if callback(evt, err) != nil {
					return
				}
			}

			// make payload available for debugging on errors.
			evt.payload = notification.Payload

			if err = json.Unmarshal([]byte(notification.Payload), &evt); err != nil {
				if callback(evt, err) != nil {
					return
				}
			}

			if err = callback(evt, nil); err != nil {
				//	callback(evt, err)
				return
			}
		}
	}()

	return conn, nil
}
