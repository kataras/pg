package desc

import (
	"database/sql"
	"reflect"
	"strings"
)

var scannerInterface = reflect.TypeOf((*sql.Scanner)(nil)).Elem()

func implementsScanner(typ reflect.Type) bool {
	return typ.Implements(scannerInterface) || reflect.PointerTo(typ).Implements(scannerInterface)
}

// reflect.

// IndirectType returns the value of a pointer-type "typ".
// If "typ" is a pointer, array, chan, map or slice it returns its Elem,
// otherwise returns the "typ" as it is.
func IndirectType(typ reflect.Type) reflect.Type {
	switch typ.Kind() {
	case reflect.Ptr, reflect.Array, reflect.Chan, reflect.Map, reflect.Slice:
		return typ.Elem()
	}
	return typ
}

// IndirectValue returns the element type (e.g. if pointer of *User it will return the User type).
func IndirectValue(v any) reflect.Value {
	return reflect.Indirect(reflect.ValueOf(v))
}

// lookupFields takes a reflect.Type that represents a struct and a parent index slice
// and returns a slice of reflect.StructField that represents the exported fields of the struct
// that have a non-empty and non-dash value for the ‘pg’ tag.
func lookupFields(typ reflect.Type, parentIndex []int) (fields []reflect.StructField) {
	// loop over all the exported fields of the struct (flattening any nested structs)
	for _, field := range lookupStructFields(typ, parentIndex) {
		// get the value of the tag with the default name and check if it is empty or dash
		if v := field.Tag.Get(DefaultTag); v == "" || v == "-" {
			// Skip fields that don’t contain the ‘pg’ tag or has ‘-’.
			// We do it here so we can have a calculated number of fields for columns.
			continue // skip this field
		}

		fields = append(fields, field) // append the field to the result slice
	}

	return // return the result slice
}

// isSpecialJSONStructure checks if a struct field has a tag that indicates a JSON or JSONB type.
func isSpecialJSONStructure(field reflect.StructField) bool {
	tag := strings.ToLower(field.Tag.Get(DefaultTag)) // get the lower case value of the tag with the default name
	return strings.Contains(tag, "type=json")         // return true if the tag contains "type=json" (this includes "type=jsonb" too)
}

// lookupStructFields takes a reflect.Type that represents a struct and a parent index slice
// and returns a slice of reflect.StructField that represents the exported fields of the struct.
func lookupStructFields(typ reflect.Type, parentIndex []int) (fields []reflect.StructField) {
	for i := 0; i < typ.NumField(); i++ { // loop over all the fields of the struct
		field := typ.Field(i)    // get the i-th field
		if field.PkgPath != "" { // skip unexported fields (they have a non-empty package path)
			continue
		}

		fieldType := IndirectType(field.Type) // get the underlying type of the field

		if fieldType.Kind() == reflect.Struct { // if the field is a struct itself and it's not time, flatten it
			if fieldType != timeType && !isSpecialJSONStructure(field) /* do not flatten the struct's fields when jsonb struct field, let it behave as it is. */ {
				// on struct field: include all fields with an exception if the struct field itself is tagged for skipping explicitly "-"
				if field.Tag.Get(DefaultTag) == "-" {
					continue
				}

				if c, _ := convertStructFieldToColumnDefinion("", field); c != nil {
					if c.Presenter {
						continue
					}
				}

				// recursively look up the fields of the nested struct and append the current index to the parent index
				structFields := lookupFields(fieldType, append(parentIndex, i))

				// as an exception, when this struct field is marked as a postgres column
				// but this field's struct type does not contain any `pg` tags
				// then treat that struct field itself as a postgres column,
				// e.g. a custom time.Time implementation.
				if len(structFields) > 0 { // if there are any nested fields found
					fields = append(fields, structFields...) // append them to the result slice
					continue
				}
			}
		}

		index := []int{i}         // create a slice with the current index
		if len(parentIndex) > 0 { // if there is a parent index
			index = append(parentIndex, i) // append the current index to it
		}

		tmp := make([]int, len(index)) // make a copy of the index slice
		copy(tmp, index)
		field.Index = tmp // assign it to the field's index

		fields = append(fields, field) // append the field to the result slice
	}

	return // return the result slice
}
