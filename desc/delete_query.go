package desc

import "fmt"

// BuildDeleteQuery builds and returns a SQL query for deleting one or more rows from a table.
func BuildDeleteQuery(td *Table, values []any) (string, []any, error) {
	// extract the primary key column name and the primary key values from the table definition and the values
	primaryKeyName, ids, err := extractPrimaryKeyValues(td, values)
	if err != nil {
		return "", nil, err // return false and the wrapped error if extracting fails
	}

	// build the SQL query using the table name, the primary key name and a placeholder for the primary key values
	query := fmt.Sprintf(`DELETE FROM "%s" WHERE "%s" = ANY($1);`, td.Name, primaryKeyName)
	return query, ids, nil
}
