package pg

import (
	"context"
	"fmt"
	"time"
)

func ExampleRepository_ListenTable() {
	db, err := openTestConnection()
	if err != nil {
		handleExampleError(err)
		return
	}
	defer db.Close()

	customers := NewRepository[Customer](db)

	closer, err := customers.ListenTable(context.Background(), func(evt TableNotification[Customer], err error) error {
		if err != nil {
			fmt.Printf("received error: %v\n", err)
			return err
		}

		fmt.Printf("table: %s, event: %s, old name: %s new name: %s\n", evt.Table, evt.Change, evt.Old.Name, evt.New.Name)
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
	err = customers.InsertSingle(context.Background(), newCustomer, &newCustomer.ID)
	if err != nil {
		fmt.Println(err)
		return
	}

	newCustomer.Name = "Makis_UPDATED"
	_, err = customers.UpdateOnlyColumns(context.Background(), []string{"name"}, newCustomer)
	if err != nil {
		fmt.Println(err)
		return
	}
	time.Sleep(5 * time.Second) // give it sometime to receive the notifications.
	// Output:
	// table: customers, event: INSERT, old name:  new name: Makis
	// table: customers, event: UPDATE, old name: Makis new name: Makis_UPDATED
}
