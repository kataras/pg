package desc

import (
	"fmt"
	"strings"
)

// BuildDuplicateQuery returns a query that duplicates a row by its primary key.
func BuildDuplicateQuery(td *Table, idPtr any) (string, error) {
	primaryKey, ok := td.PrimaryKey() // get the primary key column definition from the table definition
	if !ok {
		return "", fmt.Errorf("duplicate: no primary key")
	}

	returningColumn := "" // a variable to store the name of the column to return after insertion
	if idPtr != nil && ok {
		// if idPtr is not nil, it means we want to get the primary key value of the inserted row
		returningColumn = primaryKey.Name // assign the column name to returningColumn
	}

	var b strings.Builder

	// INSERT INTO "schema"."tableName"
	b.WriteString(`INSERT INTO`)
	b.WriteByte(' ')
	writeTableName(&b, td.SearchPath, td.Name)
	b.WriteByte(' ')

	// (name, tag, source_id)
	b.WriteByte(leftParenLiteral)

	columns := td.listColumnsForSelectWithoutGenerated()
	for i, c := range columns {
		if i > 0 {
			b.WriteByte(',')
		}

		b.WriteString(c.Name)
	}

	b.WriteByte(rightParenLiteral)

	// SELECT (name, tag, COALESCE(source_id, id))
	b.WriteByte(' ')
	b.WriteString(`SELECT`)
	b.WriteByte(' ')

	for i, c := range columns {
		if i > 0 {
			b.WriteByte(',')
		}

		columnName := c.Name
		if c.ReferenceColumnName == primaryKey.Name && c.ReferenceTableName == td.Name {
			// If self reference, then COALESCE(source_id, id).
			// This work as self reference is always a one-to-one relationship,
			// useful for applications that use tables for original vs daily weekly X plans.
			//
			// NOTE: TODO: However, keep a note that COALESCE may not work on that case, need testing on actual db.
			columnName = fmt.Sprintf("COALESCE(%s, %s)", c.Name, primaryKey.Name)
		}

		b.WriteString(columnName)
	}

	// FROM "schema"."tableName"
	b.WriteString(" FROM ")
	writeTableName(&b, td.SearchPath, td.Name)

	// WHERE id = $1
	buildWhereSubQueryByArguments(&b, Arguments{
		{
			Column: primaryKey,
		},
	})

	// RETURNING id
	// If returningColumn is not empty.
	writeInsertReturning(&b, returningColumn)

	b.WriteByte(';')

	query := b.String()
	return query, nil
}
