package benchmarks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/kataras/pg"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Customer is a struct that represents a customer entity in the database.
type Customer struct {
	ID        string    `pg:"type=uuid,primary"`
	CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
	UpdatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
	// CognitoUserID string    `pg:"type=uuid,unique,conflict=DO UPDATE SET cognito_user_id=EXCLUDED.cognito_user_id"`
	CognitoUserID string `pg:"type=uuid,unique"`
}

var (
	dsn = "host=localhost user=postgres password=admin!123 dbname=test_db sslmode=disable search_path=public"
)

// go test -benchtime=5s -benchmem -run=^$ -bench ^BenchmarkDB_Insert*

// go test -bench=BenchmarkDB_InsertSingle_Gorm -count 6 | tee result_gorm.txt
func BenchmarkDB_InsertSingle_Gorm(b *testing.B) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		b.Fatal(err)
	}

	/* To create the schema:
	db.AutoMigrate(&Customer{})
	*/

	// This doesn't even works...
	// db.Clauses(clause.OnConflict{DoUpdates: clause.AssignmentColumns([]string{"cognito_user_id"})}).
	// db.Clauses(clause.OnConflict{DoUpdates: clause.Assignments(map[string]any{"cognito_user_id": `EXCLUDED.cognito_user_id`})}).

	customer := Customer{
		CognitoUserID: uuid.NewString(),
	}

	db.
		Omit("id", "created_at", "updated_at").
		Create(&customer)
}

// go test -bench=BenchmarkDB_InsertSingle_Pg -count 6 | tee result_pg.txt
func BenchmarkDB_InsertSingle_Pg(b *testing.B) {
	var schema = pg.NewSchema().MustRegister("customers", Customer{})

	db, err := pg.Open(context.Background(), schema, dsn)
	if err != nil {
		b.Fatal(err)
	}

	/* To create the schema:
	db.CreateSchema(context.Background())
	*/

	// Automatically takes care of id, created_at and updated_at fields.
	customer := Customer{CognitoUserID: uuid.NewString()}

	err = db.InsertSingle(context.Background(), customer, &customer.ID)
	if err != nil {
		b.Fatal(err)
	}

	/* To create a record from repository (static types):
	repo := pg.NewRepository[Customer](db)
	err := repo.InsertSingle(context.Background(), customer, &customer.ID)
	*/
}

// benchstat result_gorm.txt result_pg.txt
// ± 351%
