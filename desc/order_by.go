package desc

import (
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// OrderBy validates a user-supplied sort column against the table's own columns plus
// extraColumns (for computed or aliased columns that have no *Column entry, e.g. an
// expression exposed under an alias in the SELECT list) and returns a quoted
// `"column" ASC|DESC` fragment that is safe to splice directly into an ORDER BY clause.
//
// Dynamic ORDER BY cannot be a bind parameter. PostgreSQL only accepts $N placeholders
// where a value is expected, not where an identifier is (see jackc/pgx#885), so a caller
// that lets a user pick the sort column has no way to parameterize it. The safe alternative
// is to validate the column against an allowlist and then quote it, never to concatenate the
// raw input into SQL; OrderBy does exactly that in one call, so callers stop hand-maintaining
// per-endpoint allowlist globals for it.
//
// column is matched against td's columns case-insensitively, the same rule GetColumnByName
// uses, or by exact (case-sensitive) membership in extraColumns. On a match against a table
// column the fragment quotes the descriptor's CANONICAL column name (td's own casing), not
// whatever casing the caller passed in; on a match against extraColumns it quotes the
// caller-supplied name as given, since there is no descriptor entry to canonicalize against.
// Quoting is done with pgx.Identifier.Sanitize, which double-quotes the identifier and
// doubles any embedded `"`. An unrecognized column returns a descriptive error naming just
// the offending column, not the full allowlist, so the error is safe to surface to a client
// without leaking the table's column names.
//
// Every entry in extraColumns must be a bare, unquoted identifier: the same shape
// validateIdentifier already enforces for every table, column and unique index name resolved
// by ConvertStructToTable (letters/digits/underscore/dollar, starting with a letter or
// underscore; no dots, no whitespace, not empty). OrderBy checks all of them up front, before
// looking at column, and returns a descriptive error naming the first offending entry if any
// fails, regardless of whether that particular entry ends up matching column. Without this
// check a schema-qualified or space-containing entry would still get accepted and quoted as
// one bogus identifier by pgx.Identifier.Sanitize (e.g. "t.name" becomes the single quoted
// identifier "t.name", not the table-qualified column t."name"), surfacing later as an opaque
// "column does not exist" error from PostgreSQL instead of a clear one from OrderBy itself.
//
// An empty column does not error; it falls back, in order, to a column named "created_at",
// then to one named "updated_at", then to the table's primary key column. If the table has
// none of those three, OrderBy returns an error rather than guessing a column to sort by.
//
// descending selects the trailing " DESC" (true) or " ASC" (false) on the returned fragment.
func (td *Table) OrderBy(column string, descending bool, extraColumns ...string) (string, error) {
	for _, extra := range extraColumns {
		if err := validateIdentifier(extra); err != nil {
			return "", fmt.Errorf("pg: order by: invalid extraColumns entry: %w", err)
		}
	}

	direction := "ASC"
	if descending {
		direction = "DESC"
	}

	if column == "" {
		for _, fallback := range [...]string{"created_at", "updated_at"} {
			if c := td.GetColumnByName(fallback); c != nil {
				return pgx.Identifier{c.Name}.Sanitize() + " " + direction, nil
			}
		}

		if pk, ok := td.PrimaryKey(); ok {
			return pgx.Identifier{pk.Name}.Sanitize() + " " + direction, nil
		}

		return "", fmt.Errorf("pg: order by: table %q has no created_at, updated_at or primary key column to fall back to", td.Name)
	}

	if c := td.GetColumnByName(column); c != nil {
		return pgx.Identifier{c.Name}.Sanitize() + " " + direction, nil
	}

	if slices.Contains(extraColumns, column) {
		return pgx.Identifier{column}.Sanitize() + " " + direction, nil
	}

	return "", fmt.Errorf("pg: order by: unknown column %q for table %q", column, td.Name)
}
