package desc

import (
	"fmt"
	"sort"
	"strings"
)

// ColumnFilter is a function that returns whether this column should be live inside a table.
type ColumnFilter func(*Column) bool

// mergeColumnFilters merges multiple ColumnFilters into one.
// func mergeColumnFilters(filters ...ColumnFilter) ColumnFilter {
// 	if len(filters) == 0 {
// 		return func(*Column) bool {
// 			return true
// 		}
// 	}
//
// 	toFilter := func(c *Column) bool {
// 		for _, filter := range filters {
// 			if !filter(c) {
// 				return false
// 			}
// 		}
//
// 		return true
// 	}
//
// 	return toFilter
// }

const wildcardLiteral = "*"

// columnFilterExpression is a type that represents a column filter expression.
type columnFilterExpression struct {
	// store input here.
	input string
	//
	tableName           string
	columnName          string
	columnDataType      DataType
	prefix              string
	suffix              string
	notEqualTo          string
	containsColumnNames []string

	// For custom data storage.
	Data any // data to store.
}

// sortColumnFilterExpressions sorts the given ColumnFilterExpressions by static to more dynamic.
func sortColumnFilterExpressions(expressions []*columnFilterExpression) {
	sort.SliceStable(expressions, func(i, j int) bool {
		c1 := expressions[i]
		c2 := expressions[j]

		if c1.tableName != wildcardLiteral && c1.columnName != wildcardLiteral && c1.columnDataType != InvalidDataType && len(c1.containsColumnNames) > 0 {
			return true
		}

		if c1.tableName == wildcardLiteral && c2.tableName == wildcardLiteral && c1.columnName != wildcardLiteral && c2.columnName != wildcardLiteral {
			// checks like target_date pointer and target_date not pointer field types.
			return len(c1.containsColumnNames) > len(c2.containsColumnNames)
		}

		if c1.tableName != wildcardLiteral && c1.columnName != wildcardLiteral && c1.columnDataType != InvalidDataType {
			return true
		}

		if c1.tableName != wildcardLiteral && c1.columnName != wildcardLiteral &&
			c2.tableName == wildcardLiteral || c2.columnName == wildcardLiteral {
			return true
		}

		if c1.tableName != wildcardLiteral && c2.tableName == wildcardLiteral {
			return true
		}

		if c1.columnName != wildcardLiteral && c2.columnName == wildcardLiteral {
			return true
		}

		return false
	})
}

// String returns the filter's raw input.
func (p *columnFilterExpression) String() string {
	return p.input
}

// tableNameIsWildcard returns true if the table name is wildcard.
func (p *columnFilterExpression) tableNameIsWildcard() bool {
	return p.tableName == wildcardLiteral
}

// columnNameIsWildcard returns true if the column name is wildcard.
func (p *columnFilterExpression) columnNameIsWildcard() bool {
	return p.columnName == wildcardLiteral
}

// BuildColumnFilter returns a ColumnFilter.
func (p *columnFilterExpression) BuildColumnFilter(otherColumnNamesInsideTheTable []string) ColumnFilter {
	return func(c *Column) bool {
		if p.tableName == "" || p.columnName == "" {
			return false
		}

		if !p.tableNameIsWildcard() {
			if c.TableName != p.tableName {
				return false
			}
		}

		if p.columnDataType != InvalidDataType {
			if c.Type != p.columnDataType {
				return false
			}
		}

		if p.prefix != "" {
			if !strings.HasPrefix(c.Name, p.prefix) {
				return false
			}
		} else if p.suffix != "" {
			if !strings.HasSuffix(c.Name, p.suffix) {
				return false
			}
		} else if p.notEqualTo != "" {
			if c.Name == p.notEqualTo {
				return false
			}
		} else {
			if !p.columnNameIsWildcard() {
				if c.Name != p.columnName {
					return false
				}
			}
		}

		if len(p.containsColumnNames) > 0 {
			foundCount := 0
			for _, columnName := range p.containsColumnNames {
				for _, v := range otherColumnNamesInsideTheTable {
					if columnName == v {
						foundCount++
						break
					}
				}
			}

			if foundCount != len(p.containsColumnNames) {
				return false
			}
		}

		return true
	}
}

// parseColumnFilterExpression parses the input string and returns a slice of columnFilterExpression.
func parseColumnFilterExpression(input string) ([]*columnFilterExpression, error) {
	var expressions []*columnFilterExpression

	fields := strings.FieldsFunc(input, func(r rune) bool {
		return r == '.'
	})

	if len(fields) < 2 || len(fields) > 3 {
		return nil, fmt.Errorf("invalid input: %s", input)
	}

	tableName := fields[0]
	columnLine := fields[1]

	columnName := columnLine
	dataType := InvalidDataType
	if len(fields) == 3 {
		dataType, _ = ParseDataType(fields[2])
		if dataType == InvalidDataType {
			return nil, fmt.Errorf("invalid data type: %s", fields[2])
		}
	}

	var tableShouldContainColumnNames []string

	containsIdx := strings.IndexByte(columnLine, '&')
	if containsIdx > 0 {
		rest := columnLine[containsIdx+1:]
		tableShouldContainColumnNames = strings.Split(rest, ",")
		columnName = columnLine[0:containsIdx]
	}

	var moreColumnNames []string
	multipleIdx := strings.IndexByte(columnLine, ',')
	containsColumns := multipleIdx > 0 && (containsIdx == -1 || multipleIdx < containsIdx)
	if containsColumns { // for order, maybe we can improve it even better.
		columnName = columnLine[0:multipleIdx]
	}

	prefix, suffix, notEqualTo := parseColumnNameFilterFuncs(columnName)
	expr := &columnFilterExpression{
		input:               input,
		tableName:           tableName,
		columnName:          columnName,
		columnDataType:      dataType,
		prefix:              prefix,
		suffix:              suffix,
		notEqualTo:          notEqualTo,
		containsColumnNames: tableShouldContainColumnNames,
	}

	expressions = append(expressions, expr)

	if containsColumns {
		rest := columnLine[multipleIdx+1:]
		stopIdx := strings.IndexFunc(rest, func(r rune) bool {
			return r == '&' // || r == rune(rest[len(rest)-1]) // & or last letter.
		})

		if stopIdx == -1 {
			stopIdx = len(rest)
		}

		moreColumnNames = strings.Split(rest[0:stopIdx], ",")

		//	fmt.Printf("rest:stopidx = %s, more column names: %s\n", rest[0:stopIdx], strings.Join(moreColumnNames, ",)"))

		for _, columnName := range moreColumnNames {
			prefix, suffix, notEqualTo := parseColumnNameFilterFuncs(columnName)
			expr := &columnFilterExpression{
				input:               input,
				tableName:           tableName,
				columnName:          columnName,
				columnDataType:      dataType,
				prefix:              prefix,
				suffix:              suffix,
				notEqualTo:          notEqualTo,
				containsColumnNames: tableShouldContainColumnNames,
			}

			expressions = append(expressions, expr)
		}

	}

	return expressions, nil
}

func parseColumnNameFilterFuncs(columnName string) (prefix string, suffix string, notEqualTo string) {
	if strings.HasPrefix(columnName, "prefix(") {
		prefix = strings.TrimPrefix(columnName, "prefix(")
		prefix = strings.TrimSuffix(prefix, ")")
		columnName = prefix // assume the column name is the same as the prefix
	} else if strings.HasPrefix(columnName, "suffix(") {
		suffix = strings.TrimPrefix(columnName, "suffix(")
		suffix = strings.TrimSuffix(suffix, ")")
		columnName = suffix // assume the column name is the same as the suffix
	} else if strings.HasPrefix(columnName, "noteq(") {
		notEqualTo = strings.TrimPrefix(columnName, "noteq(")
		notEqualTo = strings.TrimSuffix(notEqualTo, ")")
		columnName = notEqualTo // assume the column name is the same as the not equal value
	}

	return
}
