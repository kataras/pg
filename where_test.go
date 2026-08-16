package pg

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestWhereEmptyBuildsTrue(t *testing.T) {
	c := Where("")

	gotClause, gotArgs := c.Build(1)
	if gotClause != "TRUE" {
		t.Fatalf("clause = %q, want %q", gotClause, "TRUE")
	}
	if gotArgs != nil {
		t.Fatalf("args = %#v, want nil", gotArgs)
	}
}

func TestConditionsEmptyAfterSkippedAndIf(t *testing.T) {
	c := Where("").AndIf(false, "x = $1", 1)

	gotClause, gotArgs := c.Build(5)
	if gotClause != "TRUE" {
		t.Fatalf("clause = %q, want %q", gotClause, "TRUE")
	}
	if gotArgs != nil {
		t.Fatalf("args = %#v, want nil", gotArgs)
	}
}

func TestWhereWithFirstFragment(t *testing.T) {
	c := Where("status = $1", "active")

	gotClause, gotArgs := c.Build(1)
	wantClause := "(status = $1)"
	if gotClause != wantClause {
		t.Fatalf("clause = %q, want %q", gotClause, wantClause)
	}

	wantArgs := []any{"active"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

// TestConditionsMultipleFragmentsInterleavedArgs covers several fragments where some carry
// more than one arg, verifying Build both renumbers every placeholder correctly and flattens
// the args from all fragments, in fragment order, into one slice.
func TestConditionsMultipleFragmentsInterleavedArgs(t *testing.T) {
	c := Where("a = $1", "A").
		And("b BETWEEN $1 AND $2", 10, 20).
		And("c = $1", "C")

	gotClause, gotArgs := c.Build(1)
	wantClause := "(a = $1) AND (b BETWEEN $2 AND $3) AND (c = $4)"
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{"A", 10, 20, "C"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndRepeatedPlaceholderSingleArg verifies that $1 repeated three times inside
// one fragment renumbers to the SAME global placeholder every time and consumes exactly one
// arg, not three.
func TestConditionsAndRepeatedPlaceholderSingleArg(t *testing.T) {
	c := Where("").And("$1 = 1 OR $1 = 2 OR $1 = 3", 42)

	gotClause, gotArgs := c.Build(5)
	wantClause := "($5 = 1 OR $5 = 2 OR $5 = 3)"
	if gotClause != wantClause {
		t.Fatalf("clause = %q, want %q", gotClause, wantClause)
	}

	wantArgs := []any{42}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

// TestConditionsBuildRenumberingBoundary is the $1-vs-$10 boundary test: ten fragments, each
// with exactly one arg, must renumber to global placeholders $1..$10 with no collision between
// the single-digit and double-digit forms.
func TestConditionsBuildRenumberingBoundary(t *testing.T) {
	c := Where("a = $1", "v1")
	for i := 2; i <= 10; i++ {
		c.And(fmt.Sprintf("col%d = $1", i), fmt.Sprintf("v%d", i))
	}

	gotClause, gotArgs := c.Build(1)

	wantParts := []string{"(a = $1)"}
	wantArgs := []any{"v1"}
	for i := 2; i <= 10; i++ {
		wantParts = append(wantParts, fmt.Sprintf("(col%d = $%d)", i, i))
		wantArgs = append(wantArgs, fmt.Sprintf("v%d", i))
	}
	wantClause := strings.Join(wantParts, " AND ")

	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}

	// The critical boundary assertion: $10 must appear as one two-digit token, never as "$1"
	// immediately followed by a stray literal "0" character, and $1 itself (the first
	// fragment) must still be intact and distinct from it.
	if !strings.Contains(gotClause, "(col10 = $10)") {
		t.Fatalf("expected the clause to contain the two-digit placeholder $10, got: %s", gotClause)
	}
	if !strings.Contains(gotClause, "(a = $1)") {
		t.Fatalf("expected the clause to still contain the single-digit placeholder $1, got: %s", gotClause)
	}
	if strings.Contains(gotClause, "$100") {
		t.Fatalf("clause contains a spurious $100 - renumbering likely concatenated digits: %s", gotClause)
	}
}

// TestConditionsRenumberingDoubleDigitRepeatedPlaceholder combines the repeated-placeholder
// case with the double-digit boundary: all three occurrences of $1 inside AndAnyOf's generated
// fragment must renumber to the same $10, not to "$1" followed by a literal "0".
func TestConditionsRenumberingDoubleDigitRepeatedPlaceholder(t *testing.T) {
	c := Where("").AndAnyOf("category", "smallint", []int16{1})

	gotClause, _ := c.Build(10)
	wantClause := `($10::smallint[] IS NULL OR CARDINALITY($10::smallint[]) = 0 OR category = ANY($10::smallint[]))`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}
}

func TestConditionsAndIfSkipped(t *testing.T) {
	c := Where("a = $1", 1).AndIf(false, "b = $1", 999)

	gotClause, gotArgs := c.Build(1)
	wantClause := "(a = $1)"
	if gotClause != wantClause {
		t.Fatalf("clause = %q, want %q", gotClause, wantClause)
	}

	wantArgs := []any{1}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestConditionsAndIfIncluded(t *testing.T) {
	c := Where("a = $1", 1).AndIf(true, "b = $1", 2)

	gotClause, gotArgs := c.Build(1)
	wantClause := "(a = $1) AND (b = $2)"
	if gotClause != wantClause {
		t.Fatalf("clause = %q, want %q", gotClause, wantClause)
	}

	wantArgs := []any{1, 2}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndAnyOfGolden asserts the exact SQL shape documented for AndAnyOf.
func TestConditionsAndAnyOfGolden(t *testing.T) {
	c := Where("").AndAnyOf("category", "smallint", []int16{1, 2, 3})

	gotClause, gotArgs := c.Build(1)
	wantClause := `($1::smallint[] IS NULL OR CARDINALITY($1::smallint[]) = 0 OR category = ANY($1::smallint[]))`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{[]int16{1, 2, 3}}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndAnyOfMultiWordElemType verifies elemType may contain the space-separated
// PostgreSQL type names the pattern allows, e.g. "double precision".
func TestConditionsAndAnyOfMultiWordElemType(t *testing.T) {
	c := Where("").AndAnyOf("weight", "double precision", []float64{1.5})

	gotClause, _ := c.Build(1)
	wantClause := `($1::double precision[] IS NULL OR CARDINALITY($1::double precision[]) = 0 OR weight = ANY($1::double precision[]))`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}
}

// TestConditionsAndMatchAnyOfGolden asserts the exact SQL shape documented for AndMatchAnyOf,
// including that the caller-supplied match expression's own $1 reference renumbers along with
// the generated cast's.
func TestConditionsAndMatchAnyOfGolden(t *testing.T) {
	match := "EXISTS (SELECT 1 FROM tags WHERE tags.id = ANY($1))"
	c := Where("").AndMatchAnyOf(match, "uuid", []string{"a"})

	gotClause, gotArgs := c.Build(1)
	wantClause := `($1::uuid[] IS NULL OR CARDINALITY($1::uuid[]) = 0 OR EXISTS (SELECT 1 FROM tags WHERE tags.id = ANY($1)))`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{[]string{"a"}}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndMatchAnyOfRenumbering verifies that when Build starts past 9, every $1 in
// AndMatchAnyOf's fragment (the generated cast's occurrences AND the caller-supplied match
// expression's own $1) renumber together to the same double-digit placeholder.
func TestConditionsAndMatchAnyOfRenumbering(t *testing.T) {
	match := "EXISTS (SELECT 1 FROM tags WHERE tags.id = ANY($1))"
	c := Where("").AndMatchAnyOf(match, "uuid", []string{"a"})

	gotClause, _ := c.Build(10)
	wantClause := `($10::uuid[] IS NULL OR CARDINALITY($10::uuid[]) = 0 OR EXISTS (SELECT 1 FROM tags WHERE tags.id = ANY($10)))`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}
}

// TestConditionsAndNameMatchAnyOfGolden asserts the exact SQL shape documented for
// AndNameMatchAnyOf.
func TestConditionsAndNameMatchAnyOfGolden(t *testing.T) {
	matchExpr := `name ILIKE ('%' || btrim(t) || '%')`
	c := Where("").AndNameMatchAnyOf(matchExpr, []string{"Ann", "Bob"})

	gotClause, gotArgs := c.Build(1)
	wantClause := "($1::varchar[] IS NULL OR CARDINALITY($1::varchar[]) = 0 OR EXISTS (SELECT 1 FROM unnest($1::varchar[]) AS t WHERE btrim(t) <> '' AND " + matchExpr + "))"
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{[]string{"Ann", "Bob"}}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndSearchSubstringGolden asserts the exact SQL shape documented for
// AndSearch(substring=true).
func TestConditionsAndSearchSubstringGolden(t *testing.T) {
	c := Where("").AndSearch("ann", true, "search_vector", "full_name")

	gotClause, gotArgs := c.Build(1)
	wantClause := `($1 = '' OR full_name ILIKE '%' || $1 || '%')`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{"ann"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndSearchTsvGolden asserts the exact SQL shape documented for
// AndSearch(substring=false).
func TestConditionsAndSearchTsvGolden(t *testing.T) {
	c := Where("").AndSearch("ann", false, "search_vector", "full_name")

	gotClause, gotArgs := c.Build(1)
	wantClause := `($1 = '' OR search_vector @@ plainto_tsquery($1))`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{"ann"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndSearchEmptyTermStillBuildsFilter verifies AndSearch still appends a
// fragment (it always passes at query time when term is empty via the $1 = ” gate) rather
// than skipping itself the way AndIf(false, ...) does; the "always passes" behavior belongs to
// the generated SQL, not to whether the fragment is appended.
func TestConditionsAndSearchEmptyTermStillBuildsFilter(t *testing.T) {
	c := Where("").AndSearch("", true, "search_vector", "full_name")

	gotClause, gotArgs := c.Build(1)
	wantClause := `($1 = '' OR full_name ILIKE '%' || $1 || '%')`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{""}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndOptionalEqStringGolden asserts the string-kind gate.
func TestConditionsAndOptionalEqStringGolden(t *testing.T) {
	c := Where("").AndOptionalEq("email", "x@y.com")

	gotClause, gotArgs := c.Build(1)
	wantClause := `($1 = '' OR email = $1)`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{"x@y.com"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndOptionalEqNumericGolden asserts the numeric-kind gate.
func TestConditionsAndOptionalEqNumericGolden(t *testing.T) {
	c := Where("").AndOptionalEq("age", 42)

	gotClause, gotArgs := c.Build(1)
	wantClause := `($1 = 0 OR age = $1)`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{42}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndOptionalEqFloatGolden verifies the numeric gate also covers float kinds.
func TestConditionsAndOptionalEqFloatGolden(t *testing.T) {
	c := Where("").AndOptionalEq("score", 3.5)

	gotClause, _ := c.Build(1)
	wantClause := `($1 = 0 OR score = $1)`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}
}

// TestConditionsAndOptionalEqOtherKindGolden verifies a kind that is neither string nor
// numeric (bool here) gets no zero gate at all: the equality always applies.
func TestConditionsAndOptionalEqOtherKindGolden(t *testing.T) {
	c := Where("").AndOptionalEq("active", true)

	gotClause, gotArgs := c.Build(1)
	wantClause := `(active = $1)`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{true}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndMinGolden asserts the exact SQL shape documented for AndMin.
func TestConditionsAndMinGolden(t *testing.T) {
	c := Where("").AndMin("price", 10)

	gotClause, gotArgs := c.Build(1)
	wantClause := `($1 <= 0 OR price >= $1)`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{10}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

// TestConditionsAndMaxGolden asserts the exact SQL shape documented for AndMax.
func TestConditionsAndMaxGolden(t *testing.T) {
	c := Where("").AndMax("price", 100)

	gotClause, gotArgs := c.Build(1)
	wantClause := `($1 <= 0 OR price <= $1)`
	if gotClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", gotClause, wantClause)
	}

	wantArgs := []any{100}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

func TestConditionsArgs(t *testing.T) {
	c := Where("a = $1", 1).And("b BETWEEN $1 AND $2", 10, 20)

	got := c.Args()
	want := []any{1, 10, 20}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args() = %#v, want %#v", got, want)
	}

	// Args must match Build's flattened args regardless of the startIndex given to Build -
	// only the clause's placeholder numbers change, never the args themselves.
	_, buildArgs := c.Build(50)
	if !reflect.DeepEqual(got, buildArgs) {
		t.Fatalf("Args() = %#v, want it to match Build(50)'s args %#v", got, buildArgs)
	}
}

func TestConditionsArgsEmpty(t *testing.T) {
	c := Where("")
	if got := c.Args(); got != nil {
		t.Fatalf("Args() = %#v, want nil", got)
	}
}

func TestConditionsNextIndex(t *testing.T) {
	c := Where("a = $1", 1).And("b BETWEEN $1 AND $2", 10, 20)

	if got := c.NextIndex(1); got != 4 {
		t.Fatalf("NextIndex(1) = %d, want 4", got)
	}
	if got := c.NextIndex(5); got != 8 {
		t.Fatalf("NextIndex(5) = %d, want 8", got)
	}
}

func TestConditionsNextIndexEmpty(t *testing.T) {
	c := Where("")
	if got := c.NextIndex(1); got != 1 {
		t.Fatalf("NextIndex(1) on an empty builder = %d, want 1 (unchanged)", got)
	}
}

func TestConditionsString(t *testing.T) {
	c := Where("a = $1", 1).And("b = $1", 2)

	want := "(a = $1) AND (b = $2)"
	if got := c.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}

	wantClause, _ := c.Build(1)
	if c.String() != wantClause {
		t.Fatalf("String() = %q, want it to equal Build(1)'s clause %q", c.String(), wantClause)
	}
}

func TestConditionsStringEmpty(t *testing.T) {
	c := Where("")
	if got := c.String(); got != "TRUE" {
		t.Fatalf("String() = %q, want %q", got, "TRUE")
	}
}

// TestConditionsAndAnyOfPanicsOnBadElemType verifies AndAnyOf fails fast, via panic, on an
// elemType that does not match the allowed identifier pattern: a coding mistake, not a
// runtime condition to handle gracefully.
func TestConditionsAndAnyOfPanicsOnBadElemType(t *testing.T) {
	const badElemType = "smallint[]; DROP"

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected AndAnyOf to panic on a malformed elemType, got no panic")
		}

		err, ok := r.(error)
		if !ok {
			t.Fatalf("expected the panic value to be an error, got: %#v", r)
		}

		if !strings.Contains(err.Error(), badElemType) {
			t.Fatalf("expected the panic error to mention the offending elemType %q, got: %v", badElemType, err)
		}
	}()

	Where("").AndAnyOf("category", badElemType, nil)
}

// TestConditionsAndMatchAnyOfPanicsOnBadElemType mirrors the above for AndMatchAnyOf, which
// validates elemType the same way.
func TestConditionsAndMatchAnyOfPanicsOnBadElemType(t *testing.T) {
	const badElemType = "uuid); DROP TABLE x; --"

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected AndMatchAnyOf to panic on a malformed elemType, got no panic")
		}
	}()

	Where("").AndMatchAnyOf("EXISTS (SELECT 1)", badElemType, nil)
}

// TestConditionsRealisticCompositePageAndCountReuse is the core use case this builder exists
// for: one filter set, built with a realistic mix of helpers, drives both a page query and its
// COUNT twin. Calling Build with the same startIndex twice must yield byte-identical clauses
// and args both times. That identity is what lets a caller reuse the same Conditions value for
// both queries instead of hand-writing (and hand-renumbering) the WHERE clause twice.
func TestConditionsRealisticCompositePageAndCountReuse(t *testing.T) {
	filters := Where("archived = $1", false).
		AndSearch("ann", true, "search_vector", "full_name").
		AndAnyOf("category", "smallint", []int16{1, 2}).
		AndMin("price", 10).
		AndMax("price", 100)

	pageClause, pageArgs := filters.Build(1)
	countClause, countArgs := filters.Build(1)

	if pageClause != countClause {
		t.Fatalf("expected identical clauses for the page and count queries:\npage:  %s\ncount: %s", pageClause, countClause)
	}
	if !reflect.DeepEqual(pageArgs, countArgs) {
		t.Fatalf("expected identical args for the page and count queries:\npage:  %#v\ncount: %#v", pageArgs, countArgs)
	}

	wantClause := "(archived = $1) AND " +
		"($2 = '' OR full_name ILIKE '%' || $2 || '%') AND " +
		"($3::smallint[] IS NULL OR CARDINALITY($3::smallint[]) = 0 OR category = ANY($3::smallint[])) AND " +
		"($4 <= 0 OR price >= $4) AND " +
		"($5 <= 0 OR price <= $5)"
	if pageClause != wantClause {
		t.Fatalf("clause mismatch:\ngot:  %s\nwant: %s", pageClause, wantClause)
	}

	wantArgs := []any{false, "ann", []int16{1, 2}, 10, 100}
	if !reflect.DeepEqual(pageArgs, wantArgs) {
		t.Fatalf("args mismatch:\ngot:  %#v\nwant: %#v", pageArgs, wantArgs)
	}

	// A caller appending LIMIT/OFFSET to the page query (never to the COUNT query) picks up
	// exactly where the shared filters left off.
	if got := filters.NextIndex(1); got != 6 {
		t.Fatalf("NextIndex(1) = %d, want 6", got)
	}
}
