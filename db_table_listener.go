package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
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
	}

	// TableNotificationJSON is the generic version of the TableNotification.
	TableNotificationJSON = TableNotification[json.RawMessage]
)

// ListenTable registers a function which notifies on the given "table" changes (INSERT, UPDATE, DELETE),
// the subscribed postgres channel is named 'table_change_notifications'.
//
// The callback function can return ErrStop to stop the listener without actual error.
// The callback function can return any other error to stop the listener and return the error.
// The callback function can return nil to continue listening.
//
// TableNotification's New and Old fields are raw json values, use the "json.Unmarshal" to decode them
// to the actual type.
func (db *DB) ListenTable(ctx context.Context, table string, callback func(TableNotificationJSON, error) error) (Closer, error) {
	channelName := "table_change_notifications"

	if atomic.LoadUint32(db.tableChangeNotifyFunctionOnce) == 0 {
		// First, check and create the trigger for all tables.
		query := fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION table_change_notify() RETURNS trigger AS $$
			DECLARE
			payload text;
			channel text := '%s';
			
			BEGIN
			SELECT json_build_object('table', TG_TABLE_NAME, 'change', TG_OP, 'old', OLD, 'new', NEW)::text
			INTO payload;
			PERFORM pg_notify(channel, payload);
			RETURN NEW;
		END; 
		$$
		LANGUAGE plpgsql;`, channelName)

		_, err := db.Exec(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("create or replace function table_change_notify: %w", err)
		}

		atomic.StoreUint32(db.tableChangeNotifyFunctionOnce, 1)
	}

	db.tableChangeNotifyOnceMutex.RLock()
	_, triggerCreated := db.tableChangeNotifyTriggerOnce[table]
	db.tableChangeNotifyOnceMutex.RUnlock()
	if !triggerCreated {
		query := `CREATE TRIGGER ` + table + `_table_change_notify
        BEFORE INSERT OR
               UPDATE OR
               DELETE
        ON ` + table + `
        FOR EACH ROW
        EXECUTE FUNCTION table_change_notify();`

		_, err := db.Exec(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("create trigger %s_table_change_notify: %w", table, err)
		}

		db.tableChangeNotifyOnceMutex.Lock()
		db.tableChangeNotifyTriggerOnce[table] = struct{}{}
		db.tableChangeNotifyOnceMutex.Unlock()
	}

	conn, err := db.Listen(ctx, channelName)
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

			if err = json.Unmarshal([]byte(notification.Payload), &evt); err != nil {
				if callback(evt, err) != nil {
					return
				}
			}

			if err = callback(evt, nil); err != nil {
				return
			}
		}
	}()

	return conn, nil
}
