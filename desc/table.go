package desc

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// TableType is a type that represents the type of a table.
type TableType uint8

const (
	// TableTypeBase is the default table type.
	TableTypeBase TableType = iota
	// TableTypeView is the view table type.
	// The table is read-only.
	TableTypeView
	// TableTypeMaterializedView is the materialized view table type.
	// The table is read-only but it can be refreshed.
	TableTypeMaterializedView
	// TableTypePresenter is the presenter table type.
	// It's not a view neither a normal table.
	// It can be used to decode custom select queries.
	TableTypePresenter
)

// IsReadOnly returns true if the table is a simple view or materialized view.
func (t TableType) IsReadOnly() bool {
	return t == TableTypeView || t == TableTypeMaterializedView || t == TableTypePresenter
}

// IsRefreshable returns true if the table is a materialized view.
func (t TableType) IsRefreshable() bool {
	return t == TableTypeMaterializedView
}

// DatabaseTableTypes is a slice of TableType that contains all the table types that are database-relative.
var DatabaseTableTypes = []TableType{TableTypeBase, TableTypeView, TableTypeMaterializedView}

// ParseTableType parses the given string and returns the corresponding TableType.
func ParseTableType(s string) TableType {
	switch s {
	case "BASE TABLE":
		return TableTypeBase
	case "VIEW":
		return TableTypeView
	case "MATERIALIZED VIEW":
		return TableTypeMaterializedView
	default:
		return TableTypeBase
	}
}

// Table is a type that represents a table definition for the database.
type Table struct {
	RegisteredPosition int // the position of the table in the schema

	// Type is the kind of table (base table, view, materialized view or presenter).
	Type TableType

	StructName  string       // the name of the struct that represents the table
	StructType  reflect.Type `json:"-"` // the type of the struct that represents the table
	SearchPath  string       // the search path for the table
	Name        string       // the name of the table
	Description string       // the description of the table
	Strict      bool         // if true then the select queries will return an error if a column is missing from the struct's fields
	Columns     []*Column    // a slice of pointers to Column that represents the columns of the table

	// PasswordHandler is the password handler used to encrypt/decrypt this table's
	// password column(s), set from Schema.HandlePassword when the table is registered.
	// It is nil if the schema has no password handler configured.
	PasswordHandler *PasswordHandler
}

// GetHumanName returns the human-friendly name of the table.
func (td *Table) GetHumanName() string {
	if td.StructName != "" {
		return td.StructName
	}

	return PascalCase(td.Name)
}

// IsType returns true if the table is one of the given types (true if no typesToCheck provided).
func (td *Table) IsType(typesToCheck ...TableType) bool {
	if len(typesToCheck) == 0 {
		return true
	}

	return slices.Contains(typesToCheck, td.Type)
}

// IsReadOnly returns true if the table is a simple view or materialized view.
func (td *Table) IsReadOnly() bool {
	return td.Type.IsReadOnly()
}

// AddColumns adds the given columns to the table definition.
func (td *Table) AddColumns(columns ...*Column) {
	for _, c := range columns {
		c.Table = td
	}
	td.Columns = append(td.Columns, columns...)
}

// RemoveColumns removes the columns with the given names from the table definition.
func (td *Table) RemoveColumns(columnNames ...string) {
	// slices.DeleteFunc removes every column whose name is in columnNames, whereas the loop
	// this replaced removed only the first match per name; column names are unique within a
	// table, so at most one column can ever match a given name and the two are equivalent.
	td.Columns = slices.DeleteFunc(td.Columns, func(c *Column) bool {
		return slices.Contains(columnNames, c.Name)
	})
}

// ListColumnsWithoutGenerated returns the columns of the table definition except the generated ones.
func (td *Table) ListColumnsWithoutGenerated() []*Column {
	columns := make([]*Column, 0, len(td.Columns))
	for _, c := range td.Columns {
		if c.IsGenerated() {
			continue
		}

		columns = append(columns, c)
	}

	return columns
}

func (td *Table) listColumnsForSelectWithoutGenerated() []*Column {
	columns := make([]*Column, 0, len(td.Columns))

	for _, c := range td.Columns {
		if c.IsGenerated() {
			continue
		}

		if c.Presenter {
			continue
		}

		columns = append(columns, c)
	}

	return columns
}

// ListColumnsWithoutPresenter returns the columns of the table definition except the presenter ones.
func (td *Table) ListColumnsWithoutPresenter() []*Column {
	columns := make([]*Column, 0, len(td.Columns))
	for _, c := range td.Columns {
		if c.Presenter {
			continue
		}

		columns = append(columns, c)
	}

	return columns
}

// NumInsertableColumns returns the number of columns that occupy a position, either a $N bind
// parameter or a literal DEFAULT keyword, in the column list of a bulk-insert statement built
// by BuildBulkInsertQuery (desc/insert_query.go).
//
// It mirrors, EXACTLY, BuildBulkInsertQuery's column-selection predicate (its loop over
// td.Columns that skips columns where c.AutoGenerated || c.Presenter is true): the two must be
// kept in sync, or callers that size batches from this count (e.g. Repository.InsertMany,
// Repository.UpsertMany) will silently drift from the SQL BuildBulkInsertQuery actually emits.
//
// Callers use this to keep multi-row INSERT/UPSERT statements under PostgreSQL's 65535
// bind-parameters-per-statement limit: max rows per batch = 65535 / NumInsertableColumns().
func (td *Table) NumInsertableColumns() int {
	n := 0
	for _, c := range td.Columns {
		if c.AutoGenerated || c.Presenter {
			continue
		}

		n++
	}

	return n
}

// ListColumnNames returns all the column names of the table definition, including presenters.
func (td *Table) ListColumnNames() []string {
	names := make([]string, 0, len(td.Columns))
	for _, c := range td.Columns {
		names = append(names, c.Name)
	}

	return names
}

// ListColumnNamesWithoutPresenter returns the column names of the table definition.
func (td *Table) ListColumnNamesWithoutPresenter() []string {
	names := make([]string, 0, len(td.Columns))
	for _, c := range td.Columns {
		if c.Presenter {
			continue
		}
		names = append(names, c.Name)
	}

	return names
}

// ListColumnNamesExcept returns the column names of the table definition except the given ones.
func (td *Table) ListColumnNamesExcept(except ...string) []string {
	if len(except) == 0 {
		return td.ListColumnNames()
	}

	names := make([]string, 0, len(td.Columns))
	for _, c := range td.Columns {
		if !slices.Contains(except, c.Name) {
			names = append(names, c.Name)
		}
	}

	return names
}

// GetColumnByName returns the column definition with the given name,
// or nil if no such column exists in the table definition.
// The name comparison is case-insensitive.
func (td *Table) GetColumnByName(name string) *Column {
	for _, col := range td.Columns {
		if strings.EqualFold(col.Name, name) {
			return col
		}
	}

	return nil
}

// ColumnExists returns true if the table definition contains a column.
func (td *Table) ColumnExists(name string) bool {
	return td.GetColumnByName(name) != nil
}

// FilterColumns this will modify the columns list and the columns fields.
// Used internally by gen tool to apply the custom type resolver into the table.
func (td *Table) FilterColumns(filter ColumnFilter) {
	if filter == nil {
		return
	}

	for _, column := range td.Columns {
		// so this resolver have access to the current *Table definition, so it
		// can check if table.ColumnExists("source_id") then its target_date is *SimpleDate.
		ok := filter(column)
		if !ok {
			td.RemoveColumns(column.Name) // skip this column.
		}
	}
}

// GetUsernameColumn returns the first column marked as username.
func (td *Table) GetUsernameColumn() *Column {
	for _, col := range td.Columns {
		if col.Username {
			return col
		}
	}

	return nil
}

// GetPasswordColumn returns the first column marked as password.
func (td *Table) GetPasswordColumn() *Column {
	for _, col := range td.Columns {
		if col.Password {
			return col
		}
	}

	return nil
}

// SetStrict sets the strict flag of the table definition to the given value,
// and returns the table definition itself for chaining.
// The strict flag determines whether the table definition allows
// extra fields that are not defined in the columns.
func (td *Table) SetStrict(v bool) *Table {
	td.Strict = v
	return td
}

// ForeignKeyColumnNames returns a slice of column names that have
// a foreign key reference to another table.
func (td *Table) ForeignKeyColumnNames() []string {
	var names []string
	for _, c := range td.Columns {
		if c.ReferenceTableName != "" {
			names = append(names, c.Name)
		}
	}

	return names
}

// PrimaryKey returns the primary key's column of the
// row definition and reports if there is one.
func (td *Table) PrimaryKey() (*Column, bool) { // TODO: think of making it a static variable but keep the function somehow.
	for _, c := range td.Columns {
		if c.PrimaryKey {
			return c, true
		}
	}

	return nil, false
}

// OnConflict returns the first (and only one valid) ON CONFLICT=$Conflict.
// This is used to specify what action to take when a row conflicts with
// an existing row in the table.
func (td *Table) OnConflict() (string, bool) {
	for _, c := range td.Columns {
		if c.Conflict != "" {
			return c.Conflict, true
		}
	}

	return "", false
}

// UniqueIndexes is a type that represents a map of unique index names to column names.
// A unique index is a constraint that ensures that no two rows in a table have the same values
// for a set of columns.
type UniqueIndexes map[string][]string /* key = index name, value = set of columns */

// UniqueIndexes returns a UniqueIndexes map for the table definition,
// based on the UniqueIndex field of each column.
func (td *Table) UniqueIndexes() UniqueIndexes {
	uniqueIndexes := make(UniqueIndexes, 0)

	for _, c := range td.Columns {
		if c.UniqueIndex == "" {
			continue
		}

		// ensure uniqueness of unique indexes.
		uniqueIndexes[c.UniqueIndex] = append(uniqueIndexes[c.UniqueIndex], c.Name)
	}

	return uniqueIndexes
}

// Index is a type that represents an index definition for a column.
// An index is a data structure that improves the speed of data retrieval operations
// on a table. An index can be of different types, such as B-tree, hash, etc.
type Index struct {
	// TableName is the name of the table the index belongs to.
	TableName string
	// ColumnName is the name of the column the index is built on.
	ColumnName string

	// Name is the index's name, e.g. "<table>_<column>_idx" or "<table>_pkey".
	Name string
	// Type is the index's access method (btree, gin, ...).
	Type IndexType
}

// Indexes returns a slice of Index for the table definition,
// based on the Index field of each column.
func (td *Table) Indexes() []*Index {
	var indexes []*Index

	for _, c := range td.Columns {
		if c.Index == InvalidIndex {
			continue
		}

		var indexName string

		// blog_posts_blog_id_fkey
		if c.ReferenceColumnName != "" {
			indexName = fmt.Sprintf("%s_%s_fkey", td.Name, c.Name) // this should match the constraint name of the `information_schema.referential_constraints` used on ListTablesInformationSchema query.
		} else if c.PrimaryKey {
			indexName = fmt.Sprintf("%s_pkey", td.Name)
		} else {
			indexName = fmt.Sprintf("%s_%s_idx", td.Name, c.Name)
		}

		// indexName := "idx_" + td.Name + "_" + c.Name

		indexes = append(indexes, &Index{
			TableName:  td.Name,
			ColumnName: c.Name,
			Name:       indexName,
			Type:       c.Index,
		})
	}

	return indexes
}

// ForeignKeys returns a slice of ForeignKey for the table definition,
// based on the ReferenceTableName field of each column.
func (td *Table) ForeignKeys() []ForeignKeyConstraint {
	var fks []ForeignKeyConstraint

	for _, c := range td.Columns {
		if c.ReferenceTableName != "" {
			fks = append(fks, ForeignKeyConstraint{
				ColumnName:          c.Name,
				ReferenceTableName:  c.ReferenceTableName,
				ReferenceColumnName: c.ReferenceColumnName,
				Deferrable:          c.DeferrableReference,
				OnDelete:            c.ReferenceOnDelete,
			})
		}
	}

	return fks
}

// ListImportPaths returns a slice of import paths for the table definition,
// based on the FieldType field of each column.
func (td *Table) ListImportPaths() []string {
	var importPaths []string
	for _, c := range td.Columns {
		if c.FieldType == nil || c.FieldType.Kind() == reflect.Invalid {
			continue
		}

		importPath := c.FieldType.PkgPath()
		if importPath == "" {
			continue
		}

		if !slices.Contains(importPaths, importPath) {
			importPaths = append(importPaths, importPath)
		}
	}

	slices.SortFunc(importPaths, func(a, b string) int { // external dependencies on bottom.
		return cmp.Compare(strings.Count(a, "/"), strings.Count(b, "/"))
	})

	return importPaths
}

type (
	// TableFilter is an interface that table loopers should implement.
	TableFilter interface {
		// FilterTable returns true if the table should be filtered out.
		FilterTable(*Table) bool
	}

	// TableFilterFunc is a function that implements the TableFilter interface.
	TableFilterFunc func(*Table) bool

	// Expression is a filter expression.
	Expression struct { // if we ever want to keep the order.
		// Input is the raw filter expression text, e.g. "customers.email" or
		// "customer_profiles.fields.jsonb", parsed by parseColumnFilterExpression.
		Input string
		// ResultFieldType, when non-nil, overrides the matched column(s)' FieldType.
		ResultFieldType reflect.Type
	}

	// Expressions is a slice of expressions.
	Expressions []Expression
)

// FilterTable implements the TableFilter interface.
func (fn TableFilterFunc) FilterTable(t *Table) bool {
	return fn(t)
}

var _ TableFilter = Expressions{}

// NewExpression returns a new Expression.
func NewExpression(input string, fieldType reflect.Type) Expression {
	return Expression{
		Input:           input,
		ResultFieldType: fieldType,
	}
}

// FilterTable implements the TableFilter interface.
//
// FilterTable panics if any of the expressions' Input strings is not a well-formed filter
// expression. It cannot return an error instead, since the exported TableFilter interface's
// FilterTable(*Table) bool signature has no error return, but the panic value is always a
// wrapped, descriptive error naming the offending expression, so a recovering caller (see
// pg.DB.ListTables, which uses an unexported safeFilterTable helper) can turn it back into a
// normal error rather than a raw panic value.
func (expressions Expressions) FilterTable(t *Table) bool {
	if len(expressions) == 0 {
		return true
	}

	columnNames := t.ListColumnNames()

	var parsedExpressions []*columnFilterExpression
	// m := make(map[int]int) // key = the Expression index, value = the parsedExpressions index,
	// as they can be more than one parsedExpression per Expression.
	for _, expr := range expressions {
		parsedExprs, err := parseColumnFilterExpression(expr.Input)
		if err != nil {
			panic(fmt.Errorf("pg: filter expression %q: %w", expr.Input, err))
		}

		for _, parsedExpr := range parsedExprs {
			parsedExpr.Data = expr.ResultFieldType
		}

		// for j := range parsedExprs {
		// 	m[len(parsedExpressions)+j] = expr.ResultFieldType
		// }
		// doesn't work because we re-order them afterawrds.
		parsedExpressions = append(parsedExpressions, parsedExprs...)
	}
	sortColumnFilterExpressions(parsedExpressions)

	columnFilters := make([]ColumnFilter, 0, len(parsedExpressions))
	for _, expr := range parsedExpressions {
		columnFilter := expr.BuildColumnFilter(columnNames)
		columnFilters = append(columnFilters, columnFilter)
	}

	t.FilterColumns(func(c *Column) bool {
		for i, filter := range columnFilters {
			if filter(c) {
				// Comma-ok: Data is only ever set to a reflect.Type above (from
				// Expression.ResultFieldType), but that field can itself be a nil
				// reflect.Type (e.g. NewExpression(input, nil)), in which case the stored
				// Data is a bare nil interface and a plain type assertion would panic.
				// Treat anything that isn't a genuine, non-nil reflect.Type as "no override".
				if typ, ok := parsedExpressions[i].Data.(reflect.Type); ok && typ != nil {
					c.FieldType = typ // expressions[i].ResultFieldType // the indexes between expressions and column filters match.
					c.isPtr = typ.Kind() == reflect.Pointer
				}

				break // stop on first filter match.
			}
		}

		return true // no matter what, here we don't want to remove columns.
	})

	return true
}
