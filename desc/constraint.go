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
func (t *ConstraintType) Scan(src any) error {
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
	// TableName is the name of the table this constraint belongs to.
	TableName string
	// ColumnName is the name of the column this constraint applies to. For a plain
	// (non-unique) index row it is empty immediately after scanning (see ListConstraints),
	// but Build, which ListConstraints always calls right after scanning, fills it in
	// from the constraint definition for the common single-column index case; it stays
	// empty only when the definition doesn't match Build's simple single-column index
	// pattern (e.g. a composite/multi-column index).
	ColumnName string

	// ConstraintName is the name of the constraint, as reported by the database.
	ConstraintName string
	// ConstraintType classifies the constraint (primary key, unique, foreign key, check
	// or plain index) and determines which of Unique, Check and ForeignKey, if any, Build
	// populates.
	ConstraintType ConstraintType

	// IndexType is the access method (btree, gin, ...) backing the constraint's index,
	// when ConstraintType is IndexConstraintType.
	IndexType IndexType

	// Unique holds the parsed definition when ConstraintType is UniqueConstraintType.
	Unique *UniqueConstraint
	// Check holds the parsed definition when ConstraintType is CheckConstraintType.
	Check *CheckConstraint
	// ForeignKey holds the parsed definition when ConstraintType is ForeignKeyConstraintType.
	ForeignKey *ForeignKeyConstraint
	// Primary does not need it, as it's already described by table name and column name fields.

	// rawDefinition holds the original pg_get_constraintdef() output this Constraint was
	// built from (set by Build). It's kept around so String and BuildColumn can report the
	// offending SQL when the definition failed to parse into Unique/Check/ForeignKey (e.g.
	// composite/multi-column foreign keys, multiline CHECK expressions or schema-qualified
	// references) instead of dereferencing a nil sub-struct.
	rawDefinition string
}

// String implements the fmt.Stringer interface.
//
// If the constraint's definition could not be parsed into its typed sub-struct (Unique, Check
// or ForeignKey is nil: see Build/parseCheckConstraint/parseForeignKeyConstraint), String
// returns a placeholder built from the raw, unparsed definition instead of panicking.
func (c *Constraint) String() string {
	switch c.ConstraintType {
	case PrimaryKeyConstraintType:
		return fmt.Sprintf("PRIMARY KEY (%s)", c.ColumnName)
	case UniqueConstraintType:
		if c.Unique == nil || len(c.Unique.Columns) == 0 {
			return fmt.Sprintf("UNIQUE (%s)", c.ColumnName)
		}

		return fmt.Sprintf("UNIQUE (%s)", strings.Join(c.Unique.Columns, ", "))
	case CheckConstraintType:
		if c.Check == nil {
			return fmt.Sprintf("CHECK (<unparsed: %s>)", c.rawDefinition)
		}

		return fmt.Sprintf("CHECK (%s)", c.Check.Expression)
	case ForeignKeyConstraintType:
		if c.ForeignKey == nil {
			return fmt.Sprintf("FOREIGN KEY (%s) REFERENCES <unparsed: %s>", c.ColumnName, c.rawDefinition)
		}

		return fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s)", c.ColumnName, c.ForeignKey.ReferenceTableName, c.ForeignKey.ReferenceColumnName)
	case IndexConstraintType:
		return fmt.Sprintf("INDEX (%s)", c.ColumnName)
	}

	return ""
}

// Build implements the ColumnBuilder interface.
func (c *Constraint) Build(constraintDefinition string) {
	c.rawDefinition = constraintDefinition

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
//
// It returns a descriptive error (naming the constraint and its raw, unparsed definition)
// instead of panicking, when the constraint's definition could not be parsed into its typed
// sub-struct (Unique, Check or ForeignKey is nil). That can happen for constraint shapes the
// parsing regexes don't cover yet, e.g. composite/multi-column foreign keys, multiline CHECK
// expressions or schema-qualified references; see parseCheckConstraint and
// parseForeignKeyConstraint. Callers (e.g. DB.ListColumns) must check and propagate this error
// rather than discard it, since it can be reached at connect time via DB.CheckSchema.
func (c *Constraint) BuildColumn(column *Column) error {
	switch c.ConstraintType {
	case PrimaryKeyConstraintType:
		column.PrimaryKey = true
	case UniqueConstraintType:
		if c.Unique == nil {
			return fmt.Errorf("pg: constraint %q on %s.%s: unable to parse unique constraint definition: %s", c.ConstraintName, c.TableName, c.ColumnName, c.rawDefinition)
		}

		if len(c.Unique.Columns) == 0 || (len(c.Unique.Columns) == 1 && c.Unique.Columns[0] == c.ColumnName) {
			// PostgreSQL auto-generates constraint names as "{table}_{column}_key".
			// If the name differs, it was explicitly set via unique_index=name.
			autoName := fmt.Sprintf("%s_%s_key", c.TableName, c.ColumnName)
			if c.ConstraintName != "" && c.ConstraintName != autoName {
				column.UniqueIndex = c.ConstraintName
			} else {
				column.Unique = true
			}
		} else {
			column.UniqueIndex = c.ConstraintName
		}
	case CheckConstraintType:
		if c.Check == nil {
			return fmt.Errorf("pg: constraint %q on %s.%s: unable to parse check constraint definition: %s", c.ConstraintName, c.TableName, c.ColumnName, c.rawDefinition)
		}

		column.CheckConstraint = c.Check.Expression
	case ForeignKeyConstraintType:
		if c.ForeignKey == nil {
			return fmt.Errorf("pg: constraint %q on %s.%s: unable to parse foreign key constraint definition: %s", c.ConstraintName, c.TableName, c.ColumnName, c.rawDefinition)
		}

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
	// Columns holds the names of the columns the unique constraint applies to,
	// e.g. UNIQUE (title, source_url) or UNIQUE(name).
	// If length of this slice is one then this is a "unique" of its own column (unique=true),
	// otherwise is a multi column unique index e.g. "unique_index=uq_blog_posts".
	Columns []string
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

// CheckConstraint is a type that represents a check constraint.
type CheckConstraint struct {
	// Expression is the CHECK constraint's boolean SQL expression, with the outer
	// CHECK(...) wrapper stripped.
	Expression string
}

// parseCheckConstraint parses a check constraint definition.
func parseCheckConstraint(constraintDefinition string) *CheckConstraint {
	// Use a regex to extract the inner expression.
	// This regex will match a CHECK clause with one or two layers of parentheses.
	re := regexp.MustCompile(`(?i)^CHECK\s*\(\s*\(?\s*(.*\S)\s*\)?\s*\)$`)
	matches := re.FindStringSubmatch(constraintDefinition)
	if len(matches) < 2 {
		return nil
	}
	expression := matches[1]
	return &CheckConstraint{
		Expression: expression,
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
	// First, capture the mandatory part of the definition and the rest.
	// We don't rely on the order of the clauses, so we use a regex to find the base definition,
	// and then we'll look for the optional clauses.
	baseRegex := regexp.MustCompile(`(?i)^FOREIGN KEY\s*\((\w+)\)\s*REFERENCES\s*(\w+)\s*\((\w+)\)(.*)$`)
	matches := baseRegex.FindStringSubmatch(constraintDefinition)
	if len(matches) < 5 {
		return nil
	}

	columnName := matches[1]
	refTableName := matches[2]
	refColumnName := matches[3]
	rest := matches[4]

	onDelete := ""
	onUpdate := ""
	deferrable := false

	// Search for ON DELETE clause.
	deleteRegex := regexp.MustCompile(`(?i)ON DELETE\s+(CASCADE|RESTRICT|NO ACTION|SET NULL|SET DEFAULT)`)
	if m := deleteRegex.FindStringSubmatch(rest); m != nil {
		onDelete = strings.ToUpper(strings.TrimSpace(m[1]))
	}

	// Search for ON UPDATE clause.
	updateRegex := regexp.MustCompile(`(?i)ON UPDATE\s+(CASCADE|RESTRICT|NO ACTION|SET NULL|SET DEFAULT)`)
	if m := updateRegex.FindStringSubmatch(rest); m != nil {
		onUpdate = strings.ToUpper(strings.TrimSpace(m[1]))
	}

	// Search for DEFERRABLE keyword.
	if regexp.MustCompile(`(?i)\bDEFERRABLE\b`).FindString(rest) != "" {
		deferrable = true
	}

	return &ForeignKeyConstraint{
		ColumnName:          columnName,
		ReferenceTableName:  refTableName,
		ReferenceColumnName: refColumnName,
		OnDelete:            onDelete,
		OnUpdate:            onUpdate,
		Deferrable:          deferrable,
	}
}
