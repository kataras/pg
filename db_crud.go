package pg

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/kataras/pg/desc"
)

// This file adds a table-name based CRUD API on *DB: DeleteByID, DeleteBy, ExistsBy, CountBy
// and SelectSingle. Unlike the struct-based methods in db_repository.go (Delete, Exists, ...),
// every method here takes the target table by its registered name (a string) instead of a Go
// value, which is convenient for generic, table-agnostic code but also reintroduces the risk
// that made a similar API in an earlier version of this library unsafe: a table or column name
// coming straight from a caller-controlled string could be concatenated into SQL.
//
// Every method below closes that hole the same way: the table name is resolved through
// db.schema.GetByTableName (an unknown table name returns its descriptive error before any SQL
// is built) and every column name is resolved through the returned *desc.Table (an unknown
// column likewise returns a descriptive error). Only names that exist in the registered Schema
// ever reach a query, and even then they are quoted with QuoteIdentifier; these methods are
// safe against injection from their tableName/colValPairs arguments in a way their removed
// ancestors were not.

// parseColValPairs validates a flat "col1", v1, "col2", v2, ... slice of column/value pairs, as
// accepted by DeleteBy, ExistsBy and CountBy, and splits it into a slice of column names and a
// parallel slice of values in the same order.
//
// It never touches the database or the Schema, so both of its error cases (an odd number of
// arguments, or a pair whose key is not a string) are reachable from a plain unit test as well
// as from a live one. Column-name validity (does this column exist on the target table) is
// checked separately, by whereClauseFromPairs, once the table's descriptor is known.
func parseColValPairs(colValPairs []any) (cols []string, vals []any, err error) {
	if len(colValPairs)%2 != 0 {
		return nil, nil, fmt.Errorf(`pg: colValPairs: expected an even number of arguments ("col", value, ...), got %d`, len(colValPairs))
	}

	if len(colValPairs) == 0 {
		return nil, nil, nil
	}

	cols = make([]string, 0, len(colValPairs)/2)
	vals = make([]any, 0, len(colValPairs)/2)

	for i := 0; i < len(colValPairs); i += 2 {
		col, ok := colValPairs[i].(string)
		if !ok {
			return nil, nil, fmt.Errorf("pg: colValPairs: argument at index %d must be a string column name, got %T", i, colValPairs[i])
		}

		cols = append(cols, col)
		vals = append(vals, colValPairs[i+1])
	}

	return cols, vals, nil
}

// whereClauseFromPairs resolves cols against td (an unknown column name returns a descriptive
// error) and returns a `WHERE "c1" = $1 AND "c2" = $2` clause (or "" when cols is empty)
// prefixed with a leading space, together with args in the matching $N order. The returned
// clause is meant to be appended directly after a table reference in a query that has no
// placeholders of its own before it, so its parameters start at $1.
func whereClauseFromPairs(td *desc.Table, cols []string, vals []any) (clause string, args []any, err error) {
	if len(cols) == 0 {
		return "", nil, nil
	}

	var b strings.Builder
	b.WriteString(" WHERE ")

	for i, colName := range cols {
		col := td.GetColumnByName(colName)
		if col == nil {
			return "", nil, fmt.Errorf("pg: unknown column %q on table %q", colName, td.Name)
		}

		if i > 0 {
			b.WriteString(" AND ")
		}

		fmt.Fprintf(&b, "%s = $%d", QuoteIdentifier(col.Name), i+1)
	}

	return b.String(), vals, nil
}

// resolveWhere combines parseColValPairs and whereClauseFromPairs: it validates colValPairs'
// shape, resolves every column name against td, and returns the resulting WHERE clause (see
// whereClauseFromPairs) and its arguments.
func resolveWhere(td *desc.Table, colValPairs []any) (clause string, args []any, err error) {
	cols, vals, err := parseColValPairs(colValPairs)
	if err != nil {
		return "", nil, err
	}

	return whereClauseFromPairs(td, cols, vals)
}

// DeleteByID deletes one row of the registered table by its primary key and reports
// whether a row was removed; the primary-key column comes from the table's descriptor.
//
// tableName is resolved through the Schema (Schema.GetByTableName): an unknown table name
// returns its descriptive error instead of reaching SQL. If the table is read-only (a view,
// materialized view or presenter: see desc.TableType.IsReadOnly), it returns ErrIsReadOnly,
// matching Repository.Delete's behavior for read-only tables.
func (db *DB) DeleteByID(ctx context.Context, tableName string, id any) (bool, error) {
	td, err := db.schema.GetByTableName(tableName)
	if err != nil {
		return false, err
	}

	if td.IsReadOnly() {
		return false, ErrIsReadOnly
	}

	return db.deleteByID(ctx, td, id)
}

// DeleteBy deletes rows matching the given column/value pairs ("col1", v1, "col2", v2, ...)
// ANDed together, returning the number of rows removed; the table and every column must
// exist in the registered schema. Given zero pairs it deletes every row of the table. Callers
// that mean to delete everything should pass none, and callers that don't should always pass
// at least one pair.
//
// tableName is resolved through the Schema (Schema.GetByTableName) and every column name in
// colValPairs is resolved through the table's descriptor (desc.Table.GetColumnByName); an
// unknown table or an unknown column returns a descriptive error instead of reaching SQL, and
// resolved names are quoted with QuoteIdentifier before being embedded in the DELETE statement.
// colValPairs must have an even length with string keys, or it returns a descriptive error (see
// parseColValPairs).
//
// If the table is read-only (a view, materialized view or presenter: see
// desc.TableType.IsReadOnly), it returns ErrIsReadOnly, matching Repository.Delete's behavior
// for read-only tables.
func (db *DB) DeleteBy(ctx context.Context, tableName string, colValPairs ...any) (int64, error) {
	td, err := db.schema.GetByTableName(tableName)
	if err != nil {
		return 0, err
	}

	if td.IsReadOnly() {
		return 0, ErrIsReadOnly
	}

	where, args, err := resolveWhere(td, colValPairs)
	if err != nil {
		return 0, err
	}

	query := fmt.Sprintf(`DELETE FROM %s.%s%s;`,
		QuoteIdentifier(db.searchPath), QuoteIdentifier(td.Name), where)

	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}

	return tag.RowsAffected(), nil
}

// ExistsBy reports whether any row of the registered table matches the given
// column/value pairs (no pairs: whether the table has any row).
//
// tableName is resolved through the Schema (Schema.GetByTableName) and every column name in
// colValPairs is resolved through the table's descriptor; an unknown table or an unknown column
// returns a descriptive error instead of reaching SQL, and resolved names are quoted with
// QuoteIdentifier before being embedded in the generated
// `SELECT EXISTS (SELECT 1 FROM ... [WHERE ...])` query, executed through DB.QueryBoolean.
// colValPairs must have an even length with string keys, or it returns a descriptive error (see
// parseColValPairs).
func (db *DB) ExistsBy(ctx context.Context, tableName string, colValPairs ...any) (bool, error) {
	td, err := db.schema.GetByTableName(tableName)
	if err != nil {
		return false, err
	}

	where, args, err := resolveWhere(td, colValPairs)
	if err != nil {
		return false, err
	}

	query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.%s%s);`,
		QuoteIdentifier(db.searchPath), QuoteIdentifier(td.Name), where)

	return db.QueryBoolean(ctx, query, args...)
}

// CountBy returns the number of rows of the registered table matching the given
// column/value pairs; no pairs count the whole table.
//
// tableName is resolved through the Schema (Schema.GetByTableName) and every column name in
// colValPairs is resolved through the table's descriptor; an unknown table or an unknown column
// returns a descriptive error instead of reaching SQL, and resolved names are quoted with
// QuoteIdentifier before being embedded in the generated `SELECT COUNT(*) FROM ... [WHERE ...]`
// query, executed through DB.Count (so a query that yields no rows counts as zero rather than
// ErrNoRows, though a plain COUNT(*) always yields exactly one row). colValPairs must have an
// even length with string keys, or it returns a descriptive error (see parseColValPairs).
func (db *DB) CountBy(ctx context.Context, tableName string, colValPairs ...any) (int64, error) {
	td, err := db.schema.GetByTableName(tableName)
	if err != nil {
		return 0, err
	}

	where, args, err := resolveWhere(td, colValPairs)
	if err != nil {
		return 0, err
	}

	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s.%s%s;`,
		QuoteIdentifier(db.searchPath), QuoteIdentifier(td.Name), where)

	return db.Count(ctx, query, args...)
}

// SelectSingle executes query and scans the single resulting row into destPtr, whose
// struct type must be registered in the Schema; it returns ErrNoRows when the query
// yields none.
//
// destPtr's type is resolved through the Schema (Schema.Get, via reflect.TypeOf(destPtr)) so
// the returned row can be scanned column-by-name into the matching struct fields, the same way
// SelectByID and SelectByUsernameAndPassword do; unlike DeleteByID/DeleteBy/ExistsBy/CountBy,
// query itself is caller-supplied SQL (not built from a table name), so it is the caller's
// responsibility to keep it safe. SelectSingle does not parse or validate it.
func (db *DB) SelectSingle(ctx context.Context, destPtr any, query string, args ...any) error {
	td, err := db.schema.Get(reflect.TypeOf(destPtr))
	if err != nil {
		return err
	}

	return db.selectSingleTable(ctx, td, destPtr, query, args...)
}
