package desc

import (
	"encoding"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// RowsToStruct takes a row of data from a database query and a generic type T
// and returns a slice of values of type T with the fields populated from the row data.
func (td *Table) RowsToStruct[T any](rows pgx.Rows) ([]T, error) {
	defer rows.Close() // close the rows after the function returns

	// var valueT T // declare a variable to hold the result
	// get the table definition from the schema by using the type of the result variable
	// td, err := s.Get(reflect.TypeOf(valueT))
	// if err != nil {
	// 	return nil, err // return an error if getting the table definition failed
	// }

	slice := []T{} // create a slice to hold the result values

	// Build the column lookup once, outside the row loop, instead of once per row: findScanTargets
	// otherwise resolved every column of every row via a linear, case-insensitive scan over
	// td.Columns (GetColumnByName), an O(len(fieldDescs) * len(td.Columns)) cost per row.
	lookup := buildColumnLookup(td)

	for rows.Next() { // loop over each row in the rows
		// convert the row to a value of type T using the table definition
		var value T
		err := convertRowsToStruct(td, rows, &value, lookup)
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

// RowsToStructWithTotal is RowsToStruct that additionally captures the named int64 window
// column (typically "total_count", as produced by a `COUNT(*) OVER()` window function in the
// SELECT list) from each row instead of routing it to the no-op scanner, so a caller can read
// that total without T carrying an artificial field for it. The pattern this replaces smuggled
// the total through a fake struct field tagged `presenter`.
//
// totalColumn is matched against each row's field descriptions case-insensitively, the same
// rule buildColumnLookup/findScanTargets use to match every other column, so "total_count",
// "Total_Count" and "TOTAL_COUNT" are all equivalent. Every column other than totalColumn
// resolves exactly as RowsToStruct resolves it (same lookup, same scanners, same strict-mode
// behavior for an unmapped column); RowsToStructWithTotal changes only how totalColumn itself is
// routed. `COUNT(*) OVER()` yields the same value on every row of the result set, so the last
// row scanned wins for the returned total; a query that doesn't uphold that guarantee (a
// different value per row) gets an unspecified total back.
//
// Zero rows returns (empty, 0, nil): there is nothing to scan a total out of, following
// RowsToStruct's own zero-row behavior (no error, an empty slice).
func (td *Table) RowsToStructWithTotal[T any](rows pgx.Rows, totalColumn string) ([]T, int64, error) {
	defer rows.Close() // close the rows after the function returns

	slice := []T{} // create a slice to hold the result values

	// Build the column lookup once, outside the row loop, for the same reason RowsToStruct does
	// (see buildColumnLookup's doc).
	lookup := buildColumnLookup(td)
	totalColumnLower := strings.ToLower(totalColumn)

	var total int64
	for rows.Next() { // loop over each row in the rows
		var value T
		rowTotal, err := convertRowsToStructWithTotal(td, rows, &value, lookup, totalColumnLower)
		if err != nil {
			return nil, 0, err // return an error if converting the row failed
		}
		total = rowTotal // last row wins; COUNT(*) OVER() makes every row's value equal.
		slice = append(slice, value)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err // return an error if there was an error in iterating over the rows
	}

	return slice, total, nil
}

// RowToStruct takes a single row of data from a database query and a generic type T
// and returns a value of type T with the fields populated from the row data.
func (td *Table) RowToStruct[T any](rows pgx.Rows) (value T, err error) {
	defer rows.Close() // close the rows after the function returns

	// var value T                             // declare a variable to hold the result
	// td, err := s.Get(reflect.TypeOf(value)) // get the table definition from the schema by using the type of the result variable
	// if err != nil {
	// 	return value, err // return an error if getting the table definition failed
	// }

	if !rows.Next() { // check if there is a next row in the rows
		if err = rows.Err(); err != nil {
			return value, fmt.Errorf("%s: %w", td.GetHumanName(), err) // return an error if there was an error in getting the next row
		}

		return value, fmt.Errorf("%s: %w", td.GetHumanName(), pgx.ErrNoRows) // return an error if there was no row in the rows
	}

	// convert the row to a value of type T using the table definition; build the column lookup
	// once here too (rather than calling the exported ConvertRowsToStruct, which would rebuild
	// it) so both the single-row and multi-row paths share the same resolution code.
	err = convertRowsToStruct(td, rows, &value, buildColumnLookup(td))
	if err != nil {
		return value, err // return an error if converting the row failed
	}

	return value, rows.Err() // return the value and any error from closing the rows
}

// ConvertRowsToStruct takes a table definition, a row of data from a database query, and a generic type T
// and returns a value of type T with the fields populated from the row data.
func ConvertRowsToStruct(td *Table, rows pgx.Rows, valuePtr any) error {
	return convertRowsToStruct(td, rows, valuePtr, buildColumnLookup(td))
}

// buildColumnLookup builds a case-insensitive index of td's columns, keyed by
// strings.ToLower(column name). It lets the per-row scan-target resolution in
// convertRowsToStruct/findScanTargets look up a column by the row's field name in O(1),
// instead of paying GetColumnByName's O(len(td.Columns)) linear, case-insensitive scan for
// every column of every row.
//
// strings.ToLower is safe to use as the case-folding key here (in place of the
// strings.EqualFold comparison GetColumnByName performs) because validateIdentifier
// (desc/struct_table.go) guarantees every column name is a bare ASCII identifier
// ([A-Za-z_][A-Za-z0-9_$]*): for that charset, ToLower and EqualFold always agree, so the map
// lookup below finds exactly the column GetColumnByName would have returned. GetColumnByName
// itself is unchanged and still used by every non-per-row caller.
func buildColumnLookup(td *Table) map[string]*Column {
	lookup := make(map[string]*Column, len(td.Columns))
	for _, col := range td.Columns {
		lookup[strings.ToLower(col.Name)] = col
	}

	return lookup
}

// convertRowsToStruct is the shared implementation behind the exported ConvertRowsToStruct and
// the per-row paths in RowsToStruct/RowToStruct. lookup must be the result of
// buildColumnLookup(td); RowsToStruct builds it once and reuses it across every row instead of
// rebuilding it (and re-paying its cost) per row.
func convertRowsToStruct(td *Table, rows pgx.Rows, valuePtr any, lookup map[string]*Column) error {
	// declare a variable to hold the result
	// var value T
	// get the reflect value of the result variable
	dstElemValue := reflect.ValueOf(valuePtr).Elem()

	// find the scan targets for each column in the row
	scanTargets, err := findScanTargets(dstElemValue, td, lookup, rows.FieldDescriptions())
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

	return scanRow(td, rows, scanTargets)
}

// scanRow calls rows.Scan(scanTargets...) and, on a pgx.ScanArgError, enriches the error with
// the offending struct field/column names before returning it: the same enrichment
// convertRowsToStruct always performed inline, now shared with convertRowsToStructWithTotal so
// both scanning paths report the same diagnostic detail on a scan failure.
func scanRow(td *Table, rows pgx.Rows, scanTargets []any) error {
	if err := rows.Scan(scanTargets...); err != nil {
		// Help developer to find what field was errored:
		if scanArgErr, ok := errors.AsType[pgx.ScanArgError](err); ok {
			// scanArgErr.ColumnIndex is the index of the column in the row data.
			// NOTE: that ^ index may be invalid if the struct contains different order of the column in database,
			// the only one option is to use the col's OrdinalPosition (starting from 1, where scanArgErr.ColumnIndex starts from 0)
			// but OrdinalPosition is set only when CheckSchema method was called previously.
			if fieldDescs := rows.FieldDescriptions(); len(fieldDescs) > scanArgErr.ColumnIndex {
				colName := fieldDescs[scanArgErr.ColumnIndex].Name
				col := td.GetColumnByName(colName)
				if col != nil {
					destColumnName := col.Name
					err = fmt.Errorf("%w: field: %s.%s (type: %s): column: %s.%s",
						err,
						col.Table.StructName, col.FieldName, col.FieldType.String(),
						col.TableName, destColumnName)
				}
			}
		}

		return err // return an error if scanning the row data failed
	}

	return nil // return nil error on a successful scan
}

// convertRowsToStructWithTotal is convertRowsToStruct plus out-of-band capture of the
// totalColumnLower field (already lower-cased by the caller, RowsToStructWithTotal) into the
// returned int64. It shares findScanTargets/lookup/scanRow with convertRowsToStruct, but
// intercepts totalColumn's scan target itself, routing it to a totalScanner instead of the
// nil-scan-target/strict-mode handling convertRowsToStruct applies, so the total column is
// captured even when td.Strict would otherwise reject a column with no matching struct field.
func convertRowsToStructWithTotal(td *Table, rows pgx.Rows, valuePtr any, lookup map[string]*Column, totalColumnLower string) (int64, error) {
	dstElemValue := reflect.ValueOf(valuePtr).Elem()

	fieldDescs := rows.FieldDescriptions()
	scanTargets, err := findScanTargets(dstElemValue, td, lookup, fieldDescs)
	if err != nil {
		return 0, err
	}

	var total int64
	for i, fieldDesc := range fieldDescs {
		if strings.ToLower(fieldDesc.Name) == totalColumnLower {
			scanTargets[i] = &totalScanner{dst: &total}
			continue
		}

		if scanTargets[i] == nil {
			if td.Strict {
				return 0, fmt.Errorf("struct doesn't have corresponding row field: %s (strict check)", fieldDesc.Name)
			}
			scanTargets[i] = &noOpScanner{}
		}
	}

	if err = scanRow(td, rows, scanTargets); err != nil {
		return 0, err
	}

	return total, nil
}

// findScanTargets takes a reflect value of a struct, a table definition, a column lookup built by
// buildColumnLookup(td), and a slice of field descriptions, and returns a slice of scan targets
// for each column in the row.
func findScanTargets(dstElemValue reflect.Value, td *Table, lookup map[string]*Column, fieldDescs []pgconn.FieldDescription) ([]any, error) {
	scanTargets := make([]any, len(fieldDescs)) // create a slice to hold the scan targets

	for i, fieldDesc := range fieldDescs { // loop over each column in the row
		col := lookup[strings.ToLower(fieldDesc.Name)] // O(1) case-insensitive lookup, see buildColumnLookup.
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

		if !col.isPtr /* Edward report it, AI had a solution but this is a bit faster as the driver already handles pointers and nullables */ &&
			col.Nullable &&
			(col.Type == UUID || col.Type == Text || col.Type == CharacterVarying) { /* Allow receive null on uuid, text and varchar columns even if the field is not a string pointer. */
			scanTargets[i] = &nullableScanner{
				colName:  col.Name,
				fieldPtr: dstElemValue.FieldByIndex(col.FieldIndex),
			}

			continue
		}

		if !col.isPtr && col.Nullable && (col.Type == JSONB || col.Type == JSON) && !col.isScanner {
			// It's a JSONB/JSON column, can be null, it does not already implement a custom Scan method,
			// then wrap it so it can handle null and json values automatically.
			scanTargets[i] = &jsonScanner{
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

func (t *noOpScanner) Scan(src any) error { return nil }

// totalScanner scans a single window-function total (e.g. a `COUNT(*) OVER()` column) into dst,
// backing RowsToStructWithTotal's totalColumn capture. PostgreSQL's count() returns bigint
// (int64), which pgx hands back as int64 for a COUNT(*) OVER() column; int/int32 are accepted
// too in case a caller casts the window column to a narrower integer type. A nil src (unexpected
// for a COUNT column, which is never NULL) is treated as zero rather than erroring.
type totalScanner struct {
	dst *int64
}

// Scan completes the sql driver.Scanner interface.
func (t *totalScanner) Scan(src any) error {
	if src == nil {
		*t.dst = 0
		return nil
	}

	switch v := src.(type) {
	case int64:
		*t.dst = v
	case int32:
		*t.dst = int64(v)
	case int:
		*t.dst = int64(v)
	default:
		return fmt.Errorf("scan: total column: cannot scan value of type %T into int64", src)
	}

	return nil
}

type nullableScanner struct { // useful for UUIDs with null values.
	colName  string
	fieldPtr reflect.Value
}

// Scan completes the sql driver.Scanner interface.
//
// The driver value is assigned directly when its type allows it, converted when it is
// convertible, and otherwise handed to scanText, which covers a text-form value destined for a
// field that parses itself (see scanText).
//
// It never panics: if the field can't be set, or the value fits none of those three paths (e.g.
// the driver unexpectedly returns something other than a string/[]byte for a text/uuid/varchar
// column), it returns a descriptive error instead of letting reflect.Value.Set panic mid-scan.
func (t *nullableScanner) Scan(src any) error {
	if src == nil { // <- IMPORTANT.
		return nil
	}

	if !t.fieldPtr.CanSet() {
		return fmt.Errorf("scan: column %s: field is not settable", t.colName)
	}

	srcValue := reflect.ValueOf(src)
	srcType := srcValue.Type()
	fieldType := t.fieldPtr.Type()

	switch {
	case srcType.AssignableTo(fieldType):
		t.fieldPtr.Set(srcValue)
	case srcType.ConvertibleTo(fieldType):
		t.fieldPtr.Set(srcValue.Convert(fieldType))
	default:
		return t.scanText(src)
	}

	return nil
}

// scanText handles a text-form driver value whose target parses itself, which is what a uuid
// column needs: pgtype.UUIDCodec hands a sql.Scanner the *canonical string form*, and a Go
// string is neither assignable nor convertible to a [16]byte-based type such as uuid.UUID.
// Any field whose pointer implements encoding.TextUnmarshaler is served here.
func (t *nullableScanner) scanText(src any) error {
	fieldType := t.fieldPtr.Type()

	var text []byte
	switch v := src.(type) {
	case string:
		text = []byte(v)
	case []byte:
		text = v
	default:
		return fmt.Errorf("scan: column %s: cannot scan value of type %T into field of type %s", t.colName, src, fieldType)
	}

	if !t.fieldPtr.CanAddr() {
		return fmt.Errorf("scan: column %s: cannot scan value of type %T into field of type %s: field is not addressable", t.colName, src, fieldType)
	}

	unmarshaler, ok := t.fieldPtr.Addr().Interface().(encoding.TextUnmarshaler)
	if !ok {
		return fmt.Errorf("scan: column %s: cannot scan value of type %T into field of type %s", t.colName, src, fieldType)
	}

	if err := unmarshaler.UnmarshalText(text); err != nil {
		return fmt.Errorf("scan: column %s: into field of type %s: %w", t.colName, fieldType, err)
	}

	return nil
}

type passwordTextScanner struct {
	tableName       string
	passwordHandler *PasswordHandler

	passwordTextFieldPtr reflect.Value
}

// Scan completes the sql driver.Scanner interface.
func (t *passwordTextScanner) Scan(src any) error {
	switch v := src.(type) {
	case string:
		plainText, err := t.passwordHandler.Decrypt(t.tableName, v)
		if err != nil {
			return fmt.Errorf("%s: password: %w", t.tableName, err)
		}

		if !t.passwordTextFieldPtr.CanSet() {
			return fmt.Errorf("%s: password: text field is not settable", t.tableName)
		}

		if t.passwordTextFieldPtr.Kind() != reflect.String {
			return fmt.Errorf("%s: password: text field is not a string (kind: %s)", t.tableName, t.passwordTextFieldPtr.Kind())
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

// jsonScanner is a custom scanner for JSONB/JSON types in PostgreSQL.
type jsonScanner struct {
	fieldPtr reflect.Value
}

// Scan completes the sql driver.Scanner interface.
func (t *jsonScanner) Scan(src any) error {
	if src == nil {
		return nil
	}

	var data []byte
	switch v := src.(type) {
	case []byte: // old pg
		data = v
	case string: // new pg
		data = []byte(v)
	default:
		return fmt.Errorf("scan: invalid type of: %T", src)
	}

	// Determine the target for unmarshaling.
	target := t.fieldPtr.Interface()
	if t.fieldPtr.Kind() != reflect.Pointer && t.fieldPtr.CanAddr() {
		target = t.fieldPtr.Addr().Interface()
	}

	// MatchCaseInsensitiveNames is required, not cosmetic: this scanner decodes
	// `to_jsonb(x.*)`/`row_to_json(...)` projections, whose keys are PostgreSQL's lower-cased
	// column names, into Go structs that usually carry no json tags at all. encoding/json/v2
	// matches names exactly by default, so without this option a `Name` field would silently
	// stay at its zero value for a "name" key. encoding/json v1 matched case-insensitively.
	return json.Unmarshal(data, target, json.MatchCaseInsensitiveNames(true))
}

// Value completes the sql driver.Valuer interface.
// func (t jsonScanner) Value() (driver.Value, error) {
// 	return json.Marshal(t.fieldPtr.Interface())
// }

/* No need, the current version supports it very well nowadays.
if col.Type.IsArray() {
	scanTargets[i] = &arrayScanner[string]{
		colName:        col.Name,
		arrayFieldPtr:  dstElemValue.FieldByIndex(col.FieldIndex),
	}
	continue
}

func parseArray(src any) ([]string, error) {
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

func (t *arrayScanner) Scan(src any) error {
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
