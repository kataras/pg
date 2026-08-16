package desc

import (
	"strings"

	"github.com/jackc/pgx/v5"
)

// SelectColumnsExpr returns the table's scannable columns as a comma-separated,
// optionally alias-qualified list, for hand-written SELECT lists that must stay in sync
// with the struct: e.g. `f."id",f."name"` (alias "f") or `"id","name"` (alias "").
//
// "Scannable" means the columns a SELECT * scan of this table would bind: it is
// ListColumnsWithoutPresenter (excluding Presenter columns, which have no physical column to
// select) further narrowed to exclude Unscannable ones, e.g. a generated tsvector column,
// which ConvertRowsToStruct's scan path always skips regardless of what the query selects.
//
// Column names are individually quoted with pgx.Identifier.Sanitize. alias, when non-empty, is
// written verbatim immediately before each quoted column name followed by a literal '.'. It is
// never quoted, sanitized or otherwise validated, because it is meant to be a short,
// developer-authored SQL alias from a hand-written query (e.g. "f" in "... FROM foods f"), not
// untrusted input. Given a table with no scannable columns, it returns "".
func (td *Table) SelectColumnsExpr(alias string) string {
	columns := td.scannableColumns()
	if len(columns) == 0 {
		return ""
	}

	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}

	parts := make([]string, 0, len(columns))
	for _, c := range columns {
		parts = append(parts, prefix+pgx.Identifier{c.Name}.Sanitize())
	}

	return strings.Join(parts, ",")
}

// JSONBuildObjectExpr returns a json_build_object expression enumerating the table's
// scannable columns as 'col', alias."col" pairs, so embedded-object SELECTs stay in
// sync with the struct instead of hand-typing dozens of column names.
//
// See SelectColumnsExpr for what "scannable" means and how alias is treated: written verbatim
// before each quoted column reference, never quoted or validated itself, and meant to be a
// short, developer-authored SQL alias rather than untrusted input. Each pair's key (the string
// literal on the left of the comma) is the bare column name, single-quoted; its value (on the
// right) is the alias-qualified, double-quoted column reference. Given a table with no
// scannable columns, it returns "json_build_object()".
func (td *Table) JSONBuildObjectExpr(alias string) string {
	columns := td.scannableColumns()

	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}

	parts := make([]string, 0, len(columns))
	for _, c := range columns {
		parts = append(parts, "'"+c.Name+"', "+prefix+pgx.Identifier{c.Name}.Sanitize())
	}

	return "json_build_object(" + strings.Join(parts, ", ") + ")"
}

// scannableColumns returns the columns a SELECT * scan of td would bind: see
// SelectColumnsExpr's doc for the exact definition of "scannable" this implements.
func (td *Table) scannableColumns() []*Column {
	columns := td.ListColumnsWithoutPresenter()

	scannable := make([]*Column, 0, len(columns))
	for _, c := range columns {
		if c.Unscannable {
			continue
		}

		scannable = append(scannable, c)
	}

	return scannable
}
