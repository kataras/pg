package desc

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// BuildExistsQuery builds and returns an SQL query for checking of existing in a row in the table,
// based on the given struct value.
func BuildExistsQuery(td *Table, structValue reflect.Value) (string, []any, error) {
	args, err := extractArguments(td, structValue, nil)
	if err != nil {
		return "", nil, err // return the error if finding arguments fails
	}

	if len(args) == 0 {
		return "", nil, fmt.Errorf(`no arguments found for exists, maybe missing struct field tag of "%s"`, DefaultTag) // return an error if no arguments are found.
	}

	// build the SQL query using the table definition,
	// the arguments and the returning column
	query := buildExistsQuery(td, args)
	return query, args.Values(), nil
}

// buildExistsQuery builds and returns an SQL query for checking of existing in a row in the table,
// based on the given arguments.
func buildExistsQuery(td *Table, args Arguments) string {
	// Create a new strings.Builder
	var b strings.Builder

	// Write the query prefix
	b.WriteString(`SELECT EXISTS(SELECT 1 FROM "` + td.Name + `"`)

	buildWhereSubQueryByArguments(&b, args)

	// Write the query (EXISTS) suffix
	b.WriteString(")")

	b.WriteByte(';')

	// Return the query string
	return b.String()
}

func buildWhereSubQueryByArguments(b *strings.Builder, args Arguments) {
	b.WriteString(` WHERE `)

	var paramIndex int

	for i, a := range args {
		c := a.Column

		if i > 0 {
			b.WriteString(" AND ")
		}

		paramIndex++ // starts from 1.
		paramIndexStr := strconv.Itoa(paramIndex)
		paramName := "$" + paramIndexStr

		b.WriteString(fmt.Sprintf("%s = %s", c.Name, paramName))
	}
}
