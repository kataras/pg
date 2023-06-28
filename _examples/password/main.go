package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/kataras/pg"
	"github.com/kataras/pg/gen"
)

func init() {
	pg.SetDefaultTag("pg") // you can modify it to "db" as well.
}

type Base struct {
	ID        string    `pg:"type=uuid,primary"`
	CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
	UpdatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
}

type User struct {
	Base

	Firstname string `pg:"type=varchar(255)"`
	Lastname  string `pg:"type=varchar(255)"`
	Email     string `pg:"type=varchar(255),username,unique,conflict=DO UPDATE SET email=EXCLUDED.email"`
	Password  string `pg:"type=varchar(72),password" json:"password,omitempty"`
}

/*
// Use a PasswordHandler and Encrypt and Decrypt to manually encrypt and decrypt passwords.
// However, for better security, just use the: `pg:"type=varchar(72),password"` tag for password fields
// and let the library do the job for you.
var passwordHandler = pg.PasswordHandler{
	Encrypt: func(tableName, plainPassword string) (encryptedPassword string, err error) {
		return
	},
	// If you don't want to set passwords on Select then skip this Decrypt field.
	Decrypt: func(tableName, encryptedPassword string) (plainPassword string, err error) {
		return
	},
}

schema.HandlePassword(passwordHandler)
*/

func main() {
	// Create Schema instance.
	schema := pg.NewSchema()
	schema.MustRegister("users", User{})

	// Optionally generate the files for the given schema.
	// This can be used to statically have access to column names of each registered table.
	// It's not required to run this, it's just a helper
	// for a separate CLI flag to generate-only your table definition.
	//
	// Generated code usage:
	// definition.User.PG_TableName // "users"
	// definition.User.CreatedAt.String() // "created_at"
	// definition.User.Firstname.String() // "firstname"
	defer func() {
		opts := gen.ExportOptions{
			RootDir: "./definition",
		}
		gen.GenerateColumnsFromSchema(schema, &opts)
	}()
	// Create Database instance.
	/*
		Available connection string formats:
		-
		connString := fmt.Sprintf("host=%s port=%d user=%s password=%s search_path=%s dbname=%s sslmode=%s",
			host, port, user, password, schema, dbname, sslMode)
		-
		connString := "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable&search_path=public"
	*/

	connString := "postgres://postgres:admin!123@localhost:5432/test_db?sslmode=disable&search_path=public"
	db, err := pg.Open(context.Background(), schema, connString)
	if err != nil {
		log.Fatal(fmt.Errorf("open database: %w", err))
	}
	defer db.Close()

	if err = db.CreateSchema(context.Background()); err != nil {
		log.Fatal(fmt.Errorf("create schema: %w", err))
	}

	if err = db.CheckSchema(context.Background()); err != nil {
		log.Fatal(fmt.Errorf("check schema: %w", err))
	}

	// Create a Repository of User type.
	users := pg.NewRepository[User](db)

	var newUser = User{
		Firstname: "John",
		Lastname:  "Doe",
		Email:     "kataras2006@hotmail.com",
		Password:  "123456",
	}

	// Insert a new User with credentials.
	err = users.InsertSingle(context.Background(), newUser, &newUser.ID)
	if err != nil {
		log.Fatal(fmt.Errorf("insert user: %w", err))
	}

	// Get by id.
	query := `SELECT * FROM users WHERE id = $1 LIMIT 1;`
	existingUser, err := users.SelectSingle(context.Background(), query, newUser.ID)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Existing User (SelectSingle):\n%#+v\n", existingUser)

	// Check credentials.
	verifiedUser, err := users.SelectByUsernameAndPassword(context.Background(), "kataras2006@hotmail.com", "123456")
	if err != nil { // will return pg.ErrNoRows if not found (invalid username or password).
		log.Fatal(err)
	}
	verifiedUser.Password = "" // clear the password if you want (it contains the encrypted anyways).

	log.Printf("Verified User (SelectByUsernameAndPassword):\n%#+v\n", verifiedUser)
}
