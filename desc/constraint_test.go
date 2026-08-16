package desc

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseForeignKeyConstraint tests the parseForeignKeyConstraint function with a variety
// of PostgreSQL foreign key definitions, ensuring that all supported actions (CASCADE, RESTRICT,
// NO ACTION, SET NULL, SET DEFAULT) for ON DELETE and ON UPDATE clauses, as well as the DEFERRABLE
// flag, are parsed correctly.
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

// TestConstraintBuildColumnUnparseableDefinition verifies that constraint definitions the
// regex-based parsers can't understand, a composite/multi-column foreign key or a multiline
// CHECK expression, leave Constraint.Check / Constraint.ForeignKey nil (as documented on
// parseCheckConstraint / parseForeignKeyConstraint) and that BuildColumn reports this as a
// descriptive error instead of dereferencing the nil sub-struct and panicking. This is the path
// reachable from DB.ListColumns / DB.CheckSchema at connect time (see task B6).
func TestConstraintBuildColumnUnparseableDefinition(t *testing.T) {
	t.Run("composite foreign key", func(t *testing.T) {
		rawDefinition := "FOREIGN KEY (a, b) REFERENCES other(x, y)"

		c := &Constraint{
			TableName:      "orders",
			ColumnName:     "a",
			ConstraintName: "orders_a_b_fkey",
			ConstraintType: ForeignKeyConstraintType,
		}
		c.Build(rawDefinition)

		if c.ForeignKey != nil {
			t.Fatalf("expected parseForeignKeyConstraint to fail on a composite FK, got: %+v", c.ForeignKey)
		}

		var column Column
		err := c.BuildColumn(&column)
		if err == nil {
			t.Fatal("expected BuildColumn to return an error for an unparsed foreign key, got nil")
		}

		for _, want := range []string{c.ConstraintName, rawDefinition} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("expected error to mention %q, got: %v", want, err)
			}
		}
	})

	t.Run("multiline check", func(t *testing.T) {
		rawDefinition := "CHECK ((a > 0)\nAND (b < 0))"

		c := &Constraint{
			TableName:      "orders",
			ColumnName:     "a",
			ConstraintName: "orders_a_check",
			ConstraintType: CheckConstraintType,
		}
		c.Build(rawDefinition)

		if c.Check != nil {
			t.Fatalf("expected parseCheckConstraint to fail on a multiline CHECK, got: %+v", c.Check)
		}

		var column Column
		err := c.BuildColumn(&column)
		if err == nil {
			t.Fatal("expected BuildColumn to return an error for an unparsed check constraint, got nil")
		}

		for _, want := range []string{c.ConstraintName, rawDefinition} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("expected error to mention %q, got: %v", want, err)
			}
		}
	})
}

// TestConstraintStringNilSubStruct verifies Constraint.String() never panics when the
// constraint's typed sub-struct (Check, ForeignKey or Unique) is nil, the state a Constraint is
// left in when Build couldn't parse its raw definition (see
// TestConstraintBuildColumnUnparseableDefinition), and instead returns a placeholder mentioning
// the raw definition.
func TestConstraintStringNilSubStruct(t *testing.T) {
	tests := []struct {
		name       string
		constraint *Constraint
	}{
		{
			name: "nil Check",
			constraint: &Constraint{
				ColumnName:     "a",
				ConstraintType: CheckConstraintType,
				Check:          nil,
				rawDefinition:  "CHECK ((a > 0)\nAND (b < 0))",
			},
		},
		{
			name: "nil ForeignKey",
			constraint: &Constraint{
				ColumnName:     "a",
				ConstraintType: ForeignKeyConstraintType,
				ForeignKey:     nil,
				rawDefinition:  "FOREIGN KEY (a, b) REFERENCES other(x, y)",
			},
		},
		{
			name: "nil Unique",
			constraint: &Constraint{
				ColumnName:     "a",
				ConstraintType: UniqueConstraintType,
				Unique:         nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Constraint.String() panicked: %v", r)
				}
			}()

			got := tt.constraint.String()
			if got == "" {
				t.Fatal("expected a non-empty placeholder string, got empty string")
			}
		})
	}
}
