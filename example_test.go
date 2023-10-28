package pg

import (
	"context"
	"fmt"
	"reflect"
	"time"
)

// Example is a function that demonstrates how to use the Registry and Repository types
// to perform database operations within a transaction. It uses the Customer, Blog, and BlogPost structs
// as the entities to be stored and manipulated in the database. It also prints "OK" if everything succeeds,
// or an error message otherwise.
func Example() {
	db, err := openTestConnection()
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	defer db.Close()

	// Registry code.
	registry := NewRegistry(db) // Create a new Registry instance with the DB instance.

	// Execute a function within a database transaction, passing it a Registry instance that uses the transactional DB instance.
	err = registry.InTransaction(context.Background(), func(registry *Registry) error {
		customers := registry.Customers() // Get the CustomerRepository instance from the Registry.

		customerToInsert := Customer{ // Create a Customer struct to be inserted into the database.
			CognitoUserID: "373f90eb-00ac-410f-9fe0-1a7058d090ba",
			Email:         "kataras2006@hotmail.com",
			Name:          "kataras",
			Username:      "kataras",
		}

		// Insert the customer into the database and get its ID.
		err = customers.InsertSingle(context.Background(), customerToInsert, &customerToInsert.ID)
		if err != nil {
			return fmt.Errorf("insert single: %w", err)
		}

		// Modify cognito user id.
		newCognitoUserID := "1e6a93d0-6276-4a43-b90a-4badad8407bb"
		// Update specific columns by id:
		updated, err := customers.UpdateOnlyColumns(
			context.Background(),
			[]string{"cognito_user_id"},
			Customer{
				BaseEntity: BaseEntity{
					ID: customerToInsert.ID,
				},
				CognitoUserID: newCognitoUserID,
			})
		// Full update of the object by id (except id and created_at, updated_at columns):
		// updated, err := customers.Update(context.Background(),
		// 	Customer{
		// 		BaseEntity: BaseEntity{
		// 			ID: customerToInsert.ID,
		// 		},
		// 		CognitoUserID: newCognitoUserID,
		// 		Email:         customerToInsert.Email,
		// 		Name:          customerToInsert.Name,
		// 	})
		if err != nil {
			return fmt.Errorf("update: %w", err)
		} else if updated == 0 {
			return fmt.Errorf("update: no record was updated")
		}

		// Update a default column to its zero value.
		updated, err = customers.UpdateOnlyColumns(
			context.Background(),
			[]string{"username"},
			Customer{
				BaseEntity: BaseEntity{
					ID: customerToInsert.ID,
				},
				Username: "",
			})
		if err != nil {
			return fmt.Errorf("update username: %w", err)
		} else if updated == 0 {
			return fmt.Errorf("update username: no record was updated")
		}
		// Select the customer from the database by its ID.
		customer, err := customers.SelectSingle(context.Background(), `SELECT * FROM customers WHERE id = $1;`, customerToInsert.ID)
		if err != nil {
			return fmt.Errorf("select single: %w", err)
		}

		if customer.CognitoUserID != newCognitoUserID {
			return fmt.Errorf("expected cognito user id to be updated but it wasn't ('%s' vs '%s')",
				newCognitoUserID, customer.CognitoUserID)
		}
		if customer.Email == "" {
			return fmt.Errorf("expected email field not be removed after update")
		}
		if customer.Name == "" {
			return fmt.Errorf("expected name field not be removed after update")
		}

		// Test Upsert by modifying the email.
		customerToUpsert := Customer{
			CognitoUserID: customer.CognitoUserID,
			Email:         "kataras2023@hotmail.com",
			Name:          "kataras2023",
		}

		// Manually passing a column as the conflict column:
		// err = customers.UpsertSingle(context.Background(), "email", customerToUpsert, &customerToUpsert.ID)
		//
		// Automatically find the conflict column or expression by setting it to empty value:
		// err = customers.UpsertSingle(context.Background(), "", customerToUpsert, &customerToUpsert.ID)
		// Manually passing a unique index name, pg will resolve the conflict columns:
		err = customers.UpsertSingle(context.Background(), "customer_unique_idx", customerToUpsert, &customerToUpsert.ID)
		if err != nil {
			return fmt.Errorf("upsert single: %w", err)
		}

		if customerToUpsert.ID == "" {
			return fmt.Errorf("expected customer id to be filled after upsert")
		}

		// Delete the customer from the database by its struct value.
		deleted, err := customers.Delete(context.Background(), customer)
		if err != nil {
			return fmt.Errorf("delete: %w", err)
		} else if deleted == 0 {
			return fmt.Errorf("delete: was not removed")
		}

		exists, err := customers.Exists(context.Background(), customer.CognitoUserID)
		if err != nil {
			return fmt.Errorf("exists: %w", err)
		}
		if exists {
			return fmt.Errorf("exists: customer should not exist")
		}

		// Do something else with customers.
		return nil
	})

	if err != nil {
		fmt.Println(fmt.Errorf("in transaction: %w", err))
		return
	}

	// Insert a blog.
	blogs := registry.Blogs()
	newBlog := Blog{
		Name: "test_blog_1",
	}
	err = blogs.InsertSingle(context.Background(), newBlog, &newBlog.ID)
	if err != nil {
		fmt.Println(fmt.Errorf("insert single: blog: %w", err))
		return
	}

	// Insert a blog post to the blog.
	blogPosts := registry.BlogPosts()
	newBlogPost := BlogPost{
		BlogID:          newBlog.ID,
		Title:           "test_blog_post_1",
		PhotoURL:        "https://test.com/test_blog_post_1.png",
		SourceURL:       "https://test.com/test_blog_post_1.html",
		ReadTimeMinutes: 5,
		Category:        1,
		SearchTerms: []string{
			"test_search_blog_post_1",
			"test_search_blog_post_2",
		},
		ReadDurations: []time.Duration{
			5 * time.Minute,
			10 * time.Minute,
		},
		Feature: Feature{
			IsFeatured: true,
		},
		OtherFeatures: Features{
			Feature{
				IsFeatured: true,
			},
			Feature{
				IsFeatured: false,
			},
		},
		Tags: []Tag{
			{"test_tag_1", "test_tag_value_1"},
			{"test_tag_2", 42},
		},
	}
	err = blogPosts.InsertSingle(context.Background(), newBlogPost, &newBlogPost.ID)
	if err != nil {
		fmt.Println(fmt.Errorf("insert single: blog post: %w", err))
		return
	}

	query := `SELECT * FROM blog_posts WHERE id = $1 LIMIT 1;`
	existingBlogPost, err := blogPosts.SelectSingle(context.Background(), query, newBlogPost.ID)
	if err != nil {
		fmt.Println(fmt.Errorf("select single: blog post: %s: %w", newBlogPost.ID, err))
		return
	}

	// Test select single jsonb column of a custom type of array of custom types.
	//
	var otherFeatures Features
	err = blogPosts.QueryRow(
		context.Background(),
		`SELECT other_features FROM blog_posts WHERE id = $1 LIMIT 1;`,
		newBlogPost.ID,
	).Scan(&otherFeatures)
	// OR
	// otherFeatures, err := QuerySingle[Features](
	// 	context.Background(),
	// 	db,
	// 	`SELECT other_features FROM blog_posts WHERE id = $1 LIMIT 1;`,
	// 	newBlogPost.ID,
	// )
	if err != nil {
		fmt.Println(fmt.Errorf("select single jsonb column of custom array type of custom type: blog post: %s: %w", newBlogPost.ID, err))
		return
	}

	if expected, got := len(otherFeatures), len(existingBlogPost.OtherFeatures); expected != got {
		fmt.Printf("expected %d other_features but got %d", expected, got)
		return
	}

	if !reflect.DeepEqual(otherFeatures, existingBlogPost.OtherFeatures) {
		fmt.Printf("expected other_features to be equal but got %#+v and %#+v", otherFeatures, existingBlogPost.OtherFeatures)
		return
	}

	// Output:
	//
}
