package desc

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// BuildInsertQuery builds and returns an SQL query for inserting a row into the table,
// based on the given struct value, arguments, and returning column.
// The struct value is a reflect.Value of the struct that represents the row to be inserted.
// The arguments are a slice of Argument that contains the column definitions and values for each field of the struct.
// The returning column is an optional string that specifies which column to return after the insertion,
// such as the primary key or any other generated value.
func BuildInsertQuery(td *Table, structValue reflect.Value, idPtr any, forceOnConflictExpr string, upsert bool) (string, []any, error) {
	returningColumn := "" // a variable to store the name of the column to return after insertion
	if idPtr != nil {
		// if idPtr is not nil, it means we want to get the primary key value of the inserted row
		columnDefinition, ok := td.PrimaryKey() // get the primary key column definition from the table definition
		if ok && idPtr != nil {
			returningColumn = columnDefinition.Name // assign the column name to returningColumn
		}
	}

	// find the arguments for the SQL query based on the struct value and the table definition
	args, err := extractArguments(td, structValue)
	if err != nil {
		return "", nil, err // return the error if finding arguments fails
	}

	if len(args) == 0 {
		return "", nil, fmt.Errorf(`no arguments found, maybe missing struct field tag of "%s"`, DefaultTag) // return an error if no arguments are found.
	}

	// build the SQL query using the table definition,
	// the arguments and the returning column
	query, err := buildInsertQuery(td, args, returningColumn, forceOnConflictExpr, upsert)
	if err != nil {
		return "", nil, err
	}

	return query, args.Values(), nil
}

func buildInsertQuery(td *Table, args Arguments, returningColumn string, forceOnConflictExpr string, upsert bool) (string, error) {
	var b strings.Builder

	// INSERT INTO "schema"."tableName"
	b.WriteString(`INSERT INTO`)
	b.WriteByte(' ')
	writeTableName(&b, td.SearchPath, td.Name)
	b.WriteByte(' ')

	var (
		namedParametersValues = make([]string, 0, len(td.Columns))
		columnNamesToInsert   = make([]string, 0, len(td.Columns))
		paramIndex            int

		conflicts []string
	)

	// (record_id,record)
	b.WriteByte(leftParenLiteral)

	onConflictExpression, hasConflict := td.OnConflict()

	i := 0
	for _, a := range args {
		c := a.Column

		if i > 0 {
			b.WriteByte(',')
		}

		if hasConflict {
			// if conflict is empty then an error of: duplicate key value violates unique constraint "$key"
			// will be fired, otherwise even if one is exists
			// then error will be ignored but we can't use the returning id.

			if c.Unique {
				conflicts = append(conflicts, c.Name)
			}
		} else if c.UniqueIndex != "" {
			conflicts = append(conflicts, c.Name)
		}

		paramIndex++ // starts from 1.
		paramIndexStr := strconv.Itoa(paramIndex)
		paramName := "$" + paramIndexStr

		if c.Password {
			if td.PasswordHandler.canEncrypt() {
				// handled at args state.
			} else {
				paramName = buildInsertPassword(paramName)
			}
		}

		namedParametersValues = append(namedParametersValues, paramName)
		columnNamesToInsert = append(columnNamesToInsert, c.Name)

		b.WriteString(c.Name)
		i++
	}

	if len(namedParametersValues) == 0 {
		return "", fmt.Errorf("no columns to insert")
	}

	// set on conflict expression by custom unqiue index or column name
	// even if upsert is not true.
	if forceOnConflictExpr != "" {
		uniqueIndexes := td.UniqueIndexes()

		selectedUniqueIndexColumns, ok := uniqueIndexes[forceOnConflictExpr]
		if ok {
			conflicts = selectedUniqueIndexColumns // override the conflicts.
		} else {
			// if not found then check for unique index OR unique by column name.
			for _, conflict := range conflicts {
				if conflict == forceOnConflictExpr {
					conflicts = []string{forceOnConflictExpr} // override the conflicts.
				}
			}
		}

		if len(conflicts) == 0 { // force check of conflicts.
			return "", fmt.Errorf("can't find unique index with name: %s", forceOnConflictExpr)
		}

		// override the on conflict expression.
		onConflictExpression = `DO UPDATE SET `
		j := 0
		for _, colName := range columnNamesToInsert {
			excluded := false
			for _, conflict := range conflicts { // skip the conflict columns.
				if conflict == colName {
					excluded = true
				}
			}

			if excluded {
				continue
			}
			if j > 0 {
				onConflictExpression += ","
			}

			onConflictExpression += fmt.Sprintf(`%s = EXCLUDED.%s`, colName, colName)
			j++
		}
	} else if upsert && len(conflicts) > 0 && !hasConflict {
		// if asked for upsert and forceOnConflictExpr is empty, conflicts are set from unique_index or unique as always,
		// but on conflict tag was not set manually then generate a full upsert method to update all columns.

		// override the on conflict expression.
		onConflictExpression = `DO UPDATE SET `
		j := 0
		for _, colName := range columnNamesToInsert {
			excluded := false
			for _, conflict := range conflicts { // skip the conflict columns.
				if conflict == colName {
					excluded = true
				}
			}

			if excluded {
				continue
			}
			if j > 0 {
				onConflictExpression += ","
			}

			onConflictExpression += fmt.Sprintf(`%s = EXCLUDED.%s`, colName, colName)
			j++
		}
	} else {
		// If had unique tags but no custom on conflict expression then ignore them,
		// so the caller receives a duplication error.
		conflicts = nil
	}

	b.WriteByte(rightParenLiteral)

	// VALUES($1,$2,$3)
	b.WriteByte(' ')
	b.WriteString(`VALUES`)

	b.WriteByte(leftParenLiteral)
	b.WriteString(strings.Join(namedParametersValues, ","))
	b.WriteByte(rightParenLiteral)

	if len(conflicts) > 0 {
		// ON CONFLICT(record_id)
		b.WriteByte(' ')
		b.WriteString(`ON CONFLICT`)

		b.WriteByte(leftParenLiteral)
		b.WriteString(strings.Join(conflicts, ","))
		b.WriteByte(rightParenLiteral)

		b.WriteByte(' ')
		b.WriteString(onConflictExpression)

		if returningColumn != "" && strings.Contains(strings.ToUpper(onConflictExpression), "DO UPDATE") {
			// we can still use the returning column (source: https://stackoverflow.com/a/37543015).
			writeInsertReturning(&b, returningColumn)
		}
	} else if returningColumn != "" {
		writeInsertReturning(&b, returningColumn)
	}

	b.WriteByte(';')

	query := b.String()
	return query, nil
}

func writeTableName(b *strings.Builder, schema, tableName string) {
	b.WriteString(strconv.Quote(schema))
	b.WriteByte('.')
	b.WriteString(strconv.Quote(tableName))
}

// PasswordAlg is the password algorithm the library uses to tell postgres
// how to generate a password field's salt.
// Alternatives:
// md5
// xdes
// des
var PasswordAlg = "bf" // max password length: 72, salt bits: 128, output length: 60, blowfish-based.

const (
	singleQuoteLiteral = '\''
)

// crypt($1,gen_salt('PasswordAlg'))
func buildInsertPassword(paramName string) string {
	var b strings.Builder

	// crypt($1,
	b.WriteString(`crypt`)
	b.WriteByte(leftParenLiteral)
	b.WriteString(paramName)

	b.WriteByte(',')

	// gen_salt('bf')
	b.WriteString(`gen_salt`)
	b.WriteByte(leftParenLiteral)
	b.WriteByte(singleQuoteLiteral)
	b.WriteString(PasswordAlg)
	b.WriteByte(singleQuoteLiteral)
	b.WriteByte(rightParenLiteral)

	// )
	b.WriteByte(rightParenLiteral)
	return b.String()
}

func writeInsertReturning(b *strings.Builder, columnKey string) {
	b.WriteByte(' ')
	b.WriteString(`RETURNING`)
	b.WriteByte(' ')
	b.WriteString(columnKey)
}
