package desc

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// BuildUpdateQuery builds and returns an SQL query for updating a row in the table,
// using the given struct value and the primary key.
func BuildUpdateQuery(value any, columnsToUpdate []string, reportNotFound bool, primaryKey *Column) (string, []any, error) {
	args, err := extractUpdateArguments(value, columnsToUpdate, primaryKey)
	if err != nil {
		return "", nil, err
	}

	shouldUpdateID := slices.Contains(columnsToUpdate, primaryKey.Name)

	if len(args) == 1 { // the last one is the id.
		return "", nil, fmt.Errorf("no arguments found for update, maybe missing struct field tag of \"%s\"", DefaultTag)
	}

	// build the SQL query using the table definition and its primary key.
	query, err := buildUpdateQuery(primaryKey.Table, args, primaryKey.Name, shouldUpdateID, reportNotFound)
	if err != nil {
		return "", nil, err
	}

	return query, args.Values(), nil
}

// extractUpdateArguments extracts the arguments from the given struct value and returns them.
func extractUpdateArguments(value any, columnsToUpdate []string, primaryKey *Column) (Arguments, error) {
	structValue := IndirectValue(value)

	id, err := ExtractPrimaryKeyValue(primaryKey, structValue)
	if err != nil {
		return nil, err
	}

	columnsToUpdateLength := len(columnsToUpdate)

	args, err := extractArguments(primaryKey.Table, structValue, func(fieldName string) bool {
		if columnsToUpdateLength == 0 {
			// full update.
			return true
		}

		for _, onlyColumnName := range columnsToUpdate {
			if onlyColumnName == fieldName {
				return true
			}
		}

		return false
	})
	if err != nil {
		return nil, err // return the error if finding arguments fails
	}

	if columnsToUpdateLength == 0 {
		// full update, even zero values (e.g. integer 0) all except ID and any created_at, updated_at.
		args = filterArgumentsForFullUpdate(args)
	}

	if len(args) == 0 {
		// nothing to update, raise an error
		return nil, fmt.Errorf(`no arguments found for update, maybe missing struct field tag of "%s"`, DefaultTag)
	}

	// Add (or move) the primary key value as the last argument,
	// move is a requiremend here in order to remove a duplicated primary key name in the query;
	// this can happen if the specified column names to update do not match the database schema.
	args.ShiftEnd(Argument{
		Column: primaryKey,
		Value:  id,
	})

	return args, nil
}

// buildUpdateQuery builds the UPDATE SQL statement. It returns an error, instead of building
// the query, if a password column's value would be updated via the db-side
// crypt($N, gen_salt('<PasswordAlg>')) SQL fragment (see buildInsertPassword) and PasswordAlg
// fails validatePasswordAlg; this mirrors the same guard on the insert paths in insert_query.go
// (BuildInsertQuery/BuildBulkInsertQuery), so every crypt-emitting builder validates PasswordAlg
// before it's interpolated into SQL.
func buildUpdateQuery(td *Table, args Arguments, primaryKeyName string, shouldUpdateID bool, reportNotFound bool) (string, error) {
	var b strings.Builder

	b.WriteString(`UPDATE "` + td.Name + `" SET `)

	var paramIndex int

	for i, a := range args {
		c := a.Column

		if !shouldUpdateID && c.Name == primaryKeyName {
			// Do not update ID if not specifically asked to.
			// Fixes #1.
			continue
		}

		if i > 0 {
			b.WriteByte(',')
		}

		paramIndex++ // starts from 1.
		paramIndexStr := strconv.Itoa(paramIndex)
		paramName := "$" + paramIndexStr

		if c.Password {
			if td.PasswordHandler.canEncrypt() {
				// handled at args state.
			} else {
				if err := validatePasswordAlg(); err != nil {
					return "", err
				}

				paramName = buildInsertPassword(paramName)
			}
		}

		fmt.Fprintf(&b, `"%s" = %s`, c.Name, paramName)
	}

	primaryKeyWhereIndex := paramIndex + 1
	if shouldUpdateID { // if updating ID, then the last argument is the ID.
		primaryKeyWhereIndex = paramIndex
	}
	b.WriteString(` WHERE "` + primaryKeyName + `" = $` + strconv.Itoa(primaryKeyWhereIndex))

	if reportNotFound {
		b.WriteString(` RETURNING "` + primaryKeyName + `"`)
	}

	b.WriteByte(';')

	return b.String(), nil
}
