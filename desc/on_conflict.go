package desc

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// OnConflict describes an INSERT ... ON CONFLICT clause: the conflict target (Columns, or a
// named Constraint) and the action to take on a conflicting row: DO NOTHING, or DO UPDATE SET
// over SetColumns with an optional SetWhere predicate.
//
// A zero-value OnConflict{} behaves like Upsert: the conflict target falls back to the struct's
// unique/unique_index tag derivation, and (unless DoNothing is set) every inserted column
// outside that target is updated from EXCLUDED.
type OnConflict struct {
	// Columns is the conflict-target column list, e.g. []string{"a", "b"} for
	// ON CONFLICT (a,b). Each name is validated against the table (case-insensitively, like
	// Table.GetColumnByName) and emitted quoted via pgx.Identifier.Sanitize. Empty (with an
	// empty Constraint) falls back to the struct's unique/unique_index tag derivation, exactly
	// as BuildInsertQuery/BuildBulkInsertQuery derive it for Upsert, including silently
	// producing a plain insert (no ON CONFLICT clause at all) if the struct declares no such
	// tag, so a real duplicate raises the database's own unique-violation error.
	Columns []string
	// Constraint targets ON CONSTRAINT <name> instead of a column list; mutually exclusive with
	// Columns (BuildInsertQueryOnConflict/BuildBulkInsertQueryOnConflict return an error if both
	// are set). It is quoted via pgx.Identifier.Sanitize but not otherwise validated. Table
	// descriptors don't track constraint names.
	Constraint string
	// DoNothing emits DO NOTHING instead of DO UPDATE. It cannot be combined with SetColumns or
	// SetWhere (both imply a DO UPDATE). Doing so is a validation error.
	DoNothing bool
	// SetColumns lists the columns updated as col = EXCLUDED.col on a DO UPDATE, in the given
	// order. Each name is validated and quoted the same way Columns is. Empty means every
	// inserted column outside the conflict target is updated (the current Upsert behavior).
	SetColumns []string
	// SetWhere is an optional raw SQL predicate appended as " WHERE <SetWhere>" after the DO
	// UPDATE SET list, and may reference EXCLUDED.* and the table's own columns (e.g. to only
	// overwrite a row that is actually older: "t.updated_at < EXCLUDED.updated_at"). It is
	// developer-authored SQL, written verbatim with no escaping. Never build it from
	// end-user input. Combining it with DoNothing is a validation error.
	SetWhere string
}

// BuildInsertQueryOnConflict is BuildInsertQuery with an explicit OnConflict specification
// instead of the tag-driven forceOnConflictExpr/upsert behavior: oc.Columns or oc.Constraint
// (or, if both are empty, the struct's tag-derived unique/unique_index target) becomes the ON
// CONFLICT target, and oc.DoNothing / oc.SetColumns / oc.SetWhere control the DO NOTHING / DO
// UPDATE SET action.
//
// Unlike BuildInsertQuery, which only appends RETURNING for a DO UPDATE action,
// BuildInsertQueryOnConflict always appends RETURNING <primary key> when idPtr is non-nil and
// there is a conflict target: a DO NOTHING that skips the row then yields zero returned rows,
// which callers such as Repository.InsertSingleOnConflict surface as ErrNoRows instead of a
// stale idPtr.
func BuildInsertQueryOnConflict(td *Table, structValue reflect.Value, idPtr any, oc OnConflict) (string, []any, error) {
	returningColumn := "" // a variable to store the name of the column to return after insertion
	if idPtr != nil {
		// if idPtr is not nil, it means we want to get the primary key value of the inserted row
		columnDefinition, ok := td.PrimaryKey() // get the primary key column definition from the table definition
		if ok {
			returningColumn = columnDefinition.Name // assign the column name to returningColumn
		}
	}

	// find the arguments for the SQL query based on the struct value and the table definition
	args, err := extractArguments(td, structValue, nil)
	if err != nil {
		return "", nil, err // return the error if finding arguments fails
	}

	if len(args) == 0 {
		return "", nil, fmt.Errorf(`no arguments found, maybe missing struct field tag of "%s"`, DefaultTag)
	}

	var b strings.Builder

	// INSERT INTO "schema"."tableName"
	b.WriteString(`INSERT INTO`)
	b.WriteByte(' ')
	writeTableName(&b, td.SearchPath, td.Name)
	b.WriteByte(' ')

	// (col1,col2,...)
	namedParametersValues, insertCols, err := writeInsertColumnList(&b, td, args)
	if err != nil {
		return "", nil, err
	}

	if len(namedParametersValues) == 0 {
		return "", nil, fmt.Errorf("no columns to insert")
	}

	// VALUES($1,$2,...)
	b.WriteByte(' ')
	b.WriteString(`VALUES`)
	b.WriteByte(leftParenLiteral)
	b.WriteString(strings.Join(namedParametersValues, ","))
	b.WriteByte(rightParenLiteral)

	hasTarget, targetSQL, action, err := resolveOnConflict(td, insertCols, oc)
	if err != nil {
		return "", nil, err
	}

	writeOnConflictClause(&b, hasTarget, targetSQL, action, returningColumn, true)

	b.WriteByte(';')

	return b.String(), args.Values(), nil
}

// BuildBulkInsertQueryOnConflict is BuildBulkInsertQuery with an explicit OnConflict
// specification instead of the tag-driven forceOnConflictExpr/upsert behavior. Column selection,
// DEFAULT-vs-placeholder emission and password handling are identical to BuildBulkInsertQuery;
// only ON CONFLICT derivation differs, via the same oc.Columns/oc.Constraint/oc.DoNothing/
// oc.SetColumns/oc.SetWhere rules BuildInsertQueryOnConflict uses. Like BuildBulkInsertQuery, it
// never appends RETURNING. Pair it with Repository.InsertOnConflict, not a variant that scans a
// primary key back.
func BuildBulkInsertQueryOnConflict(td *Table, structValues []reflect.Value, oc OnConflict) (string, []any, error) {
	if len(structValues) == 0 {
		return "", nil, fmt.Errorf("no values to insert")
	}

	// Collect the column set: every non-AutoGenerated, non-Presenter column
	// in declaration order. Passwords are kept: they get special handling
	// per-row below.
	cols := make([]*Column, 0, len(td.Columns))
	for _, c := range td.Columns {
		if c.AutoGenerated || c.Presenter {
			continue
		}
		cols = append(cols, c)
	}
	if len(cols) == 0 {
		return "", nil, fmt.Errorf(`no columns to insert, maybe missing struct field tag of "%s"`, DefaultTag)
	}

	hasTarget, targetSQL, action, err := resolveOnConflict(td, cols, oc)
	if err != nil {
		return "", nil, err
	}

	var b strings.Builder
	b.WriteString(`INSERT INTO`)
	b.WriteByte(' ')
	writeTableName(&b, td.SearchPath, td.Name)
	b.WriteByte(' ')

	args, err := writeBulkInsertHeaderAndValues(&b, td, cols, structValues)
	if err != nil {
		return "", nil, err
	}

	writeOnConflictClause(&b, hasTarget, targetSQL, action, "", false)

	b.WriteByte(';')
	return b.String(), args, nil
}

// resolveOnConflict validates oc against td and derives the SQL fragments writeOnConflictClause
// needs:
//
//   - hasTarget/targetSQL describe the conflict target: an explicit oc.Columns
//     ("(\"col1\",\"col2\")"), an explicit oc.Constraint (" ON CONSTRAINT \"name\""), or, when
//     both are empty, the tag-derived target Upsert would use (see tagConflictColumns), which
//     may turn out to be no target at all (hasTarget=false), matching BuildInsertQuery's
//     historical behavior of silently falling back to a plain insert in that case.
//   - action is either "DO NOTHING" or "DO UPDATE SET ... [WHERE ...]".
//
// fallbackCols is every column this INSERT would write, in order. It is used both for the
// tag-derived target fallback and, when oc.SetColumns is empty, to build the "every inserted
// column outside the conflict target" DO UPDATE SET list (mirroring Upsert's default behavior,
// but with quoted identifiers).
func resolveOnConflict(td *Table, fallbackCols []*Column, oc OnConflict) (hasTarget bool, targetSQL, action string, err error) {
	if len(oc.Columns) > 0 && oc.Constraint != "" {
		return false, "", "", fmt.Errorf("desc: on conflict: Columns and Constraint are mutually exclusive")
	}
	if oc.DoNothing && (len(oc.SetColumns) > 0 || oc.SetWhere != "") {
		return false, "", "", fmt.Errorf("desc: on conflict: DoNothing cannot be combined with SetColumns or SetWhere")
	}

	// targetNames, when non-nil, is the unquoted set of column names in the conflict target. It is
	// used below to exclude the target from the "every other inserted column" DO UPDATE SET
	// fallback. It is only populated for a column-list target (explicit or tag-derived): a named
	// Constraint target doesn't reveal which columns it covers to this package.
	var targetNames []string

	switch {
	case len(oc.Columns) > 0:
		quoted := make([]string, len(oc.Columns))
		for i, name := range oc.Columns {
			c := td.GetColumnByName(name)
			if c == nil {
				return false, "", "", fmt.Errorf("desc: on conflict: unknown column %q for table %q", name, td.Name)
			}
			quoted[i] = pgx.Identifier{c.Name}.Sanitize()
			targetNames = append(targetNames, c.Name)
		}
		hasTarget = true
		targetSQL = "(" + strings.Join(quoted, ",") + ")"
	case oc.Constraint != "":
		hasTarget = true
		targetSQL = " ON CONSTRAINT " + pgx.Identifier{oc.Constraint}.Sanitize()
	default:
		if conflicts := tagConflictColumns(td, fallbackCols); len(conflicts) > 0 {
			hasTarget = true
			targetSQL = "(" + strings.Join(conflicts, ",") + ")"
			targetNames = conflicts
		}
	}

	if oc.DoNothing {
		return hasTarget, targetSQL, `DO NOTHING`, nil
	}

	var setIdentifiers []string
	if len(oc.SetColumns) > 0 {
		setIdentifiers = make([]string, len(oc.SetColumns))
		for i, name := range oc.SetColumns {
			c := td.GetColumnByName(name)
			if c == nil {
				return false, "", "", fmt.Errorf("desc: on conflict: unknown column %q for table %q", name, td.Name)
			}
			setIdentifiers[i] = pgx.Identifier{c.Name}.Sanitize()
		}
	} else {
		for _, c := range fallbackCols {
			if slices.Contains(targetNames, c.Name) {
				continue
			}
			setIdentifiers = append(setIdentifiers, pgx.Identifier{c.Name}.Sanitize())
		}
	}

	action = buildDoUpdateFromExcludedSubset(setIdentifiers)
	if oc.SetWhere != "" {
		action += " WHERE " + oc.SetWhere
	}

	return hasTarget, targetSQL, action, nil
}

// tagConflictColumns returns the ON CONFLICT target column names implied by cols' Unique/
// UniqueIndex tags, exactly as the tag-driven builders derive a target for Upsert: when the
// table has a `conflict` tag anywhere (td.OnConflict()'s hasConflict), every Unique column in
// cols; otherwise every column in cols with a non-empty UniqueIndex. Order matches cols.
//
// This is the target-only half of tagConflictTarget (insert_query.go): it deliberately ignores
// forceOnConflictExpr (OnConflict has no equivalent parameter) and the tag-derived action itself
// (OnConflict.DoNothing/SetColumns/SetWhere control the action instead).
func tagConflictColumns(td *Table, cols []*Column) []string {
	_, hasConflict := td.OnConflict()

	var conflicts []string
	for _, c := range cols {
		if hasConflict {
			if c.Unique {
				conflicts = append(conflicts, c.Name)
			}
		} else if c.UniqueIndex != "" {
			conflicts = append(conflicts, c.Name)
		}
	}

	return conflicts
}
