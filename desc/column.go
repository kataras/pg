package desc

import (
	"fmt"
	"io"
	"reflect"
	"strings"
)

type (
	// ColumnBuilder is an interface that is used to build a column definition.
	ColumnBuilder interface {
		BuildColumn(*Column) error
	}

	// Column is a type that represents a column definition for the database.
	Column struct {
		Table            *Table    // the parent table reference.
		TableName        string    // the name of the table this column lives at.
		TableDescription string    // the description of the table this column lives at.
		TableType        TableType // the type of the table this column lives at.

		Name            string       // the name of the column
		Type            DataType     // the data type of the column
		Description     string       // the description of the column
		OrdinalPosition int          // the position (starting from 1) of the corresponding column in the table.
		FieldIndex      []int        // the index of the corresponding struct field
		FieldType       reflect.Type // the reflect.Type of the corresponding struct field
		isPtr           bool         // reprots whether FieldType.Kind() == reflect.Ptr.
		/* if nil then wasn't able to resolve it by builtin method */
		FieldName    string // the name of the corresponding struct field
		TypeArgument string // an optional argument for the data type, e.g. 255 when Type is "varchar"
		PrimaryKey   bool   // a flag that indicates if the column is a primary key
		Identity     bool   // a flag that indicates if the column is an identity column, e.g. INT GENERATED ALWAYS AS IDENTITY
		// Required        bool         // a flag that indicates if the column is required (not null, let's just use the !Nullable)
		Default         string // an optional default value or sql function for the column
		CheckConstraint string // an optional check constraint for the column
		Unique          bool   // a flag that indicates if the column has a unique constraint (postgres automatically adds an index for that single one)
		// As tested, the unique=true and a single column of unique_index is the same result in the database on table creation,
		// note that because the generator does store unique_index instead of simple unique on Go generated source files.
		Conflict string // an optional conflict action for the unique constraint, e.g do nothing

		// If true this and password field is used to SelectByUsernameAndPassword repository method.
		Username bool
		// If true Postgres handles password encryption (on inserts) and decryption (on selects),
		// note that you MUST set the Schema.HandlePassword in order for this to work by both ways.
		// A flag that indicates if the column is a password column.
		Password bool
		// If true it's a shorthand of default="null".
		Nullable            bool   // a flag that indicates if the column is nullable
		ReferenceTableName  string // an optional reference table name for a foreign key constraint, e.g. user_profiles(id) -> user_profiles
		ReferenceColumnName string // an optional reference column name for a foreign key constraint, e.g. user_profiles(id) -> id
		DeferrableReference bool   // a flag that indicates if the foreign key constraint is deferrable (omits foreign key checks on transactions)
		ReferenceOnDelete   string // an optional action for deleting referenced rows when referencing rows are deleted, e.g. NO ACTION, RESTRICT, CASCADE, SET NULL and SET DEFAULT. Defaults to CASCADE.

		Index IndexType // an optional index type for the column

		// Unique indexes can really improve the performance on big data select queries
		// Read more at: https://www.postgresql.org/docs/current/indexes-unique.html
		UniqueIndex string // an optional name for a unique index on the column
		// If true then create table, insert, update and duplicate queries will omit this column.
		Presenter bool
		// If true then insert query will omit this column.
		AutoGenerated bool
		// If true then this column->struct value is skipped from the Select queries
		Unscannable bool // a flag that indicates if the column is unscannable

		// If true  then this column-> struct field type is already implements a scanner interface for the table.
		isScanner bool
	}
)

// IsGeneratedTimestamp returns true if the column is a timestamp column and
// has a default value of "clock_timestamp()" or "now()".
func (c *Column) IsGeneratedTimestamp() bool {
	if c.Type.IsTime() {
		defaultValue := strings.ToLower(c.Default)
		return (defaultValue == "clock_timestamp()" || defaultValue == "now()")
	}

	return false
}

// IsGeneratedPrimaryUUID returns true if the column is a primary UUID column and
// has a default value of "gen_random_uuid()" or "uuid_generate_v4()".
func (c *Column) IsGeneratedPrimaryUUID() bool {
	return c.PrimaryKey && !c.Nullable && c.Type == UUID &&
		(c.Default == genRandomUUIDPGCryptoFunction1 || c.Default == genRandomUUIDPGCryptoFunction2)
}

// IsGenerated returns true if the column is a generated column.
func (c *Column) IsGenerated() bool {
	return c.IsGeneratedPrimaryUUID() || c.IsGeneratedTimestamp()
}

//nolint:all
func writeTagProp(w io.StringWriter, key string, value any) {
	if key == "" {
		return
	}

	if value == nil {
		w.WriteString(key)
		return
	}

	if isZero(value) {
		return // don't write if arg value is empty, e.g. "".
	}

	// if  key[len(key)-1] != '=' {
	if !strings.Contains(key, "%") {
		// is probably just a boolean (which we don't need to declare its value if true).
		w.WriteString(key)
		return
	}

	if b, ok := value.(bool); ok && b {
		w.WriteString(key)
		return
	}

	_, _ = w.WriteString(fmt.Sprintf(key, value))
}

// FieldTagString returns a string representation of the struct field tag for the column.
func (c *Column) FieldTagString(strict bool) string {
	b := new(strings.Builder)
	b.WriteString(DefaultTag)
	b.WriteString(`:"`)

	writeTagProp(b, "name=%s", c.Name)
	writeTagProp(b, ",type=%s", c.Type.String())
	if (c.Table != nil && c.Table.Type.IsReadOnly()) || c.TableType.IsReadOnly() {
		// If it's a view then don't write the rest of the tags, we only care for name and type.
		b.WriteString(`"`)
		return b.String()
	}

	if strict {
		writeTagProp(b, "(%s)", c.TypeArgument)
	}

	writeTagProp(b, ",primary", c.PrimaryKey)
	writeTagProp(b, ",identity", c.Identity)
	//writeTagProp(b, "", c.Required)
	if c.Nullable {
		// writeTagProp(b, ",default=%s", nullLiteral)
		writeTagProp(b, ",nullable", true)
	} else {
		defaultValue := c.Default
		if !strict {
			// E.g. {}::integer[], we need to cut the ::integer[] part as it's so strict.
			// Cut {}::integer[] the :: part.
			if names, ok := dataTypeText[c.Type]; ok {
				for _, name := range names {
					defaultValue = strings.TrimSuffix(defaultValue, "::"+name)
				}
			}
		}

		writeTagProp(b, ",default=%s", defaultValue)
	}

	writeTagProp(b, ",unique", c.Unique)
	writeTagProp(b, ",conflict=%s", c.Conflict)
	if strict {
		writeTagProp(b, ",username", c.Username)
		writeTagProp(b, ",password", c.Password)
	}

	if tb := c.ReferenceTableName; tb != "" {
		// write the ref line.
		writeTagProp(b, ",ref=%s", tb)

		if rc := c.ReferenceColumnName; rc != "" {
			writeTagProp(b, "(%s", rc)

			if c.ReferenceOnDelete != "" {
				writeTagProp(b, " "+c.ReferenceOnDelete, nil)
			}

			writeTagProp(b, " deferrable", c.DeferrableReference)
			writeTagProp(b, ")", nil)
		}
	}

	if c.Index != InvalidIndex {
		writeTagProp(b, ",index=%s", c.Index.String())
	}

	writeTagProp(b, ",unique_index=%s", c.UniqueIndex)
	writeTagProp(b, ",check=%s", c.CheckConstraint)
	if strict {
		writeTagProp(b, ",auto", c.AutoGenerated)
		writeTagProp(b, ",presenter", c.Presenter)
		writeTagProp(b, ",unscannable", c.Unscannable)
	}

	b.WriteString(`"`)
	return b.String()
}
