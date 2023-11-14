package desc

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// RowsToStruct takes a schema, a row of data from a database query, and a generic type T
// and returns a slice of values of type T with the fields populated from the row data.
func RowsToStruct[T any](td *Table, rows pgx.Rows) ([]T, error) {
	defer rows.Close() // close the rows after the function returns

	// var valueT T // declare a variable to hold the result
	// get the table definition from the schema by using the type of the result variable
	// td, err := s.Get(reflect.TypeOf(valueT))
	// if err != nil {
	// 	return nil, err // return an error if getting the table definition failed
	// }

	slice := []T{} // create a slice to hold the result values

	for rows.Next() { // loop over each row in the rows
		// convert the row to a value of type T using the table definition
		var value T
		err := ConvertRowsToStruct(td, rows, &value)
		if err != nil {
			return nil, err // return an error if converting the row failed
		}
		slice = append(slice, value) // append the value to the slice
	}

	if err := rows.Err(); err != nil {
		return nil, err // return an error if there was an error in iterating over the rows
	}

	return slice, nil // return the slice and nil error
}

// RowToStruct takes a schema, a single row of data from a database query, and a generic type T
// and returns a value of type T with the fields populated from the row data.
func RowToStruct[T any](td *Table, rows pgx.Rows) (value T, err error) {
	defer rows.Close() // close the rows after the function returns

	// var value T                             // declare a variable to hold the result
	// td, err := s.Get(reflect.TypeOf(value)) // get the table definition from the schema by using the type of the result variable
	// if err != nil {
	// 	return value, err // return an error if getting the table definition failed
	// }

	if !rows.Next() { // check if there is a next row in the rows
		if err = rows.Err(); err != nil {
			return value, err // return an error if there was an error in getting the next row
		}

		return value, pgx.ErrNoRows // return an error if there was no row in the rows
	}

	err = ConvertRowsToStruct(td, rows, &value) // convert the row to a value of type T using the table definition
	if err != nil {
		return value, err // return an error if converting the row failed
	}

	return value, rows.Err() // return the value and any error from closing the rows
}

// ConvertRowsToStruct takes a table definition, a row of data from a database query, and a generic type T
// and returns a value of type T with the fields populated from the row data.
func ConvertRowsToStruct(td *Table, rows pgx.Rows, valuePtr interface{}) error {
	// declare a variable to hold the result
	// var value T
	// get the reflect value of the result variable
	dstElemValue := reflect.ValueOf(valuePtr).Elem()
	// find the scan targets for each column in the row
	scanTargets, err := findScanTargets(dstElemValue, td, rows.FieldDescriptions())
	if err != nil {
		return err // return an error if finding scan targets failed
	}

	for i, t := range scanTargets {
		if t == nil {
			if td.Strict {
				return fmt.Errorf("struct doesn't have corresponding row field: %s (strict check)", rows.FieldDescriptions()[i].Name) // return an error if the struct doesn't have a field for a column
			} else {
				scanTargets[i] = &noOpScanner{}
			}
		}
	}

	if err = rows.Scan(scanTargets...); err != nil {
		// Help developer to find what field was errored:
		var scanArgErr pgx.ScanArgError
		if errors.As(err, &scanArgErr) {
			if len(td.Columns) > scanArgErr.ColumnIndex {
				col := td.Columns[scanArgErr.ColumnIndex]
				// NOTE: this index may be invalid if the struct contains different order of the column in database,
				// the only one option is to use the col's OrdinalPosition (starting from 1, where scanArgErr.ColumnIndex starts from 0)
				// but OrdinalPosition is set only when CheckSchema method was called previously.
				destColumnName := col.Name
				err = fmt.Errorf("%w: field: %s.%s (%s): column: %s.%s",
					err,
					col.Table.StructName, col.FieldName, col.FieldType.String(),
					col.TableName, destColumnName)
			}
		}

		return err // return an error if scanning the row data failed
	}

	return nil // return the result value and nil error
}

// findScanTargets takes a reflect value of a struct, a table definition, and a slice of field descriptions
// and returns a slice of scan targets for each column in the row.
func findScanTargets(dstElemValue reflect.Value, td *Table, fieldDescs []pgconn.FieldDescription) ([]any, error) {
	scanTargets := make([]any, len(fieldDescs)) // create a slice to hold the scan targets

	for i, fieldDesc := range fieldDescs { // loop over each column in the row
		col := td.GetColumnByName(fieldDesc.Name) // get the column definition by name
		if col == nil {
			continue // skip this column if there is no definition for it
		}

		if col.Unscannable {
			continue // skip this column if it is unscannable
		}

		// fmt.Printf("searching for db column: %s over column: %s with field index of: %v\n",
		// fieldDesc.Name, col.Name, col.FieldIndex)

		// If it's a password which contains a custom decryption, use the internal passwordTextScanner driver type.
		if col.Password {
			if td.PasswordHandler.canDecrypt() {
				scanTargets[i] = &passwordTextScanner{
					tableName:            td.Name,
					passwordHandler:      td.PasswordHandler,
					passwordTextFieldPtr: dstElemValue.FieldByIndex(col.FieldIndex),
				}

				continue
			}
		}

		if col.Nullable && (col.Type == UUID ||
			col.Type == Text || col.Type == CharacterVarying) /* Allow receive null on uuid, text and varchar columns even if the field is not a string pointer. */ {
			scanTargets[i] = &nullableScanner{
				fieldPtr: dstElemValue.FieldByIndex(col.FieldIndex),
			}

			continue
		}

		// get the scan target by using the field index and taking the address and interface of the struct field
		scanTargets[i] = dstElemValue.FieldByIndex(col.FieldIndex).Addr().Interface()
	}

	return scanTargets, nil // return the scan targets and nil error
}

type noOpScanner struct{}

func (t *noOpScanner) Scan(src interface{}) error { return nil }

type nullableScanner struct { // useful for UUIDs with null values.
	fieldPtr reflect.Value
}

func (t *nullableScanner) Scan(src interface{}) error {
	if src == nil { // <- IMPORTANT.
		return nil
	}

	t.fieldPtr.Set(reflect.ValueOf(src))

	return nil
}

type passwordTextScanner struct {
	tableName       string
	passwordHandler *PasswordHandler

	passwordTextFieldPtr reflect.Value
}

// Scan completes the sql driver.Scanner interface.
func (t *passwordTextScanner) Scan(src interface{}) error {
	switch v := src.(type) {
	case string:
		plainText, err := t.passwordHandler.Decrypt(t.tableName, v)
		if err != nil {
			return fmt.Errorf("%s: password: %w", t.tableName, err)
		}

		if !t.passwordTextFieldPtr.CanSet() {
			return fmt.Errorf("%s: password: text field is not settable", t.tableName)
		}

		if plainText == "" {
			return nil // if it's empty (this can happen if the Decrypt only verifies the password and not set).
		}

		t.passwordTextFieldPtr.Set(reflect.ValueOf(plainText))
	case []byte:
		return t.Scan(string(v))
	case nil:
	default:
		return fmt.Errorf("%s: password: unknown type of: %T", t.tableName, v)
	}

	return nil
}

/* No need, the current version supports it very well nowadays.
if col.Type.IsArray() {
	scanTargets[i] = &arrayScanner[string]{
		colName:        col.Name,
		arrayFieldPtr:  dstElemValue.FieldByIndex(col.FieldIndex),
	}
	continue
}

func parseArray(src interface{}) ([]string, error) {
	if src == nil { // allow nullable.
		return nil, nil
	}

	switch s := src.(type) {
	case []byte:
		return parseArray(string(s))
	case string:
		if len(s) <= 2 {
			// empty array, return an empty array.
			// *t = make([]int, 0)
			return nil, nil
		}

		// postgres returns a string, e.g. {1,3,5}.
		s = strings.TrimLeft(s, "{")
		s = strings.TrimRight(s, "}")

		return strings.Split(s, ","), nil
	default:
		return nil, fmt.Errorf("invalid type of: %T", src)
	}
}

// T constraints.Ordered
type arrayScanner struct {
	colName string

	arrayFieldPtr  reflect.Value
}

func (t *arrayScanner) Scan(src interface{}) error {
	values, err := parseArray(src)
	if err != nil {
		return fmt.Errorf("array scan: %s: %w", t.colName, err)
	}

	switch t.arrayFieldPtr.Elem().Kind() {
	case reflect.Int:
		arr := make([]any, 0, len(values))

		for _, v := range values {
			valueAsInt, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("array scan: %s: %w", t.colName, err)
			}

			arr = append(arr, valueAsInt)
		}

		t.arrayFieldPtr.Set(reflect.ValueOf(arr))
	case reflect.String:
		t.arrayFieldPtr.Set(reflect.ValueOf(values))
	default:
		return fmt.Errorf("array scan: %s: unsupported type of: %T", t.colName, t.arrayFieldPtr)
	}

	return nil
}
*/
