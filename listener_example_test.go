package pg

import (
	"context"
	"fmt"
	"time"
)

// go test -coverprofile=cov
// go tool cover -html=cov
func ExampleDB_Listen() {
	db, err := openEmptyTestConnection()
	if err != nil {
		handleExampleError(err)
		return
	}
	// defer db.Close()

	const channel = "chat_db"

	conn, err := db.Listen(context.Background(), channel)
	if err != nil {
		fmt.Println(fmt.Errorf("listen: %w\n", err))
		return
	}

	go func() {
		// To just terminate this listener's connection and unlisten from the channel:
		defer conn.Close(context.Background())

		for {
			notification, err := conn.Accept(context.Background())
			if err != nil {
				fmt.Println(fmt.Errorf("accept: %w\n", err))
				return
			}

			fmt.Printf("channel: %s, payload: %s\n", notification.Channel, notification.Payload)
		}
	}()

	if err = db.Notify(context.Background(), channel, "hello"); err != nil {
		fmt.Println(fmt.Errorf("notify: hello: %w", err))
		return
	}

	if err = db.Notify(context.Background(), channel, "world"); err != nil {
		fmt.Println(fmt.Errorf("notify: world: %w", err))
		return
	}

	time.Sleep(5 * time.Second) // give it sometime to receive the notifications.
	// Output:
	// channel: chat_db, payload: hello
	// channel: chat_db, payload: world
}

type Message struct {
	BaseEntity

	Sender string `pg:"type=varchar(255)" json:"sender"`
	Body   string `pg:"type=text" json:"body"`
}

func Example_notify_JSON() {
	schema := NewSchema()
	db, err := Open(context.Background(), schema, getTestConnString())
	if err != nil {
		fmt.Println(err)
		return
	}
	// defer db.Close()

	const channel = "chat_json"

	conn, err := db.Listen(context.Background(), channel)
	if err != nil {
		fmt.Println(fmt.Errorf("listen: %w", err))
	}

	go func() {
		// To just terminate this listener's connection and unlisten from the channel:
		defer conn.Close(context.Background())

		for {
			notification, err := conn.Accept(context.Background())
			if err != nil {
				fmt.Println(fmt.Errorf("accept: %w\n", err))
				return
			}

			payload, err := UnmarshalNotification[Message](notification)
			if err != nil {
				fmt.Println(fmt.Errorf("N: %w", err))
				return
			}

			fmt.Printf("channel: %s, payload.sender: %s, payload.body: %s\n",
				notification.Channel, payload.Sender, payload.Body)
		}
	}()

	firstMessage := Message{
		Sender: "kataras",
		Body:   "hello",
	}
	if err = db.Notify(context.Background(), channel, firstMessage); err != nil {
		fmt.Println(fmt.Errorf("notify: first message: %w", err))
		return
	}

	secondMessage := Message{
		Sender: "kataras",
		Body:   "world",
	}

	if err = db.Notify(context.Background(), channel, secondMessage); err != nil {
		fmt.Println(fmt.Errorf("notify: second message: %w", err))
		return
	}

	time.Sleep(5 * time.Second) // give it sometime to receive the notifications, this is too much though.
	// Output:
	// channel: chat_json, payload.sender: kataras, payload.body: hello
	// channel: chat_json, payload.sender: kataras, payload.body: world
}
