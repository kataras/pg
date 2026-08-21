package pg

import (
	"context"
	"fmt"
	"strings"
)

// PageOptions describes LIMIT/OFFSET pagination and ordering for SelectPaginated.
type PageOptions struct {
	// Limit is the maximum number of rows per page; zero or negative adds no LIMIT.
	Limit int64
	// Offset is the number of rows to skip; zero or negative adds no OFFSET.
	Offset int64
	// OrderBy holds the ORDER BY contents (no "ORDER BY" keywords, e.g. `"id" DESC`). It is
	// interpolated directly into the query, NOT bound as a parameter: PostgreSQL does not allow
	// a bind parameter where an identifier is expected (see jackc/pgx#885), so a dynamic ORDER
	// BY has no parameterized form. OrderBy must therefore come from Repository.OrderBy (which
	// validates the caller's column against the table's own columns before quoting it) or a
	// trusted literal written by the caller, never directly from unvalidated user input, which
	// would otherwise open a SQL injection hole.
	OrderBy string
	// WithoutTotal skips the derived COUNT query; SelectPaginated then reports total -1.
	WithoutTotal bool
}

// SelectPaginated executes query (a SELECT without its own ORDER BY, LIMIT, OFFSET or a
// trailing semicolon), appending ORDER BY, LIMIT and OFFSET derived from page, and returns the
// resulting page of rows together with the total row count the unpaginated query would have
// produced. It is the one call meant to replace the count-then-list pagination boilerplate
// downstream consumers used to hand-assemble per endpoint.
//
// query is defensively trimmed of trailing whitespace and a trailing ";" before use, so a
// caller-supplied query ending in either (or both) does not produce invalid SQL once wrapped or
// extended below.
//
// Unless page.WithoutTotal is set, the total is obtained from a derived
// `SELECT COUNT(*) FROM (query) AS _pg_total` query run with the same args, via DB.Count (so a
// query that legitimately yields no rows at all (e.g. one with a GROUP BY over an empty result)
// still reports total 0 instead of surfacing ErrNoRows). A total of zero short-circuits:
// SelectPaginated returns (nil, 0, nil) immediately without running the page query at all, since
// it would necessarily return no rows either. When page.WithoutTotal is set, the COUNT query is
// skipped entirely, total is reported as -1, and the page query still runs.
//
// page.Limit and page.Offset, when positive, are appended as extra bind parameters (never
// interpolated); page.OrderBy, when non-empty, is interpolated: see PageOptions.OrderBy's doc
// for why, and for where it must come from. See buildPaginatedQuery for the exact assembly and
// bind-numbering rules.
//
// Row scanning for the page query is identical to Repository.Select (SelectPaginated delegates
// to it directly): rows are converted to []T via desc.Table.RowsToStruct against the repository's
// table descriptor.
func (repo *Repository[T]) SelectPaginated(ctx context.Context, page PageOptions, query string, args ...any) ([]T, int64, error) {
	query = trimQuery(query)

	total := int64(-1)
	if !page.WithoutTotal {
		var err error
		total, err = repo.db.Count(ctx, "SELECT COUNT(*) FROM ("+query+") AS _pg_total", args...)
		if err != nil {
			return nil, 0, err
		}
		if total == 0 {
			return nil, 0, nil
		}
	}

	pageQuery, pageArgs := buildPaginatedQuery(query, page, len(args)+1)

	allArgs := make([]any, 0, len(args)+len(pageArgs))
	allArgs = append(allArgs, args...)
	allArgs = append(allArgs, pageArgs...)

	items, err := repo.Select(ctx, pageQuery, allArgs...)
	if err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// SelectWithTotal executes a caller-authored query whose SELECT list includes a
// COUNT(*) OVER() window column named "total_count", scanning rows into T while capturing the
// total out-of-band via desc.Table.RowsToStructWithTotal, so T needs no artificial total-count field
// for it, unlike the fake struct fields tagged `presenter` this replaces.
//
// Unlike SelectPaginated, SelectWithTotal does not derive a separate COUNT query, does not
// append ORDER BY/LIMIT/OFFSET, and does not trim query: it runs query and args exactly as
// given, so the caller is responsible for the COUNT(*) OVER() AS total_count column and any
// ordering/pagination clauses it wants. Use it when the caller already has (or needs) full
// control over the query shape; use SelectPaginated when a plain SELECT plus PageOptions is
// enough.
//
// Zero rows returns (empty, 0, nil).
func (repo *Repository[T]) SelectWithTotal(ctx context.Context, query string, args ...any) ([]T, int64, error) {
	rows, err := repo.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}

	items, total, err := repo.td.RowsToStructWithTotal[T](rows, "total_count")
	if err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// trimQuery strips trailing whitespace and a trailing statement-terminating semicolon (in any
// combination and order, e.g. "SELECT 1", "SELECT 1;", "SELECT 1 ;  \n" all trim to "SELECT 1")
// from query. SelectPaginated and buildPaginatedQuery both rely on this so a caller-supplied
// query can be safely wrapped in a derived COUNT(*) subquery and/or have ORDER BY/LIMIT/OFFSET
// appended without producing invalid SQL such as "SELECT ...; ORDER BY ..." or
// "SELECT COUNT(*) FROM (SELECT ...;) AS _pg_total".
func trimQuery(query string) string {
	return strings.TrimRight(query, " \t\r\n;")
}

// buildPaginatedQuery appends ORDER BY, LIMIT and OFFSET clauses derived from page onto query,
// returning the assembled SQL together with the extra bind parameters (LIMIT's value, then
// OFFSET's, in that order, only for whichever of the two are actually appended)
// SelectPaginated must append to its own args before executing the result.
//
// query is trimmed via trimQuery first (see its doc), so a trailing semicolon or whitespace on
// an already-trimmed query is a harmless no-op here.
//
// page.OrderBy, when non-empty, is interpolated as-is (` ORDER BY <page.OrderBy>`): see
// PageOptions.OrderBy's doc for why it must never come from unvalidated user input. page.Limit
// and page.Offset are each appended only when strictly positive (` LIMIT $n` / ` OFFSET $n`,
// per PageOptions' zero-or-negative-means-omit contract) as new bind parameters, numbered
// consecutively starting at startIndex, which the caller sets to len(args)+1 so numbering
// continues correctly after whatever positional parameters (`$1`, `$2`, ...) its own query
// already uses.
func buildPaginatedQuery(query string, page PageOptions, startIndex int) (string, []any) {
	query = trimQuery(query)

	var extraArgs []any

	if page.OrderBy != "" {
		query += " ORDER BY " + page.OrderBy
	}

	if page.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", startIndex)
		extraArgs = append(extraArgs, page.Limit)
		startIndex++
	}

	if page.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", startIndex)
		extraArgs = append(extraArgs, page.Offset)
	}

	return query, extraArgs
}
