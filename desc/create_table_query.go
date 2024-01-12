package desc

import (
	"fmt"
	"strconv"
	"strings"
)

// BuildCreateTableQuery creates a table in the database according to the given table definition.
func BuildCreateTableQuery(td *Table) string {
	// Generate the SQL query to create the table
	var query strings.Builder

	// Start with the CREATE TABLE statement and the table name
	query.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (", td.Name))

	columns := td.ListColumnsWithoutPresenter()
	// Loop over the columns and append their definitions to the query
	for i, col := range columns {
		// Add the column name and type
		query.WriteString(strconv.Quote(col.Name) + " " + col.Type.String())

		// Add the type argument if any
		if col.TypeArgument != "" {
			query.WriteString(fmt.Sprintf("(%s)", col.TypeArgument))
		}

		// Add the default value if any
		if col.Default != "" {
			query.WriteString(" DEFAULT " + col.Default)
		}
		// Add the NOT NULL constraint if applicable
		if !col.Nullable {
			query.WriteString(" NOT NULL")
		}
		// Add the UNIQUE constraint if applicable
		if col.Unique {
			query.WriteString(" UNIQUE")
		}

		// Add the CHECK constraint if any
		if col.CheckConstraint != "" {
			query.WriteString(fmt.Sprintf(" CHECK (%s)", col.CheckConstraint))
		}

		// Add a comma separator if this is not the last column.
		if i < len(columns)-1 {
			query.WriteString(", ")
		}
	}

	// Add the primary key constraint if any
	if primaryKey, ok := td.PrimaryKey(); ok {
		query.WriteString(fmt.Sprintf(`, PRIMARY KEY ("%s")`, primaryKey.Name))
	}

	// Loop over the foreign key constraints and append them to the query
	/* No, let's create foreign keys at the end of the all known tables creation,
	so registeration order does not matter.
	for _, fk := range td.ForeignKeys() {
		query.WriteString(fmt.Sprintf(", FOREIGN KEY (%s) REFERENCES %s(%s) ON DELETE %s", fk.ColumnName, fk.ReferenceTableName, fk.ReferenceColumnName, fk.OnDelete))

		// Add the DEFERRABLE option if applicable
		if fk.Deferrable {
			query.WriteString(" DEFERRABLE")
		}
	}
	See `buildAlterTableForeignKeysQuery`.
	*/

	// Loop over the unique indexes and append them to the query as constraints,
	// no WHERE clause is allowed in this case.
	//
	// Read more at: https://stackoverflow.com/questions/23542794/postgres-unique-constraint-vs-index
	for idxName, colNames := range td.UniqueIndexes() {
		for i := range colNames {
			colNames[i] = strconv.Quote(colNames[i]) // quote column names.
		}
		query.WriteString(fmt.Sprintf(", CONSTRAINT %s UNIQUE (%s)", idxName, strings.Join(colNames, ", ")))
	}

	// Close the CREATE TABLE statement with a semicolon
	query.WriteString(");")

	// Loop over the non-unique indexes and append them to the query as separate statements
	for _, idx := range td.Indexes() {
		// Use the CREATE INDEX statement with the index name, table name, type and column name
		query.WriteString(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s USING %s ("%s");`,
			idx.Name, td.Name, idx.Type.String(), idx.ColumnName))
	}

	return query.String()
}
