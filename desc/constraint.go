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

// ForeignKeyConstraint represents a foreign key definition for a column.
//
// A foreign key establishes a link between two tables based on a column or a set of columns.
// It may specify actions to be taken when the referenced row is deleted or updated,
// such as CASCADE, RESTRICT, NO ACTION, SET NULL, or SET DEFAULT.
// The constraint can also be marked as deferrable, meaning that its verification can be postponed until
// the end of a transaction rather than being checked immediately.
type ForeignKeyConstraint struct {
	ColumnName          string // The column that holds the foreign key.
	ReferenceTableName  string // The table that is referenced.
	ReferenceColumnName string // The column in the referenced table.
	OnDelete            string // Action to take when the referenced row is deleted.
	OnUpdate            string // Action to take when the referenced row is updated.
	Deferrable          bool   // Whether the constraint is deferrable.
}

// foreignKeyConstraintRegex is a compiled regular expression that matches PostgreSQL foreign key constraint definitions.
//
// The regex supports definitions with optional ON DELETE and ON UPDATE clauses, as well as an optional DEFERRABLE flag.
// It expects the definition to follow this format (case-insensitive):
//
//	FOREIGN KEY (column_name) REFERENCES table_name (reference_column)
//	  [ON DELETE {CASCADE | RESTRICT | NO ACTION | SET NULL | SET DEFAULT}]
//	  [ON UPDATE {CASCADE | RESTRICT | NO ACTION | SET NULL | SET DEFAULT}]
//	  [DEFERRABLE]
//
// The regular expression explicitly lists the allowed actions for the ON DELETE and ON UPDATE clauses.
var foreignKeyConstraintRegex = regexp.MustCompile(
	`(?i)^FOREIGN KEY\s*\((\w+)\)\s*REFERENCES\s*(\w+)\s*\((\w+)\)` +
		`(?:\s+ON DELETE\s+(CASCADE|RESTRICT|NO ACTION|SET NULL|SET DEFAULT))?` +
		`(?:\s+ON UPDATE\s+(CASCADE|RESTRICT|NO ACTION|SET NULL|SET DEFAULT))?` +
		`(?:\s+(DEFERRABLE))?$`)

// parseForeignKeyConstraint parses a foreign key constraint definition from a SQL statement.
//
// It extracts the column name, referenced table and column, as well as the optional ON DELETE and ON UPDATE actions,
// and checks whether the constraint is defined as deferrable.
//
// Parameters:
//   - constraintDefinition: A string containing the SQL foreign key constraint definition.
//
// Returns:
//   - A pointer to a ForeignKeyConstraint populated with the parsed values,
//     or nil if the constraintDefinition does not match the expected format.
func parseForeignKeyConstraint(constraintDefinition string) *ForeignKeyConstraint {
	// Attempt to match the provided constraint definition against the compiled regex.
	matches := foreignKeyConstraintRegex.FindStringSubmatch(constraintDefinition)
	// Verify that at least the first 4 groups are present: full match, column name, referenced table, and referenced column.
	if len(matches) < 4 {
		return nil
	}

	// Extract the necessary components from the regex match groups.
	columnName := matches[1]    // The foreign key column in the current table.
	refTableName := matches[2]  // The referenced table name.
	refColumnName := matches[3] // The referenced column in the referenced table.

	// Group 4: Optional ON DELETE action.
	onDelete := ""
	if len(matches) > 4 && matches[4] != "" {
		onDelete = strings.TrimSpace(matches[4])
	}

	// Group 5: Optional ON UPDATE action.
	onUpdate := ""
	if len(matches) > 5 && matches[5] != "" {
		onUpdate = strings.TrimSpace(matches[5])
	}

	// Group 6: Optional DEFERRABLE flag.
	deferrable := false
	if len(matches) > 6 && matches[6] != "" {
		// Use a case-insensitive comparison to check if the matched string is "DEFERRABLE"
		deferrable = strings.EqualFold(strings.TrimSpace(matches[6]), "DEFERRABLE")
	}

	// Return the parsed foreign key constraint.
	return &ForeignKeyConstraint{
		ColumnName:          columnName,
		ReferenceTableName:  refTableName,
		ReferenceColumnName: refColumnName,
		OnDelete:            onDelete,
		OnUpdate:            onUpdate,
		Deferrable:          deferrable,
	}
}
