package desc

import (
	"fmt"
	"regexp"
	"strings"
)

// ConstraintType is a type that represents a constraint type.
type ConstraintType uint8

const (
	// NoneConstraintType is a constraint type that represents no constraint.
	NoneConstraintType ConstraintType = iota
	// PrimaryKeyConstraintType is a constraint type that represents a primary key constraint.
	PrimaryKeyConstraintType
	// UniqueConstraintType is a constraint type that represents a unique constraint.
	UniqueConstraintType
	// ForeignKeyConstraintType is a constraint type that represents a foreign key constraint.
	ForeignKeyConstraintType
	// CheckConstraintType is a constraint type that represents a check constraint.
	CheckConstraintType
	// IndexConstraintType is a constraint type that represents a simple index constraint.
	IndexConstraintType // A custom type to represent a simple index, see ListConstraints.
)

var textToConstraintType = map[string]ConstraintType{
	// constraint_type
	"PRIMARY KEY": PrimaryKeyConstraintType,
	"UNIQUE":      UniqueConstraintType,
	"CHECK":       CheckConstraintType,
	"FOREIGN KEY": ForeignKeyConstraintType,
	"INDEX":       IndexConstraintType,

	// contype
	"p": PrimaryKeyConstraintType,
	"u": UniqueConstraintType,
	"c": CheckConstraintType,
	"f": ForeignKeyConstraintType,
	"i": IndexConstraintType,
}

// Scan implements the sql.Scanner interface.
func (t *ConstraintType) Scan(src interface{}) error {
	switch v := src.(type) {
	case []byte:
		return t.Scan(string(v))
	case string:
		tt, ok := textToConstraintType[v]
		if !ok {
			return fmt.Errorf("constraint type: unknown value of: %#+v", v)
		}

		*t = tt
	default:
		return fmt.Errorf("constraint type: unknown type of: %T", v)
	}

	return nil
}

// Constraint is a type that represents a constraint.
type Constraint struct {
	TableName  string
	ColumnName string

	ConstraintName string
	ConstraintType ConstraintType

	IndexType IndexType

	Unique     *UniqueConstraint
	Check      *CheckConstraint
	ForeignKey *ForeignKeyConstraint
	// Primary does not need it, as it's already described by table name and column name fields.
}

// String implements the fmt.Stringer interface.
func (c *Constraint) String() string {
	switch c.ConstraintType {
	case PrimaryKeyConstraintType:
		return fmt.Sprintf("PRIMARY KEY (%s)", c.ColumnName)
	case UniqueConstraintType:
		if len(c.Unique.Columns) == 0 {
			return fmt.Sprintf("UNIQUE (%s)", c.ColumnName)
		}

		return fmt.Sprintf("UNIQUE (%s)", strings.Join(c.Unique.Columns, ", "))
	case CheckConstraintType:
		return fmt.Sprintf("CHECK (%s)", c.Check.Expression)
	case ForeignKeyConstraintType:
		return fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s)", c.ColumnName, c.ForeignKey.ReferenceTableName, c.ForeignKey.ReferenceColumnName)
	case IndexConstraintType:
		return fmt.Sprintf("INDEX (%s)", c.ColumnName)
	}

	return ""
}

// Build implements the ColumnBuilder interface.
func (c *Constraint) Build(constraintDefinition string) {
	switch c.ConstraintType {
	case UniqueConstraintType:
		c.Unique = parseUniqueConstraint(constraintDefinition)
	case CheckConstraintType: // no index type.
		c.Check = parseCheckConstraint(constraintDefinition)
	case ForeignKeyConstraintType: // no index type.
		c.ForeignKey = parseForeignKeyConstraint(constraintDefinition)
	case IndexConstraintType:
		_, _, columnName, indexType := parseSimpleIndexConstraint(constraintDefinition)
		c.ColumnName = columnName
		c.IndexType = indexType
	}
}

var _ ColumnBuilder = (*Constraint)(nil)

// BuildColumn implements the ColumnBuilder interface.
func (c *Constraint) BuildColumn(column *Column) error {
	if column.Index == InvalidIndex {
		column.Index = c.IndexType
	}

	switch c.ConstraintType {
	case PrimaryKeyConstraintType:
		column.PrimaryKey = true
	case UniqueConstraintType:
		if len(c.Unique.Columns) == 0 || (len(c.Unique.Columns) == 1 && c.Unique.Columns[0] == c.ColumnName) {
			// simple unique to itself.
			column.Unique = true
		} else {
			column.UniqueIndex = c.ConstraintName
		}
		// column.Unique = true
	case CheckConstraintType:
		column.CheckConstraint = c.Check.Expression
	case ForeignKeyConstraintType:
		column.ReferenceTableName = c.ForeignKey.ReferenceTableName
		column.ReferenceColumnName = c.ForeignKey.ReferenceColumnName
		column.ReferenceOnDelete = c.ForeignKey.OnDelete
		column.DeferrableReference = c.ForeignKey.Deferrable
	case IndexConstraintType:
		column.Index = c.IndexType
	}

	return nil
}

var simpleIndexRegex = regexp.MustCompile(`CREATE INDEX (\w+) ON \w+\.(\w+) USING (\w+) \((\w+)\)`)

// parseSimpleIndexConstraint parses a simple index constraint definition.
func parseSimpleIndexConstraint(constraintDefinition string) (indexName, tableName, columnName string, indexType IndexType) {
	// Define a regular expression that matches the input pattern
	// Find the submatches in the input
	matches := simpleIndexRegex.FindStringSubmatch(constraintDefinition)
	if len(matches) == 0 {
		return
	}

	indexName = matches[1]
	tableName = matches[2]
	indexType = parseIndexType(matches[3])
	columnName = matches[4]

	return
}

// UniqueConstraint is a type that represents a unique constraint.
type UniqueConstraint struct {
	Columns []string
	// e.g. UNIQUE (title, source_url) or UNIQUE(name),
	// If length of this slice is one then this is a "unique" of its own column (unique=true),
	// otherwise is a multi column unique index e.g. "unique_index=uq_blog_posts".
}

// parseUniqueConstraint parses a unique constraint definition.
func parseUniqueConstraint(constraintDefinition string) *UniqueConstraint {
	input := strings.TrimPrefix(constraintDefinition, "UNIQUE (")
	input = strings.TrimSuffix(input, ")")
	columns := strings.Split(input, ", ") // ["title", "source_url"] or ["name"]

	return &UniqueConstraint{
		Columns: columns,
	}
}

var uniqueIndexConstraintRegexp = regexp.MustCompile(`CREATE UNIQUE INDEX (?P<name>\w+) ON (?P<schema>\w+)\.(?P<table>\w+) USING (?P<method>\w+) \((?P<columns>.*)\)`)

func parseUniqueIndexConstraint(constraintDefinition string) []string {
	// Find the submatches in the sql string
	matches := uniqueIndexConstraintRegexp.FindStringSubmatch(constraintDefinition)
	// Get the names of the subexpressions
	names := uniqueIndexConstraintRegexp.SubexpNames()
	// Create a map to store the submatches by name
	result := make(map[string]string)
	for i, match := range matches {
		result[names[i]] = match
	}
	// Return the column names as a slice
	return strings.Split(result["columns"], ", ")
}

// CheckConstraint is a type that represents a check constraint.
type CheckConstraint struct {
	Expression string
}

// parseCheckConstraint parses a check constraint definition.
func parseCheckConstraint(constraintDefinition string) *CheckConstraint {
	input := strings.TrimPrefix(constraintDefinition, "CHECK ((")
	input = strings.TrimSuffix(input, "))")

	return &CheckConstraint{
		Expression: input,
	}
}

// ForeignKeyConstraint is a type that represents a foreign key definition for a column.
// A foreign key is a constraint that establishes a link between two tables based on a column or a set of columns.
// A foreign key can have different options for handling the deletion or update of the referenced row,
// such as cascade, restrict, set null, etc. A foreign key can also be deferrable, meaning that the constraint
// can be checked at the end of a transaction instead of immediately.
type ForeignKeyConstraint struct {
	// the name of the column that references another table,
	// this is the same as the column name of the Constraint type but
	// it's here because we use this type internally and solo as well.
	ColumnName string

	ReferenceTableName  string // the name of the table that is referenced by the foreign key
	ReferenceColumnName string // the name of the column that is referenced by the foreign key
	OnDelete            string // the action to take when the referenced row is deleted
	Deferrable          bool   // whether the foreign key constraint is deferrable or not
}

// Compile the regular expression pattern into a regular expression instance
var foreignKeyConstraintRegex = regexp.MustCompile(`^FOREIGN KEY\s*\((\w+)\)\s*REFERENCES\s*(\w+)\((\w+)\)(?:\s*ON DELETE\s*([A-Za-z]+\s*[A-Za-z]*))?(?:\s*(DEFERRABLE))?$`)

// parseForeignKeyConstraint parses a foreign key constraint definition.
func parseForeignKeyConstraint(constraintDefinition string) *ForeignKeyConstraint {
	// Use the regular expression instance to match against the SQL result line and extract the relevant parts
	matches := foreignKeyConstraintRegex.FindStringSubmatch(constraintDefinition)
	// If there are less than 4 matches, skip this row as it is not a valid foreign key definition
	if len(matches) < 4 { // At least 4 groups are expected: full match, column, table, ref column
		return nil
	}

	// Assign each match to a variable with a descriptive name
	var (
		columnName    = matches[1] // the column name that references another table
		refTableName  = matches[2] // the referenced table name
		refColumnName = matches[3] // the referenced column name
		onDelete      string       // the action to take on delete (optional)
		deferrable    bool         // whether the constraint is deferrable or not (optional)
	)

	// If there is a fifth match and it is not empty, assign it to onDelete
	if len(matches) > 4 && matches[4] != "" {
		onDelete = strings.TrimSpace(matches[4]) // Trim any extra spaces
	}

	// If there is a sixth match and it is not empty, assign it to deferrable
	if len(matches) > 5 && matches[5] != "" {
		deferrable = strings.TrimSpace(matches[5]) == "DEFERRABLE"
	}

	return &ForeignKeyConstraint{
		ColumnName:          columnName,
		ReferenceTableName:  refTableName,
		ReferenceColumnName: refColumnName,
		OnDelete:            onDelete,
		Deferrable:          deferrable,
	}
}
