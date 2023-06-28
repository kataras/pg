package desc

import (
	"fmt"
	"reflect"
	"testing"
)

func TestParseColumnFilterExpression(t *testing.T) {
	var tests = []struct {
		input    string
		expected []columnFilterExpression
	}{
		{"tablename.column.varchar", []columnFilterExpression{
			{
				tableName:      "tablename",
				columnName:     "column",
				columnDataType: CharacterVarying,
			},
		}},
		{"*.column", []columnFilterExpression{
			{
				tableName:  "*",
				columnName: "column",
			},
		}},
		{"*.column.varchar", []columnFilterExpression{
			{
				tableName:      "*",
				columnName:     "column",
				columnDataType: CharacterVarying,
			},
		}},
		{"tablename.prefix(col).varchar", []columnFilterExpression{
			{
				tableName:      "tablename",
				columnName:     "prefix(col)",
				columnDataType: CharacterVarying,
				prefix:         "col",
			},
		}},
		{"tablename.suffix(col).varchar", []columnFilterExpression{
			{
				tableName:      "tablename",
				columnName:     "suffix(col)",
				columnDataType: CharacterVarying,
				suffix:         "col",
			},
		}},
		{"tablename.noteq(col).varchar", []columnFilterExpression{
			{
				tableName:      "tablename",
				columnName:     "noteq(col)",
				columnDataType: CharacterVarying,
				notEqualTo:     "col",
			},
		}},
		{"tablename.column1&column2,column3.varchar", []columnFilterExpression{
			{
				tableName:           "tablename",
				columnName:          "column1",
				columnDataType:      CharacterVarying,
				containsColumnNames: []string{"column2", "column3"},
			},
		}},
		{"tablename.column1&column2,column3", []columnFilterExpression{
			{
				tableName:           "tablename",
				columnName:          "column1",
				containsColumnNames: []string{"column2", "column3"},
			},
		}},
		{"*.column1,column2,column3&column4.character[]", []columnFilterExpression{
			{
				tableName:           "*",
				columnName:          "column1",
				columnDataType:      CharacterArray,
				containsColumnNames: []string{"column4"},
			},
			{
				tableName:           "*",
				columnName:          "column2",
				columnDataType:      CharacterArray,
				containsColumnNames: []string{"column4"},
			},
			{
				tableName:           "*",
				columnName:          "column3",
				columnDataType:      CharacterArray,
				containsColumnNames: []string{"column4"},
			},
		}},
		{"*.column1,column2,column3&column4", []columnFilterExpression{
			{
				tableName:           "*",
				columnName:          "column1",
				containsColumnNames: []string{"column4"},
			},
			{
				tableName:           "*",
				columnName:          "column2",
				containsColumnNames: []string{"column4"},
			},
			{
				tableName:           "*",
				columnName:          "column3",
				containsColumnNames: []string{"column4"},
			},
		}},
		{"tablename.column1,column2,column3&column4", []columnFilterExpression{
			{
				tableName:           "tablename",
				columnName:          "column1",
				containsColumnNames: []string{"column4"},
			},
			{
				tableName:           "tablename",
				columnName:          "column2",
				containsColumnNames: []string{"column4"},
			},
			{
				tableName:           "tablename",
				columnName:          "column3",
				containsColumnNames: []string{"column4"},
			},
		}},
	}

	for i, tt := range tests {
		exprs, err := parseColumnFilterExpression(tt.input)
		if err != nil {
			t.Fatal(err)
		}

		for j, expr := range exprs {
			tt.expected[j].input = tt.input

			if !reflect.DeepEqual(*expr, tt.expected[j]) {
				t.Fatalf("[%d:%d] [%s] expected:\n%v\nbut got:\n%v", i, j, tt.input, tt.expected[j], *expr)
			}
		}
	}
}

func TestParsedColumnFilterExpression(t *testing.T) {
	exprs, err := parseColumnFilterExpression("tablename.column1,column2,column3&column4.varchar")
	if err != nil {
		t.Fatal(err)
	}

	for i, expr := range exprs {
		filter := expr.BuildColumnFilter([]string{"column4"})
		column := &Column{
			TableName: "tablename",
			Name:      fmt.Sprintf("column%d", i+1),
			Type:      CharacterVarying,
		}

		if !filter(column) {
			t.Fatalf("[%d] expected to pass", i)
		}

		column2 := &Column{
			TableName: "other",
			Name:      fmt.Sprintf("column%d", i+1),
			Type:      CharacterVarying,
		}
		if filter(column2) {
			t.Fatalf("[%d] expected not to pass", i)
		}
	}
}
