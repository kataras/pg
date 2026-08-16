package pg

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// TestSchemaConcurrent registers many distinct struct types concurrently while
// other goroutines concurrently read the Schema through Get, GetByTableName,
// Tables and TableNames. It exercises Schema's internal locking (schema.go)
// and is meaningful evidence of correctness only when run with `go test -race`;
// the race detector is unavailable on this development machine (windows/arm64),
// so it is run here without -race (`go test -run TestSchemaConcurrent .`) and
// still exercises the locking paths without crashing. CI (linux/amd64) runs it
// under -race later in this hardening pass.
func TestSchemaConcurrent(t *testing.T) {
	const n = 64

	schema := NewSchema()

	types := make([]reflect.Type, n)
	values := make([]any, n)
	tableNames := make([]string, n)

	for i := 0; i < n; i++ {
		// Build N structurally distinct struct types at runtime so that
		// Register genuinely inserts N independent entries rather than
		// racing multiple goroutines on the same map key.
		typ := reflect.StructOf([]reflect.StructField{
			{
				Name: fmt.Sprintf("Field%d", i),
				Type: reflect.TypeFor[string](),
				Tag:  reflect.StructTag(`pg:"type=uuid,primary"`),
			},
		})

		types[i] = typ
		values[i] = reflect.New(typ).Elem().Interface()
		tableNames[i] = fmt.Sprintf("race_table_%d", i)
	}

	stop := make(chan struct{})

	var readersWG sync.WaitGroup
	for i := 0; i < n; i++ {
		readersWG.Add(1)
		go func(i int) {
			defer readersWG.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				_, _ = schema.GetByTableName(tableNames[i])
				_, _ = schema.Get(types[i])
				_ = schema.Tables()
				_ = schema.TableNames()
				_ = schema.Last()
			}
		}(i)
	}

	var (
		writersWG sync.WaitGroup
		errsMu    sync.Mutex
		errs      []error
	)

	for i := 0; i < n; i++ {
		writersWG.Add(1)
		go func(i int) {
			defer writersWG.Done()

			if _, err := schema.Register(tableNames[i], values[i]); err != nil {
				errsMu.Lock()
				errs = append(errs, fmt.Errorf("register %s: %w", tableNames[i], err))
				errsMu.Unlock()
			}
		}(i)
	}

	writersWG.Wait()
	close(stop)
	readersWG.Wait()

	for _, err := range errs {
		t.Error(err)
	}

	if got := len(schema.Tables()); got != n {
		t.Fatalf("expected %d registered tables, got %d", n, got)
	}

	for i := 0; i < n; i++ {
		td, err := schema.GetByTableName(tableNames[i])
		if err != nil {
			t.Fatalf("GetByTableName(%s): unexpected error: %v", tableNames[i], err)
		}

		if td.Name != tableNames[i] {
			t.Fatalf("GetByTableName(%s): got table name %s", tableNames[i], td.Name)
		}

		if _, err := schema.Get(types[i]); err != nil {
			t.Fatalf("Get(%s): unexpected error: %v", tableNames[i], err)
		}
	}
}

// TestSchemaGetByTableNameNotFound verifies the exact not-found error shape
// GetByTableName returns for a table name that was never registered.
func TestSchemaGetByTableNameNotFound(t *testing.T) {
	schema := NewSchema()

	_, err := schema.GetByTableName("does_not_exist")
	if err == nil {
		t.Fatal("expected an error for an unregistered table name")
	}

	const want = "table does_not_exist was not registered, forgot Schema.Register?"
	if err.Error() != want {
		t.Fatalf("expected error %q, got %q", want, err.Error())
	}
}

// TestSchemaGetByTableNameFound verifies that GetByTableName finds a table
// registered through Register/MustRegister via its new map-backed lookup.
func TestSchemaGetByTableNameFound(t *testing.T) {
	type customer struct {
		ID string `pg:"type=uuid,primary"`
	}

	schema := NewSchema()
	schema.MustRegister("customers", customer{})

	td, err := schema.GetByTableName("customers")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if td.Name != "customers" {
		t.Fatalf("expected table name %q, got %q", "customers", td.Name)
	}
}
