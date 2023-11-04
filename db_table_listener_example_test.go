package pg

import (
	"context"
	"fmt"
	"time"
)

func ExampleDB_ListenTable() {
	db, err := openTestConnection()
	if err != nil {
		handleExampleError(err)
		return
	}
	defer db.Close()

	opts := &ListenTableOptions{
		Tables: map[string][]TableChangeType{"customers": defaultChangesToWatch},
	}
	closer, err := db.ListenTable(context.Background(), opts, func(evt TableNotificationJSON, err error) error {
		if err != nil {
			fmt.Printf("received error: %v\n", err)
			return err
		}

		if evt.Change == "INSERT" {
			fmt.Printf("table: %s, event: %s, old: %s\n", evt.Table, evt.Change, string(evt.Old)) // new can't be predicated through its ID and timestamps.
		} else {
			fmt.Printf("table: %s, event: %s\n", evt.Table, evt.Change)
		}

		return nil
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer closer.Close(context.Background())

	newCustomer := Customer{
		CognitoUserID: "766064d4-a2a7-442d-aa75-33493bb4dbb9",
		Email:         "kataras2024@hotmail.com",
		Name:          "Makis",
	}
	err = db.InsertSingle(context.Background(), newCustomer, &newCustomer.ID)
	if err != nil {
		fmt.Println(err)
		return
	}

	newCustomer.Name = "Makis_UPDATED"
	_, err = db.UpdateOnlyColumns(context.Background(), []string{"name"}, newCustomer)
	if err != nil {
		fmt.Println(err)
		return
	}
	time.Sleep(5 * time.Second) // give it sometime to receive the notifications.
	// Output:
	// table: customers, event: INSERT, old: null
	// table: customers, event: UPDATE
}
