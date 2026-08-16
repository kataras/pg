package desc

import "testing"

// selectExprTestTable is a hand-built Table (not derived from a struct, following the pattern
// already used by TestColumnFieldTagString and the Table-literal tests in table_test.go) with a
// normal column, a Presenter column and an Unscannable column, so SelectColumnsExpr and
// JSONBuildObjectExpr can be exercised against all three kinds without a live database.
func selectExprTestTable() *Table {
	return &Table{
		Name: "foods",
		Columns: []*Column{
			{Name: "id", TableName: "foods"},
			{Name: "name", TableName: "foods"},
			{Name: "search_vector", TableName: "foods", Unscannable: true},
			{Name: "display_name", TableName: "foods", Presenter: true},
		},
	}
}

func TestSelectColumnsExpr(t *testing.T) {
	td := selectExprTestTable()

	tests := []struct {
		name  string
		alias string
		want  string
	}{
		{name: "no alias", alias: "", want: `"id","name"`},
		{name: "alias", alias: "f", want: `f."id",f."name"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := td.SelectColumnsExpr(tt.alias); got != tt.want {
				t.Fatalf("SelectColumnsExpr(%q) = %q, want %q", tt.alias, got, tt.want)
			}
		})
	}
}

// TestSelectColumnsExprNoScannableColumns verifies the documented empty-string return when
// every column is either Presenter or Unscannable.
func TestSelectColumnsExprNoScannableColumns(t *testing.T) {
	td := &Table{
		Name: "empty_scannable",
		Columns: []*Column{
			{Name: "search_vector", TableName: "empty_scannable", Unscannable: true},
			{Name: "display_name", TableName: "empty_scannable", Presenter: true},
		},
	}

	if got := td.SelectColumnsExpr(""); got != "" {
		t.Fatalf("SelectColumnsExpr(%q) = %q, want empty string", "", got)
	}
}

func TestJSONBuildObjectExpr(t *testing.T) {
	td := selectExprTestTable()

	tests := []struct {
		name  string
		alias string
		want  string
	}{
		{name: "no alias", alias: "", want: `json_build_object('id', "id", 'name', "name")`},
		{name: "alias", alias: "f", want: `json_build_object('id', f."id", 'name', f."name")`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := td.JSONBuildObjectExpr(tt.alias); got != tt.want {
				t.Fatalf("JSONBuildObjectExpr(%q) = %q, want %q", tt.alias, got, tt.want)
			}
		})
	}
}

// TestJSONBuildObjectExprNoScannableColumns verifies the documented "json_build_object()"
// return when every column is either Presenter or Unscannable.
func TestJSONBuildObjectExprNoScannableColumns(t *testing.T) {
	td := &Table{
		Name: "empty_scannable",
		Columns: []*Column{
			{Name: "search_vector", TableName: "empty_scannable", Unscannable: true},
			{Name: "display_name", TableName: "empty_scannable", Presenter: true},
		},
	}

	want := "json_build_object()"
	if got := td.JSONBuildObjectExpr(""); got != want {
		t.Fatalf("JSONBuildObjectExpr(%q) = %q, want %q", "", got, want)
	}
}

// TestSelectColumnsExprQuotesColumnNames verifies that a column name requiring quoting (an
// embedded double quote) is sanitized via pgx.Identifier.Sanitize rather than embedded
// verbatim, mirroring the same guarantee already tested for other query builders in this
// package (see e.g. TestWriteTableNameQuoting in insert_query_test.go).
func TestSelectColumnsExprQuotesColumnNames(t *testing.T) {
	td := &Table{
		Name: "evil",
		Columns: []*Column{
			{Name: `evil"column`, TableName: "evil"},
		},
	}

	want := `"evil""column"`
	if got := td.SelectColumnsExpr(""); got != want {
		t.Fatalf("SelectColumnsExpr(%q) = %q, want %q", "", got, want)
	}
}
