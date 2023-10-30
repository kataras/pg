package pg

import (
	"fmt"
	"time"
)

// Structs.

// BaseEntity is a struct that defines common fields for all entities in the database.
// It has an ID field of type uuid that is the primary key, and two timestamp fields
// for tracking the creation and update times of each row.
type BaseEntity struct {
	ID        string    `pg:"type=uuid,primary"`
	CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
	UpdatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
}

// Customer is a struct that represents a customer entity in the database.
// It embeds the BaseEntity struct and adds a CognitoUserID field of type uuid
// that is required and unique. It also specifies a conflict resolution strategy
// for the CognitoUserID field in case of duplicate values.
type Customer struct {
	BaseEntity
	// CognitoUserID string `pg:"type=uuid,unique,conflict=DO UPDATE SET cognito_user_id=EXCLUDED.cognito_user_id"`

	CognitoUserID string `pg:"type=uuid,unique_index=customer_unique_idx"`
	Email         string `pg:"type=varchar(255),unique_index=customer_unique_idx"`
	// ^ optional: unique to allow upsert by "email"-only column confliction instead of the unique_index.
	Name string `pg:"type=varchar(255),index=btree"`

	Username string `pg:"type=varchar(255),default=''"`
}

// Blog is a struct that represents a blog entity in the database.
// It embeds the BaseEntity struct and has no other fields.
type Blog struct {
	BaseEntity

	Name string `pg:"type=varchar(255)"`
}

// BlogPost is a struct that represents a blog post entity in the database.
// It embeds the BaseEntity struct and adds several fields for the blog post details,
// such as BlogID, Title, PhotoURL, SourceURL, ReadTimeMinutes, and Category.
// The BlogID field is a foreign key that references the ID field of the blogs table,
// with cascade option for deletion and deferrable option for constraint checking.
// The Title and SourceURL fields are part of a unique index named uk_blog_post,
// which ensures that no two blog posts have the same title or source URL.
// The ReadTimeMinutes field is a smallint with a default value of 1 and a check constraint
// that ensures it is positive. The Category field is a smallint with a default value of 0.
type BlogPost struct {
	BaseEntity

	BlogID          string `pg:"type=uuid,index,ref=blogs(id cascade deferrable)"`
	Title           string `pg:"type=varchar(255),unique_index=uk_blog_post"`
	PhotoURL        string `pg:"type=varchar(255)"`
	SourceURL       string `pg:"type=varchar(255),unique_index=uk_blog_post"`
	ReadTimeMinutes int    `pg:"type=smallint,default=1,check=read_time_minutes > 0"`
	Category        int    `pg:"type=smallint,default=0"`

	SearchTerms   []string        `pg:"type=varchar[]"` // Test a slice of strings.
	ReadDurations []time.Duration `pg:"type=bigint[]"`  // Test a slice of time.Duration based on an int64.

	// Custom types.
	Feature       Feature  `pg:"type=jsonb"` // Test a JSON structure.
	OtherFeatures Features `pg:"type=jsonb"` // Test a JSON array of structures behind a custom type.
	Tags          []Tag    `pg:"type=jsonb"` // Test a JSON array of structures.
}

type Features []Feature

type Feature struct {
	IsFeatured bool `json:"is_featured"`
}

type Tag struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

func ExampleNewSchema() {
	// Database code.
	schema := NewSchema()
	schema.MustRegister("customers", Customer{})  // Register the Customer struct as a table named "customers".
	schema.MustRegister("blogs", Blog{})          // Register the Blog struct as a table named "blogs".
	schema.MustRegister("blog_posts", BlogPost{}) // Register the BlogPost struct as a table named "blog_posts".

	fmt.Println("OK")
	// Output:
	// OK
}
