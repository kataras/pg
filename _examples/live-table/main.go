package main

import (
	"context"
	"database/sql/driver"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kataras/pg"
)

/*
Example of real-time database table to html5 table view using Go, Postgres, HTML and Websockets.

1. Modify the connectionString variable to match your database connection string.
2. Execute `go run main.go`
3. Open a browser window at http://localhost:8080
3.1 Open your database client and execute `INSERT INTO users (email, name, username) VALUES ('john.doe@example.com', 'John Doe', 'johndoe');`
3.2 Make any changes to the users table, e.g. insert new row, update and delete,
4. See the changes in real-time in your browser window (HTML Table).
*/

const connectionString = "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable"

func main() {
	// Database.
	schema := pg.NewSchema()
	schema.MustRegister("users", User{})

	db, err := pg.Open(context.Background(), schema, connectionString)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err = db.CreateSchema(context.Background()); err != nil {
		log.Fatal(err)
	}

	users := pg.NewRepository[User](db)
	closer, err := users.ListenTable(context.Background(), func(tn pg.TableNotification[User], err error) error {
		if err != nil {
			log.Fatalf("%v\nPayload:\n%s", err, tn.GetPayload())
			return err // to stop the listener (even without log.Fatal).
		}

		// log.Printf("Database change: %s\n", tn.Change)

		switch tn.Change {
		case pg.TableChangeTypeInsert:
			message := WebsocketMessage{
				Type: "insert",
				Data: tn.New,
			}
			sendMessageToAllClients(message)
		case pg.TableChangeTypeUpdate:
			message := WebsocketMessage{
				Type: "update",
				Data: tn.New,
			}
			sendMessageToAllClients(message)
		case pg.TableChangeTypeDelete:
			message := WebsocketMessage{
				Type: "delete",
				Data: tn.Old,
			}
			sendMessageToAllClients(message)
		default:
			return nil
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	defer closer.Close(context.Background())

	// Insert a new user.
	// user := User{
	// 	Email:    "john.doe@example.com",
	// 	Name:     "John Doe",
	// 	Username: "johndoe",
	// }
	// err = users.InsertSingle(context.Background(), user, &user.ID)
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// HTTP Server.
	http.HandleFunc("/", index)
	http.HandleFunc("/websocket", handleWebsocket(users))

	log.Println("Listening on http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}

func index(w http.ResponseWriter, r *http.Request) {
	r.Header.Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFile(w, r, "index.html")
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // Accept all requests.
}

var (
	// Keep track of all connected clients.
	clients = make(map[*websocket.Conn]struct{})
	// Protect the clients map.
	mu sync.RWMutex
)

// WebsocketMessage is the message that we send to the client (browser).
type WebsocketMessage struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func sendMessageToAllClients(message WebsocketMessage) {
	log.Printf("Sending message to %d client(s): %#+v", len(clients), message)

	mu.RLock()
	defer mu.RUnlock()

	for client := range clients {
		err := client.WriteJSON(message)
		if err != nil {
			log.Printf("write message: %v", err)
		}
	}
}

// ClientMessage is the message that we receive from the client (browser).
type ClientMessage struct {
	Text string `json:"text"`
}

func handleWebsocket(repo *pg.Repository[User]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer connection.Close()

		// Add the client connection to the global connections, so we can send the message in ListenTable.
		mu.Lock()
		clients[connection] = struct{}{}
		mu.Unlock()

		// Don't forget to delete the client from the global connections.
		defer func() {
			mu.Lock()
			delete(clients, connection)
			mu.Unlock()
		}()

		for {
			// Read message from the client (browser).
			var message ClientMessage
			err := connection.ReadJSON(&message)
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseGoingAway) {
					break
				}

				log.Printf("read message: %v", err)
				break
			}

			if message.Text == "ask_view" {
				userList, err := repo.Select(context.Background(), `SELECT * FROM users ORDER BY created_at DESC LIMIT 500;`)
				if err != nil {
					log.Println(err)
					break
				}

				serverMessage := WebsocketMessage{
					Type: "view",
					Data: userList,
				}
				// Write back to the client (browser).
				err = connection.WriteJSON(serverMessage)
				if err != nil {
					log.Printf("write message: %v", err)
					break
				}
			}
		}
	}
}

// BaseEntity is a struct that defines common fields for all entities in the database.
// It has an ID field of type uuid that is the primary key, and two timestamp fields
// for tracking the creation and update times of each row.
type BaseEntity struct {
	ID        string `pg:"type=uuid,primary" json:"id"`
	CreatedAt ISO8601/* time.Time */ `pg:"type=timestamp,default=clock_timestamp()" json:"created_at"`
	UpdatedAt ISO8601/* time.Time */ `pg:"type=timestamp,default=clock_timestamp()" json:"updated_at"`
}

// User is our example database entity.
type User struct {
	BaseEntity

	Email    string `pg:"type=varchar(255),unique_index=user_unique_idx" json:"email"`
	Name     string `pg:"type=varchar(255),index=btree" json:"name"`
	Username string `pg:"type=varchar(255),default=''" json:"username"`
}

// ISO8601 is just an example of custom type which implements Value and Scan methods.
// It describes a time compatible with javascript time format.
// A tiny clopy of: https://github.com/kataras/iris/blob/main/x/jsonx/iso8601.go.
type ISO8601 time.Time

const (
	// ISO8601Layout holds the time layout for the the javascript iso time.
	// Read more at: https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/Date/toISOString.
	ISO8601Layout = "2006-01-02T15:04:05"
	// ISO8601ZLayout same as ISO8601Layout but with the timezone suffix.
	ISO8601ZLayout = "2006-01-02T15:04:05Z"
	// ISO8601ZUTCOffsetLayout ISO 8601 format, with full time and zone with UTC offset.
	// Example: 2022-08-10T03:21:00.000000+03:00, 2023-02-04T09:48:14+00:00, 2022-08-09T00:00:00.000000.
	ISO8601ZUTCOffsetLayout = "2006-01-02T15:04:05.999999Z07:00" // -07:00
)

// ParseISO8601 reads from "s" and returns the ISO8601 time.
func ParseISO8601(s string) (ISO8601, error) {
	if s == "" || s == "null" {
		return ISO8601{}, nil
	}

	var (
		tt  time.Time
		err error
	)

	if s[len(s)-1] == 'Z' {
		tt, err = time.Parse(ISO8601ZLayout, s)
	} else {
		tt, err = time.Parse(ISO8601Layout, s)
	}

	if err != nil {
		return ISO8601{}, fmt.Errorf("ISO8601: %w", err)
	}

	return ISO8601(tt), nil
}

// UnmarshalJSON parses the "b" into ISO8601 time.
func (t *ISO8601) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return nil
	}

	s := strings.Trim(string(b), `"`)
	tt, err := ParseISO8601(s)
	if err != nil {
		return err
	}

	*t = tt
	return nil
}

// MarshalJSON writes a quoted string in the ISO8601 time format.
func (t ISO8601) MarshalJSON() ([]byte, error) {
	if s := t.String(); s != "" {
		s = strconv.Quote(s)
		return []byte(s), nil
	}

	return []byte("null"), nil
}

// ToTime returns the unwrapped *t to time.Time.
func (t *ISO8601) ToTime() time.Time {
	tt := time.Time(*t)
	return tt
}

// String returns the text representation of the "t" using the ISO8601 time layout.
func (t ISO8601) String() string {
	tt := t.ToTime()
	if tt.IsZero() {
		return ""
	}

	return tt.Format(ISO8601Layout)
}

// Value returns the database value of time.Time.
func (t ISO8601) Value() (driver.Value, error) {
	return time.Time(t), nil
}

// Scan completes the sql driver.Scanner interface.
func (t *ISO8601) Scan(src interface{}) error {
	switch v := src.(type) {
	case time.Time: // type was set to timestamp
		if v.IsZero() {
			return nil // don't set zero, ignore it.
		}
		*t = ISO8601(v)
	case string:
		tt, err := ParseISO8601(v)
		if err != nil {
			return err
		}
		*t = tt
	case []byte:
		return t.Scan(string(v))
	case nil:
		*t = ISO8601(time.Time{})
	default:
		return fmt.Errorf("ISO8601: unknown type of: %T", v)
	}

	return nil
}
