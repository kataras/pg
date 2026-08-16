package pg

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// placeholderPattern matches a PostgreSQL bind placeholder such as $1 or $10 inside a raw SQL
// fragment. \d+ is greedy, so $10 is captured whole ("10"), never as $1 followed by a literal
// "0": the boundary that makes automatic renumbering safe past nine parameters.
var placeholderPattern = regexp.MustCompile(`\$(\d+)`)

// conditionsElemTypePattern constrains the elemType argument accepted by AndAnyOf and
// AndMatchAnyOf: a bare or space-separated PostgreSQL type name (e.g. "smallint", "double
// precision", "varchar"), with no punctuation that could break out of the `$1::<elemType>[]`
// cast it is spliced into.
var conditionsElemTypePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_ ]*$`)

// conditionFragment is one AND-joined piece of a Conditions clause: raw SQL, still using
// call-local $1..$n placeholders, together with the args those placeholders refer to.
type conditionFragment struct {
	sql  string
	args []any
}

// Conditions builds a WHERE clause from raw SQL fragments whose bind parameters are
// renumbered automatically, so one filter set can drive both a page query and its COUNT twin
// without hand-renumbering `$N` a second time. Inside each fragment passed to Where or one of
// the And* methods, $1..$n refer to that call's own args (and may repeat, e.g. an array
// filter that casts, null-checks and matches the same parameter three times), and Build
// rewrites them to consecutive global positions when it renders the final clause.
//
// Conditions is deliberately SQL-transparent: it does not parse or understand the fragments it
// is given, it only renumbers their placeholders and joins them with AND. Column and type
// strings passed to the helper methods below (column, elemType, match, matchExpr, tsvExpr,
// textExpr) are developer-authored SQL literals, not sanitized in any way. Never pass user
// input through them; user input belongs in the args that go alongside a fragment (or, for a
// dynamic sort column, in Repository.OrderBy / desc.Table.OrderBy instead).
//
// The zero value is not ready to use; construct a Conditions with Where. Methods are not
// nil-safe on a nil *Conditions.
type Conditions struct {
	fragments []conditionFragment
}

// Where starts a Conditions builder with an optional first fragment; fragment == "" starts an
// empty builder with nothing appended, so a caller building a filter set conditionally can
// always start from Where("") and grow it with And/And* calls. Args are interpreted the same
// way as And's: $1..$n inside fragment refer to args[0]..args[n-1].
func Where(fragment string, args ...any) *Conditions {
	c := &Conditions{}
	return c.And(fragment, args...)
}

// And appends a fragment joined with AND to the ones already in c. Inside fragment, $k refers
// to args[k-1] and may appear more than once; Build later renumbers $k to a single global
// position shared by every occurrence, so the underlying arg is not duplicated. An empty
// fragment is a no-op (useful for conditionally built filters without an extra branch). And
// returns c so calls can be chained.
func (c *Conditions) And(fragment string, args ...any) *Conditions {
	if fragment == "" {
		return c
	}

	c.fragments = append(c.fragments, conditionFragment{sql: fragment, args: args})
	return c
}

// AndIf appends fragment (as And would) only when cond is true; when cond is false, args are
// ignored and c is returned unchanged. This is the common shape for an optional filter whose
// value a caller already validated, e.g. AndIf(status != "", "status = $1", status), though
// AndOptionalEq below covers that exact case without the extra branch.
func (c *Conditions) AndIf(cond bool, fragment string, args ...any) *Conditions {
	if !cond {
		return c
	}

	return c.And(fragment, args...)
}

// AndAnyOf appends an optional array filter that passes when values is NULL or empty and
// otherwise requires column to equal one of its elements:
//
//	($1::<elemType>[] IS NULL OR CARDINALITY($1::<elemType>[]) = 0 OR <column> = ANY($1::<elemType>[]))
//
// values is bound once as $1 of this fragment (three occurrences above renumber to the same
// global position); it is typically a []T slice matching elemType, or nil/empty to disable the
// filter entirely. elemType must match ^[A-Za-z_][A-Za-z0-9_ ]*$ (a bare or space-separated
// PostgreSQL type name, e.g. "smallint" or "double precision"); AndAnyOf panics on a violation,
// since elemType is developer-authored SQL, not user input, and a malformed value is a coding
// mistake to catch immediately rather than a runtime condition to handle.
func (c *Conditions) AndAnyOf(column, elemType string, values any) *Conditions {
	validateConditionsElemType(elemType)

	cast := "$1::" + elemType + "[]"
	fragment := cast + " IS NULL OR CARDINALITY(" + cast + ") = 0 OR " + column + " = ANY(" + cast + ")"
	return c.And(fragment, values)
}

// AndMatchAnyOf is AndAnyOf with a caller-supplied match expression instead of a fixed
// `column = ANY(...)` comparison, for EXISTS / NOT EXISTS / IN-subquery shapes that AndAnyOf
// cannot express:
//
//	($1::<elemType>[] IS NULL OR CARDINALITY($1::<elemType>[]) = 0 OR <match>)
//
// match must itself reference $1 as the array parameter, e.g.
// `EXISTS (SELECT 1 FROM tags WHERE tags.item_id = item.id AND tags.name = ANY($1))`. values
// and elemType behave exactly as in AndAnyOf, including the panic on a malformed elemType.
func (c *Conditions) AndMatchAnyOf(match, elemType string, values any) *Conditions {
	validateConditionsElemType(elemType)

	cast := "$1::" + elemType + "[]"
	fragment := cast + " IS NULL OR CARDINALITY(" + cast + ") = 0 OR " + match
	return c.And(fragment, values)
}

// AndNameMatchAnyOf appends a multi-name search that passes when names is NULL or empty and
// otherwise requires at least one non-blank name in it for which matchExpr holds:
//
//	($1::varchar[] IS NULL OR CARDINALITY($1::varchar[]) = 0 OR
//	 EXISTS (SELECT 1 FROM unnest($1::varchar[]) AS t WHERE btrim(t) <> '' AND <matchExpr>))
//
// matchExpr references the unnested element as t, e.g.
// `name ILIKE ('%' || btrim(t) || '%')`. names is bound once as $1.
func (c *Conditions) AndNameMatchAnyOf(matchExpr string, names []string) *Conditions {
	const cast = "$1::varchar[]"
	fragment := cast + " IS NULL OR CARDINALITY(" + cast + ") = 0 OR EXISTS (SELECT 1 FROM unnest(" + cast + ") AS t WHERE btrim(t) <> '' AND " + matchExpr + ")"
	return c.And(fragment, names)
}

// AndSearch appends a text-search filter that always passes when term is empty (the filter is
// disabled) and otherwise requires a match:
//
//	substring == true:  ($1 = '' OR <textExpr> ILIKE '%' || $1 || '%')
//	substring == false: ($1 = '' OR <tsvExpr> @@ plainto_tsquery($1))
//
// Use substring for a plain ILIKE contains-match against a text expression (textExpr), or the
// full-text-search form against a tsvector expression (tsvExpr). The unused one of the two
// expression arguments is ignored. term is bound once as $1.
func (c *Conditions) AndSearch(term string, substring bool, tsvExpr, textExpr string) *Conditions {
	var fragment string
	if substring {
		fragment = "$1 = '' OR " + textExpr + ` ILIKE '%' || $1 || '%'`
	} else {
		fragment = "$1 = '' OR " + tsvExpr + " @@ plainto_tsquery($1)"
	}

	return c.And(fragment, term)
}

// AndOptionalEq appends a zero-gated equality filter, disabled by the zero value of value:
//
//	string value:              ($1 = '' OR <column> = $1)
//	integer/uint/float value:  ($1 = 0 OR <column> = $1)
//	anything else:             <column> = $1 (no zero gate; value is always applied)
//
// The gate is chosen from value's reflect.Kind at call time. Values of a kind other than
// string or numeric (e.g. bool, time.Time, a slice) have no defined "disabled" zero value here,
// so the fragment applies the equality unconditionally. Pair AndOptionalEq with AndIf instead
// if such a value needs to be optional.
func (c *Conditions) AndOptionalEq(column string, value any) *Conditions {
	var fragment string
	switch kind := reflect.ValueOf(value).Kind(); {
	case kind == reflect.String:
		fragment = "$1 = '' OR " + column + " = $1"
	case isConditionsNumericKind(kind):
		fragment = "$1 = 0 OR " + column + " = $1"
	default:
		fragment = column + " = $1"
	}

	return c.And(fragment, value)
}

// AndMin appends a lower-bound filter that is ignored when value is not positive:
//
//	($1 <= 0 OR <column> >= $1)
func (c *Conditions) AndMin(column string, value any) *Conditions {
	return c.And("$1 <= 0 OR "+column+" >= $1", value)
}

// AndMax appends an upper-bound filter that is ignored when value is not positive:
//
//	($1 <= 0 OR <column> <= $1)
func (c *Conditions) AndMax(column string, value any) *Conditions {
	return c.And("$1 <= 0 OR "+column+" <= $1", value)
}

// Build renders the accumulated fragments as one parenthesized-and-AND-joined WHERE clause,
// with every fragment's $1..$n placeholders renumbered to consecutive global positions
// starting at startIndex, and returns that clause together with the flattened args in the same
// order (so the returned args line up with the returned clause's placeholders). Use startIndex
// 1 for a standalone query, or a higher value to append the clause after other, already-
// numbered parameters (e.g. NextIndex of an earlier Conditions).
//
// An empty builder is one built from Where("") with nothing appended, or from Where(fragment)
// where every And/And* call was skipped (AndIf(false, ...), an empty fragment, ...). It renders
// as the literal clause "TRUE" with nil args, so `WHERE ` + clause is always valid SQL and
// never filters out every row by accident.
//
// Build is the mechanism that lets one Conditions value drive both a page query and its COUNT
// twin: call Build(1) (or the same startIndex) again for the COUNT query and it renders the
// identical clause and args a second time, no manual re-numbering required.
func (c *Conditions) Build(startIndex int) (clause string, args []any) {
	if len(c.fragments) == 0 {
		return "TRUE", nil
	}

	parts := make([]string, 0, len(c.fragments))
	next := startIndex

	for _, f := range c.fragments {
		base := next - 1

		sql := placeholderPattern.ReplaceAllStringFunc(f.sql, func(match string) string {
			n, _ := strconv.Atoi(match[1:]) // match is always "$" + one-or-more digits, so this never errors.
			return "$" + strconv.Itoa(base+n)
		})

		parts = append(parts, "("+sql+")")
		args = append(args, f.args...)
		next += len(f.args)
	}

	return strings.Join(parts, " AND "), args
}

// Args returns the accumulated bind arguments in fragment order: the same args slice Build
// would return, without paying for the placeholder-renumbering pass.
func (c *Conditions) Args() []any {
	var args []any
	for _, f := range c.fragments {
		args = append(args, f.args...)
	}

	return args
}

// NextIndex returns startIndex plus the number of accumulated args: the first free $N after a
// Build(startIndex) call, ready for a caller to append its own trailing parameters (e.g. LIMIT
// $N / OFFSET $N+1) after the WHERE clause without recounting placeholders by hand.
func (c *Conditions) NextIndex(startIndex int) int {
	n := 0
	for _, f := range c.fragments {
		n += len(f.args)
	}

	return startIndex + n
}

// String implements fmt.Stringer by rendering Build(1)'s clause, for logging and tests where
// the exact starting parameter index does not matter.
func (c *Conditions) String() string {
	clause, _ := c.Build(1)
	return clause
}

// validateConditionsElemType panics if elemType does not match conditionsElemTypePattern.
// elemType is developer-authored SQL (see the Conditions doc comment), so a violation is a
// coding mistake caught at development time (mirroring NewRepository's fail-fast panic), not
// a runtime condition callers are expected to recover from. The panic value is a wrapped,
// descriptive error naming the offending elemType, consistent with the panics elsewhere in
// this module (e.g. desc.Expressions.FilterTable).
func validateConditionsElemType(elemType string) {
	if !conditionsElemTypePattern.MatchString(elemType) {
		panic(fmt.Errorf("pg: conditions: invalid elemType %q: must match %s", elemType, conditionsElemTypePattern.String()))
	}
}

// isConditionsNumericKind reports whether kind is one of the built-in signed integer, unsigned
// integer or floating-point kinds, used by AndOptionalEq to pick its zero-value gate.
func isConditionsNumericKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}
