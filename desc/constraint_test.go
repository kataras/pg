package desc

import (
	"reflect"
	"testing"
)

// TestParseForeignKeyConstraint tests the parseForeignKeyConstraint function with a variety
// of PostgreSQL foreign key definitions, ensuring that all supported actions (CASCADE, RESTRICT,
// NO ACTION, SET NULL, SET DEFAULT) for ON DELETE and ON UPDATE clauses—as well as the DEFERRABLE
// flag—are parsed correctly.
func TestParseForeignKeyConstraint(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *ForeignKeyConstraint
	}{
		{
			name:  "Minimal definition",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref)",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "",
				OnUpdate:            "",
				Deferrable:          false,
			},
		},
		{
			name:  "ON DELETE CASCADE",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON DELETE CASCADE",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "CASCADE",
				OnUpdate:            "",
				Deferrable:          false,
			},
		},
		{
			name:  "ON DELETE RESTRICT",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON DELETE RESTRICT",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "RESTRICT",
				OnUpdate:            "",
				Deferrable:          false,
			},
		},
		{
			name:  "ON DELETE NO ACTION",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON DELETE NO ACTION",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "NO ACTION",
				OnUpdate:            "",
				Deferrable:          false,
			},
		},
		{
			name:  "ON DELETE SET NULL",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON DELETE SET NULL",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "SET NULL",
				OnUpdate:            "",
				Deferrable:          false,
			},
		},
		{
			name:  "ON DELETE SET DEFAULT",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON DELETE SET DEFAULT",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "SET DEFAULT",
				OnUpdate:            "",
				Deferrable:          false,
			},
		},
		{
			name:  "ON UPDATE CASCADE",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON UPDATE CASCADE",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "",
				OnUpdate:            "CASCADE",
				Deferrable:          false,
			},
		},
		{
			name:  "ON UPDATE RESTRICT",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON UPDATE RESTRICT",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "",
				OnUpdate:            "RESTRICT",
				Deferrable:          false,
			},
		},
		{
			name:  "ON UPDATE NO ACTION",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON UPDATE NO ACTION",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "",
				OnUpdate:            "NO ACTION",
				Deferrable:          false,
			},
		},
		{
			name:  "ON UPDATE SET NULL",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON UPDATE SET NULL",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "",
				OnUpdate:            "SET NULL",
				Deferrable:          false,
			},
		},
		{
			name:  "ON UPDATE SET DEFAULT",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON UPDATE SET DEFAULT",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "",
				OnUpdate:            "SET DEFAULT",
				Deferrable:          false,
			},
		},
		{
			name:  "Combined ON DELETE and ON UPDATE",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON DELETE CASCADE ON UPDATE NO ACTION",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "CASCADE",
				OnUpdate:            "NO ACTION",
				Deferrable:          false,
			},
		},
		{
			name:  "Combined with DEFERRABLE",
			input: "FOREIGN KEY (col) REFERENCES tbl (ref) ON DELETE RESTRICT ON UPDATE SET DEFAULT DEFERRABLE",
			expected: &ForeignKeyConstraint{
				ColumnName:          "col",
				ReferenceTableName:  "tbl",
				ReferenceColumnName: "ref",
				OnDelete:            "RESTRICT",
				OnUpdate:            "SET DEFAULT",
				Deferrable:          true,
			},
		},
		{
			name:  "Case Insensitive and extra spaces",
			input: "foreign key (Col) references TBL (Ref) on delete set null on update cascade deferrable",
			expected: &ForeignKeyConstraint{
				ColumnName:          "Col",
				ReferenceTableName:  "TBL",
				ReferenceColumnName: "Ref",
				OnDelete:            "SET NULL",
				OnUpdate:            "CASCADE",
				Deferrable:          true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseForeignKeyConstraint(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("For input %q, expected %+v, got %+v", tt.input, tt.expected, result)
			}
		})
	}
}
