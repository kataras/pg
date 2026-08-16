package pg

import (
	"context"
	"reflect"

	"github.com/kataras/pg/desc"
)

// tableForScan resolves the *desc.Table QueryStructs/QueryStruct scan T through: db's Schema is
// consulted first (via the reflect.Type lookup Schema.Get performs, not GetByTableName: T is
// never known by a table name here), so a T that was registered with Schema.Register keeps using
// its full, registration-time descriptor (including any password handling, unique
// indexes/constraints information, and explicit "type=" overrides that descriptor carries).
// Only when T is not registered does it fall back to desc.LooseTable, which builds a descriptor
// purely from T's field tags/names via reflection, with no Schema involvement at all.
func tableForScan[T any](db *DB) (*desc.Table, error) {
	var value T
	typ := reflect.TypeOf(value)

	if td, err := db.schema.Get(typ); err == nil {
		return td, nil
	}

	return desc.LooseTable(typ)
}

// QueryStructs executes query (with args bound positionally as $1, $2, ...) and scans every
// returned row into a T, matched by column name. T does NOT need to be registered in db's
// Schema first, unlike Repository[T]/NewRepository, which panics for an unregistered type.
//
// db's Schema is consulted first: if T was registered (Schema.Register/MustRegister), its full
// registered descriptor is used, exactly as Repository[T].Select would use it. Otherwise, a
// schema-independent descriptor is built by desc.LooseTable purely from T's field tags/names via
// reflection (see its doc for the exact column-name and JSON-wrap rules) and cached per type, so
// repeated calls pay the reflection cost only once per T.
//
// Every field whose (deref'd) type is a struct, map or slice (excluding time.Time, []byte and
// any sql.Scanner-implementing type) decodes automatically from a JSON/JSONB column or a
// `to_jsonb(...)`/`row_to_json(...)` projection via encoding/json; no manual json.Unmarshal call
// is needed. A result column with no matching T field is ignored rather than causing an error
// (LooseTable's descriptor is always non-strict), so a join or presenter query can freely select
// extra columns T doesn't care about.
//
// When T is unregistered (the desc.LooseTable path), every column is treated as nullable (there
// is no schema to consult for a field's real nullability), so a plain (non-pointer) text/UUID/
// varchar or JSON-wrapped field tolerates an unexpected SQL NULL instead of erroring; see
// desc.LooseTable's doc for the full rationale.
//
// A query yielding no rows returns an empty, non-nil slice and a nil error (the same convention
// Repository[T].Select follows).
//
// Example:
//
//	type OrderWithCustomer struct {
//		ID       int64
//		Total    float64
//		Customer *Customer // populated from a `to_jsonb(c.*) AS customer` projection.
//	}
//
//	rows, err := pg.QueryStructs[OrderWithCustomer](ctx, db, `
//		SELECT o.id, o.total, to_jsonb(c.*) AS customer
//		FROM orders o JOIN customers c ON c.id = o.customer_id`)
func QueryStructs[T any](ctx context.Context, db *DB, query string, args ...any) ([]T, error) {
	td, err := tableForScan[T](db)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	return desc.RowsToStruct[T](td, rows)
}

// QueryStruct is QueryStructs for exactly one row: it executes query, scans the first returned
// row into a T using the same column-matching (and, when T is unregistered, the same
// desc.LooseTable) rules QueryStructs documents, and reports ErrNoRows (check with
// errors.Is/IsErrNoRows) when the query yields no rows at all.
//
// Example:
//
//	item, err := pg.QueryStruct[OrderWithCustomer](ctx, db, `
//		SELECT o.id, o.total, to_jsonb(c.*) AS customer
//		FROM orders o JOIN customers c ON c.id = o.customer_id
//		WHERE o.id = $1`, orderID)
func QueryStruct[T any](ctx context.Context, db *DB, query string, args ...any) (T, error) {
	var zero T

	td, err := tableForScan[T](db)
	if err != nil {
		return zero, err
	}

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return zero, err
	}

	return desc.RowToStruct[T](td, rows)
}

// ScanStructs scans every remaining row of rows (as produced by DB.Query, Repository[T].Query, a
// transaction's Query, etc.) into a T by column name and closes rows before returning, using the
// same JSON-wrap and non-strict extra-column rules QueryStructs documents.
//
// Unlike QueryStructs/QueryStruct, ScanStructs has no *DB to consult, so it cannot check whether
// T is registered in a Schema: it always builds T's descriptor via desc.LooseTable, even for a
// type that happens to be registered elsewhere. Prefer QueryStructs/QueryStruct (or
// Repository[T].Select) when a *DB is available and T is registered, so its full descriptor
// (e.g. password handling) is used instead.
//
// Example:
//
//	rows, err := db.Query(ctx, "SELECT id, name FROM users WHERE active")
//	if err != nil {
//		return err
//	}
//	users, err := pg.ScanStructs[User](rows)
func ScanStructs[T any](rows Rows) ([]T, error) {
	var value T

	td, err := desc.LooseTable(reflect.TypeOf(value))
	if err != nil {
		rows.Close()
		return nil, err
	}

	return desc.RowsToStruct[T](td, rows)
}
