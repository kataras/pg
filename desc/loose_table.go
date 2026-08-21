package desc

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// looseTableCache caches the *Table built for each struct type LooseTable is called with, keyed
// by the dereferenced (non-pointer) reflect.Type. It is populated lazily and is safe for
// concurrent use; sync.Map.LoadOrStore guarantees that if two goroutines race to build a
// descriptor for the same type, both observe the same, single *Table instance afterwards.
var looseTableCache sync.Map // map[reflect.Type]*Table

// LooseTable returns a cached, schema-independent *Table descriptor for typ (a struct type, or a
// pointer to one; it is dereferenced automatically), suitable for scanning query results into
// ad-hoc struct types that were never passed to Schema.Register: join results, read models, or
// any other SELECT whose shape doesn't correspond to a single registered table.
//
// Unlike ConvertStructToTable (the descriptor built by Schema.Register, and the one every
// Repository[T] uses), LooseTable does not require a `pg` struct tag on every field: lookupFields
// (desc/reflect.go), which ConvertStructToTable relies on, silently skips any field without one,
// which is exactly why it cannot serve an untagged ad-hoc struct. LooseTable instead considers
// every exported field and resolves its column name, in order:
//
//  1. The `pg` tag's "name=" option, e.g. `pg:"name=food_id"` -> "food_id".
//  2. The `json` tag's name, e.g. `json:"foodId,omitempty"` -> "foodId"; a bare `json:"-"` skips
//     the field entirely (a `json:"-,"` tag, per encoding/json's own convention, is instead read
//     as the literal column name "-", not a skip).
//  3. SnakeCase(field name) (see desc/naming.go), e.g. FoodID -> "food_id".
//
// A field tagged `pg:"-"` is skipped outright (checked before the json tag, so a field can be
// hidden from LooseTable independently of its JSON marshaling). Unexported fields are always
// skipped.
//
// Embedded (anonymous) struct fields are NOT promoted/flattened the way ConvertStructToTable
// flattens a tagged nested struct: an anonymous field becomes exactly one column of its own
// (named per the rules above and, being struct-kinded, normally JSON-wrapped, see below)
// rather than having its own fields hoisted up as top-level columns. ConvertStructToTable's
// flattening (lookupStructFields, desc/reflect.go) decides whether to flatten a nested struct by
// recursively checking whether that struct's own fields carry `pg` tags; reusing it here would
// silently produce nothing for an untagged ad-hoc struct (a zero-field lookupFields result), so
// LooseTable does not attempt it. A type that needs promoted embedded fields must be registered
// with Schema.Register instead, which supports it natively.
//
// JSON auto-unmarshal: a field whose type, after removing at most one pointer indirection, has
// kind struct, map or slice: excluding time.Time, []byte (and *[]byte), any type where
// field.Type itself or a pointer to it implements sql.Scanner (the same check struct_table.go
// uses for every registered column, and the same interface pgx's own JSON codec special-cases
// ahead of its generic decode), and any type defined in github.com/jackc/pgx/v5/pgtype (e.g.
// pgtype.Range[T], pgtype.Array[T]: ordinary structs that do NOT implement sql.Scanner, but that
// pgx's own driver already scans directly from an int4range/daterange/array/... column. Marking
// one of them JSONB would hand pgx's native text representation, e.g. "[1,10)" or "{1,2,3}", to
// jsonScanner's json.Unmarshal and fail with a JSON syntax error at scan time), is marked as a
// JSONB column. For a non-pointer field this makes
// the scan path (findScanTargets, desc/scanner.go) route the column through the package's
// existing jsonScanner, which both decodes the JSON payload with encoding/json and tolerates a
// SQL NULL (jsonScanner.Scan no-ops on a nil src). For a pointer field (e.g. `Parent
// *ParentModel`), isPtr is left true so that branch is not entered; instead the scan path falls
// through to its default `Addr().Interface()` target, i.e. **ParentModel, which pgx's own
// pgtype.JSONCodec already decodes generically for any Go pointer-to-pointer destination
// (allocating the pointee on a non-null value, leaving it nil on SQL NULL), so both pointer and
// non-pointer struct/map/slice fields decode automatically, via two different (but both
// pre-existing, unmodified) code paths. Either way, `to_jsonb(x.*) AS field` and a genuine
// jsonb/json column both work with no extra configuration.
//
// Every column is marked Nullable so that a plain (non-pointer) text-like field (Type Text, UUID
// or CharacterVarying) also benefits from the package's existing nullableScanner grace path
// instead of erroring on an unexpected SQL NULL. LooseTable has no schema to consult for a
// field's real nullability, so it conservatively assumes every column might be NULL.
//
// The returned *Table has a zero-value Strict field (false), so scanning is intentionally
// non-strict: any result column with no matching field is bound to the existing no-op scanner
// (see convertRowsToStruct, desc/scanner.go) instead of erroring, exactly as it already does for
// any non-strict registered table. Its Type is TableTypePresenter, the existing TableType
// documented for "decod[ing] custom select queries", precisely LooseTable's purpose, so no new
// TableType or Table field was needed.
//
// The result is cached per (dereferenced) reflect.Type, so repeated calls for the same type are
// O(1) after the first and always return the same *Table instance.
//
// LooseTable returns an error if typ (after dereferencing any pointer) is not a struct.
//
// Registered types are unaffected: LooseTable has no knowledge of, and never consults, any
// Schema: callers that already have a *Table from Schema.Register (or Schema.Get) should keep
// using it directly; QueryStructs/QueryStruct (the root pg package) only fall back to
// LooseTable when the type is not registered.
func LooseTable(typ reflect.Type) (*Table, error) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("desc: LooseTable: expected a struct type but got: %s", typ.Kind())
	}

	if cached, ok := looseTableCache.Load(typ); ok {
		return cached.(*Table), nil
	}

	table := buildLooseTable(typ)

	actual, _ := looseTableCache.LoadOrStore(typ, table)
	return actual.(*Table), nil
}

// buildLooseTable does the actual reflection work behind LooseTable; it is split out only so
// LooseTable itself stays focused on validation and caching.
func buildLooseTable(typ reflect.Type) *Table {
	table := &Table{
		Type:       TableTypePresenter,
		StructName: typ.Name(),
		StructType: typ,
		SearchPath: DefaultSearchPath,
		Name:       SnakeCase(typ.Name()),
	}

	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue // unexported field.
		}

		name, skip := looseColumnName(field)
		if skip {
			continue
		}

		col := &Column{
			Table:      table,
			TableName:  table.Name,
			Name:       name,
			FieldIndex: []int{i},
			FieldType:  field.Type,
			FieldName:  field.Name,
			isPtr:      field.Type.Kind() == reflect.Pointer,
			isScanner:  implementsScanner(field.Type),
			Nullable:   true, // no schema to consult for real nullability; assume every column can be NULL.
		}

		if !col.isScanner && isLooseJSONField(field.Type) {
			col.Type = JSONB
		} else {
			col.Type = goTypeToDataType(field.Type)
		}

		table.Columns = append(table.Columns, col)
	}

	return table
}

// looseColumnName resolves the column name LooseTable uses for field, following the name
// precedence documented on LooseTable itself (pg tag "name=" option, then json tag name, then
// SnakeCase(field name)). skip is true when the field must be omitted entirely (a `pg:"-"` tag,
// or a bare `json:"-"` tag).
func looseColumnName(field reflect.StructField) (name string, skip bool) {
	pgTag := field.Tag.Get(DefaultTag)
	if pgTag == "-" {
		return "", true
	}

	if n := loosePgTagName(pgTag); n != "" {
		return n, false
	}

	if jsonTag, ok := field.Tag.Lookup("json"); ok {
		if jsonTag == "-" {
			return "", true // bare "-" (no trailing comma) means "omit this field".
		}

		jsonName, _, _ := strings.Cut(jsonTag, ",")
		if jsonName != "" {
			return jsonName, false // covers the "-," (literal "-") edge case too, by design.
		}
	}

	return SnakeCase(field.Name), false
}

// loosePgTagName extracts the explicit "name=" option out of a `pg` struct tag, e.g.
// "name=food_id,type=jsonb" -> "food_id". It returns "" if the tag is empty or carries no "name="
// option. Every other option in the tag (including a bare, comma-less tag such as `pg:"id"`,
// which struct_table.go's convertStructFieldToColumnDefinion treats as a naming shorthand) is
// intentionally ignored here: LooseTable only honors the unambiguous "name=" form, so a stray
// bare tag (e.g. a `pg:"unique"` left over from copy-pasting a registered struct) is never
// misread as a column name; the field simply falls through to its json tag / SnakeCase(field
// name) instead, exactly as if it had no `pg` tag at all.
func loosePgTagName(tag string) string {
	if tag == "" {
		return ""
	}

	for opt := range strings.SplitSeq(tag, ",") {
		key, value, found := strings.Cut(opt, "=")
		if found && key == "name" {
			return value
		}
	}

	return ""
}

// pgxPgtypePackagePath is the import path of github.com/jackc/pgx/v5/pgtype, the package behind
// pgx's own driver-native wrapper types (pgtype.Range[T], pgtype.Array[T], pgtype.Bits, ...).
// isLooseJSONField excludes every type defined in this package from JSON-wrap marking: none of
// them need to implement sql.Scanner to be scannable, pgx's own driver already knows how to
// scan directly into them (that's their entire purpose), so the sql.Scanner check alone doesn't
// catch them, and without this exclusion a field like `Ages pgtype.Range[int32]` would be
// wrongly marked JSONB and handed pgx's native text representation (e.g. "[1,10)") to
// jsonScanner's json.Unmarshal, failing with a JSON syntax error at scan time. reflect.Type's
// PkgPath is the natural discriminator: every exported type pgtype defines, however many type
// parameters it takes, reports this exact package path, so no per-type allowlist is needed.
const pgxPgtypePackagePath = "github.com/jackc/pgx/v5/pgtype"

// isLooseJSONField reports whether a field of the given type should be scanned as a JSON/JSONB
// payload by LooseTable: its type, after removing at most one pointer indirection, has kind
// struct (excluding time.Time and any github.com/jackc/pgx/v5/pgtype type), map, or slice
// (excluding []byte): see LooseTable's doc for the full rationale and how the pointer/non-pointer
// cases each end up decoding automatically.
func isLooseJSONField(fieldType reflect.Type) bool {
	t := fieldType
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if t.PkgPath() == pgxPgtypePackagePath {
		return false // pgx's own driver already scans these directly; never wrap them in JSON.
	}

	switch t.Kind() {
	case reflect.Struct:
		return t != timeType
	case reflect.Map:
		return true
	case reflect.Slice:
		return t.Elem().Kind() != reflect.Uint8 // exclude []byte/*[]byte (bytea), not JSON.
	default:
		return false
	}
}
