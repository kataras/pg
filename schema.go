package pg

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/kataras/pg/desc"
)

// Schema is a type that represents a schema for the database.
type Schema struct {
	// structCache is a map from reflect.Type to Table
	// that stores the table definitions for the registered structs
	structCache  map[reflect.Type]*desc.Table
	orderedTypes []reflect.Type

	passwordHandler *desc.PasswordHandler // cache for tables.
	// The name of the "updated_at" column. Defaults to "updated_at" but it can be modified,
	// this is useful to set when triggers should be registered automatically.
	//
	// If set to empty then triggers will not be registered automatically.
	UpdatedAtColumnName string
	// Set the name of the trigger that sets the "updated_at" column, defaults to "set_timestamp".
	//
	// If set to empty then triggers will not be registered automatically.
	SetTimestampTriggerName string
}

// NewSchema creates and returns a new Schema with an initialized struct cache.
func NewSchema() *Schema {
	return &Schema{
		// make a map from reflect.Type to Table.
		structCache: make(map[reflect.Type]*desc.Table),
		// set the default name for the "updated_at" column.
		UpdatedAtColumnName: "updated_at",
		// set the default name for the trigger that sets the "updated_at" column.
		SetTimestampTriggerName: "set_timestamp",
	}
}

/*

type TextFunc = func(context.Context, string) (string, error)

func NewPasswordHandler(set, get TextFunc) PasswordHandler {
	return &plainPasswordHandler{
		setter: set,
		getter: get,
	}
}

type plainPasswordHandler struct {
	setter TextFunc
	getter TextFunc
}

func (h *plainPasswordHandler) Set(ctx context.Context, plainPassword string) (encryptedPassword string, err error) {
	return h.setter(ctx, plainPassword)
}

func (h *plainPasswordHandler) Get(ctx context.Context, encryptedPassword string) (plainPassword string, err error) {
	return h.getter(ctx, encryptedPassword)
}
*/

// HandlePassword sets the password handler.
func (s *Schema) HandlePassword(handler desc.PasswordHandler) *Schema {
	if handler.Encrypt == nil && handler.Decrypt == nil {
		return s
	}

	s.passwordHandler = &handler
	return s
}

// View is a TableFilterFunc that sets the table type to "view" and returns true.
//
// Example:
//
//	schema.MustRegister("customer_master", FullCustomer{}, pg.View)
var View = func(td *desc.Table) bool {
	td.Type = desc.TableTypeView
	return true
}

// Presenter is a TableFilterFunc that sets the table type to "presenter" and returns true.
// A presenter is a table that is used to present data from one or more tables with custom select queries.
// It's not a base table neither a view.
// Example:
//
//	schema.MustRegister("customer_presenter", CustomerPresenter{}, pg.Presenter)
var Presenter = func(td *desc.Table) bool {
	td.Type = desc.TableTypePresenter
	return true
}

// MustRegister same as "Register" but it panics on errors and returns the Schema instance instead of the Table one.
func (s *Schema) MustRegister(tableName string, emptyStructValue any, opts ...TableFilterFunc) *Schema {
	td, err := s.Register(tableName, emptyStructValue, opts...) // call Register with the same arguments
	if err != nil {                                             // if there is an error
		panic(err) // panic with the error
	}
	td.SetStrict(true)

	return s // return the table definition
}

// Register registers a database model (a struct value) mapped to a specific database table name.
// Returns the generated Table definition.
func (s *Schema) Register(tableName string, emptyStructValue any, opts ...TableFilterFunc) (*desc.Table, error) {
	typ := desc.IndirectType(reflect.TypeOf(emptyStructValue)) // get the underlying type of the struct value

	td, err := desc.ConvertStructToTable(tableName, typ) // convert the type to a table definition
	if err != nil {                                      // if there is an error
		return nil, err // return the error
	}

	td.RegisteredPosition = len(s.structCache) + 1 // assign the registered position as the current size of the cache plus one
	td.PasswordHandler = s.passwordHandler

	for _, opt := range opts {
		if !opt(td) { // do not register if returns false.
			return td, nil
		}
	}

	s.structCache[typ] = td // store the table definition in the cache with the type as the key
	s.orderedTypes = append(s.orderedTypes, typ)

	return td, nil // return the table definition and no error
}

// Last returns the last registered table definition.
func (s *Schema) Last() *desc.Table {
	if len(s.orderedTypes) == 0 {
		return nil
	}

	return s.structCache[s.orderedTypes[len(s.orderedTypes)-1]]
}

// Get takes a reflect.Type that represents a struct type
// and returns a pointer to a Table that represents the table definition for the database
// or an error if the type is not registered in the schema.
func (s *Schema) Get(typ reflect.Type) (*desc.Table, error) { // NOTE: to make it even faster we could set and then retrieve a Definition variable for each table struct type by interface check.
	typ = desc.IndirectType(typ) // get the underlying type of the struct value.

	td, ok := s.structCache[typ] // get the table definition from the cache
	if !ok {                     // if not found
		return nil, fmt.Errorf("%s was not registered, forgot Schema.Register?", typ.String()) // return an error
	}

	return td, nil // return the table definition and no error
}

// GetByTableName takes a table name as a string
// and returns a pointer to a Table that represents the table definition for the database
// or an error if the table name is not registered in the schema.
func (s *Schema) GetByTableName(tableName string) (*desc.Table, error) {
	for _, td := range s.structCache { // loop over all the table definitions in the cache
		if td.Name == tableName { // if the table name matches
			return td, nil // return the table definition and no error
		}
	}

	return nil, fmt.Errorf("table %s was not registered, forgot Schema.Register?", tableName) // return an error if no match found
}

// Tables returns a slice of pointers to Table that represents all the table definitions in the schema
// sorted by their registered position.
func (s *Schema) Tables(types ...desc.TableType) []*desc.Table {
	// make a slice of pointers to Table with the same capacity as the number of entries in the cache
	list := make([]*desc.Table, 0, len(s.structCache))

	for _, td := range s.structCache { // loop over all the table definitions in the cache
		if !td.IsType(types...) { // if not the table type matches the given types (if any) then skip it.
			continue
		}

		list = append(list, td) // append each table definition to the slice
	}

	sort.Slice(list, func(i, j int) bool { // sort the slice by their registered position
		return list[i].RegisteredPosition < list[j].RegisteredPosition
	})

	return list // return the sorted slice
}

// TableNames returns a slice of strings that represents all the table names in the schema.
func (s *Schema) TableNames(types ...desc.TableType) []string {
	// make a slice of strings with the same capacity as the number of entries in the cache
	list := make([]string, 0, len(s.structCache))

	for _, td := range s.Tables(types...) { // loop over all the table definitions in the schema (sorted by their registered position)
		list = append(list, td.Name) // append each table name to the slice
	}

	return list // return the slice of table names
}

// HasColumnType takes a DataType that represents a data type for the database
// and returns true if any of the tables in the schema has a column with that data type.
func (s *Schema) HasColumnType(dataTypes ...desc.DataType) bool {
	for _, td := range s.Tables() { // loop over all the tables in the schema (sorted by their registered position)
		for _, col := range td.Columns { // loop over all the columns in each table
			for _, dt := range dataTypes {
				if col.Type == dt { // if the column has the same data type as given
					return true // return true
				}
			}
		}
	}

	return false // return false if no match found
}

// HasPassword reports whether the tables in the schema have a column with the password feature enabled.
func (s *Schema) HasPassword() bool {
	for _, td := range s.Tables() {
		for _, col := range td.Columns { // loop over all the columns in each table
			if col.Password {
				return true
			}
		}
	}

	return false
}
