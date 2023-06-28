package desc

import (
	"fmt"
)

// BuildAlterTableForeignKeysQueries creates ALTER TABLE queries for adding foreign key constraints.
func BuildAlterTableForeignKeysQueries(td *Table) []string {
	foreignKeys := td.ForeignKeys()
	queries := make([]string, 0, len(foreignKeys))

	for _, fk := range foreignKeys {
		constraintName := fmt.Sprintf("%s_%s_fkey", td.Name, fk.ColumnName)

		dropQuery := fmt.Sprintf(`ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s;`, td.Name, constraintName)
		queries = append(queries, dropQuery)

		q := fmt.Sprintf(`ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s) ON DELETE %s`,
			td.Name, constraintName, fk.ColumnName, fk.ReferenceTableName, fk.ReferenceColumnName, fk.OnDelete)

		// Add the DEFERRABLE option if applicable
		if fk.Deferrable {
			q += " DEFERRABLE"
		}

		q += ";"
		queries = append(queries, q)
	}

	return queries
}
