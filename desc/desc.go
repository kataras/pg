// Package desc holds the descriptors and SQL builders behind the pg package's `pg:"..."`
// struct-tag parsing: Table and Column definitions, the query builders that turn a Table
// (and, where relevant, a struct value) into CREATE TABLE, INSERT, UPDATE, DELETE and
// EXISTS statements, and the row-scanning code that maps a query's result rows back onto
// a struct. Most applications consume this package only indirectly, through pg.DB and
// pg.Repository[T]; it is exported mainly for the gen subpackage and for callers that need
// to inspect or build table/column definitions directly (e.g. a custom TableFilter).
package desc

var (
	// DefaultTag is the default struct field tag.
	DefaultTag = "pg"
	// DefaultSearchPath is the default search path for the table.
	DefaultSearchPath = "public"
)
