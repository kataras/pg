package desc

import (
	"encoding/json"
	"math/big"
	"net"
	"reflect"
	"testing"
	"time"
)

// TestIsZero tests the isZero function with various inputs and outputs
func TestIsZero(t *testing.T) {
	now := time.Now()
	timePtr := &now
	var nilTimePtr *time.Time

	// Define a table of test cases
	testCases := []struct {
		input  any  // input value
		output bool // expected output value
	}{
		{nil, true},                             // nil value should be zero
		{"", true},                              // empty string should be zero
		{"hello", false},                        // non-empty string should not be zero
		{0, true},                               // zero int should be zero
		{1, false},                              // non-zero int should not be zero
		{0.0, true},                             // zero float should be zero
		{1.0, false},                            // non-zero float should not be zero
		{false, true},                           // false bool should be zero
		{true, false},                           // true bool should not be zero
		{[]int{}, true},                         // empty slice of ints should be zero
		{[]int{1, 2, 3}, false},                 // non-empty slice of ints should not be zero
		{[]string{}, true},                      // empty slice of strings should be zero
		{[]string{"a", "b", "c"}, false},        // non-empty slice of strings should not be zero
		{map[string]int{}, true},                // empty map of strings to ints should be zero
		{map[string]int{"a": 1, "b": 2}, false}, // non-empty map of strings to ints should not be zero
		{struct{}{}, true},                      // empty struct should be zero
		{struct{ x int }{1}, false},             // non-empty struct should not be zero
		{big.NewInt(0), true},                   // big int pointer with value 0 should be zero (checked via Sign(), not just nilness)
		{big.NewInt(1), false},                  // big int pointer with value 1 should not be zero
		{big.NewRat(0, 1), true},                // big rational pointer with value 0/1 should be zero (checked via Sign(), not just nilness)
		{big.NewRat(1, 2), false},               // big rational pointer with value 1/2 should not be zero
		{big.NewFloat(0.0), true},               // big float pointer with value 0.0 should be zero (checked via Sign(), not just nilness)
		{big.NewFloat(1.0), false},              // big float pointer with value 1.0 should not be zero
		{json.Number(""), true},                 // empty json.Number should be zero
		{json.Number("123"), false},             // non-empty json.Number should not be zero
		{net.IP{}, true},                        // empty net.IP should be zero
		{net.IPv4(127, 0, 0, 1), false},         // non-empty net.IP should not be zero
		{time.Time{}, true},                     // empty time.Time (zero time) should be zero
		{time.Now(), false},                     // non-empty time.Time (current time) should not be zero
		{timePtr, false},                        // non-nil time.Time (current time) should not be zero
		{nilTimePtr, true},                      // nil time.Time should be zero
	}

	for i, tc := range testCases {
		isNil := false

		if val := reflect.ValueOf(tc.input); val.Kind() == reflect.Pointer {
			isNil = val.IsNil()
		}

		if tc.input == nil || isNil {
			t.Run("nil", func(t *testing.T) {
				result := isZero(tc.input) // call the isZero function with the input
				if result != tc.output {   // compare the result with the expected output
					t.Errorf("[%d] isZero(%v) = %v, want %v", i, tc.input, result, tc.output) // report an error if they don't match
				}
			})
			continue
		}

		if zr, ok := tc.input.(Zeroer); ok { // if the input implements the Zeroer interface (this includes time.Time as well)
			result := zr.IsZero()    // call the IsZero method on the input value
			if result != tc.output { // compare the result with the expected output
				t.Errorf("[%d] %T.IsZero() = %v, want %v", i, tc.input, result, tc.output) // report an error if they don't match
			}

			continue
		}

		if tm, ok := tc.input.(time.Time); ok { // if the input is a time.Time value (this is a special case because time.Time implements Zeroer but has a different definition of zero)
			result := tm.IsZero() || tm.UnixNano() == 0 // call the IsZero method on the time value or check if its UnixNano representation is zero (this covers both the standard library definition and the custom definition of zero for time.Time)
			if result != tc.output {                    // compare the result with the expected output
				t.Errorf("[%d] %T.IsZero() = %v, want %v", i, tc.input, result, tc.output) // report an error if they don't match
			}

			continue
		}

		if ip, ok := tc.input.(net.IP); ok { // if the input is a net.IP value (this is another special case because net.IP is a slice of bytes but has a different definition of zero)
			result := len(ip) == 0 || ip.Equal(net.IPv4zero) || ip.Equal(net.IPv6zero) || ip.Equal(net.IPv6unspecified) || ip.Equal(net.IPv6loopback) || ip.Equal(net.IPv6interfacelocalallnodes) || ip.Equal(net.IPv6linklocalallnodes) || ip.Equal(net.IPv6linklocalallrouters) || ip.Equal(net.IPv4bcast) // check if the IP value is empty or equal to one of the predefined constants that represent a zero IP address (this covers all the possible cases of zero for net.IP)
			if result != tc.output {                                                                                                                                                                                                                                                                         // compare the result with the expected output
				t.Errorf("[%d] %T.IsZero() = %v, want %v", i, tc.input, result, tc.output) // report an error if they don't match
			}

			continue
		}

		result := isZero(tc.input) // call the isZero function with the input
		if result != tc.output {   // compare the result with the expected output
			t.Errorf("[%d] isZero(%v) = %v, want %v", i, tc.input, result, tc.output) // report an error if they don't match
		}
	}
}

// customEmail is a defined (named) string type, standing in for the kind of
// domain type (e.g. a validated email or ID wrapper) application code
// commonly uses instead of a bare string. The pre-rewrite type switch had no
// case for named string types, so isZero(customEmail("")) always returned
// false (never zero) via its `default: return false` branch, permanently
// blocking DEFAULT emission for any column of such a type.
type customEmail string

// valueReceiverZeroer implements Zeroer with a value receiver.
type valueReceiverZeroer struct{ n int }

// IsZero reports whether valueReceiverZeroer.n is zero.
func (v valueReceiverZeroer) IsZero() bool { return v.n == 0 }

// pointerReceiverZeroer implements Zeroer with a pointer receiver and, on
// purpose, does not guard itself against a nil receiver (no `if p == nil`
// check): it exists to prove that isZero (not the method) is what keeps a
// typed nil pointer of this type safe to check.
type pointerReceiverZeroer struct{ n int }

// IsZero reports whether pointerReceiverZeroer.n is zero. It panics if called
// on a nil receiver; isZero must never let that happen.
func (p *pointerReceiverZeroer) IsZero() bool { return p.n == 0 }

// plainCoords is an ordinary struct with no Zeroer implementation. The
// pre-rewrite type switch had no case for arbitrary structs (only the
// literal empty `struct{}` type was special-cased), so its zero value fell
// to `default: return false` and was always reported non-zero.
type plainCoords struct {
	X, Y int
}

// TestIsZeroTypeCoverage exercises the categories of value the pre-rewrite
// ~90-case type switch mishandled: unrecognized array-backed, named, or
// custom struct types fell to its `default: return false` and were
// permanently reported non-zero, blocking DEFAULT/gen_random_uuid()
// emission for columns of those types. It also covers the math/big and
// typed-nil Zeroer edge cases the replacement algorithm now handles
// explicitly, and a re-confirmation that the reflect.Value ("path 1")
// branch is unchanged.
func TestIsZeroTypeCoverage(t *testing.T) {
	nonZeroStringPtr := "x"
	nilPointerReceiverZeroer := (*pointerReceiverZeroer)(nil)

	tests := []struct {
		name  string
		input any
		want  bool
	}{
		// [16]byte-shaped UUID (array kind): the motivating "unknown type"
		// case: previously always non-zero regardless of value.
		{"array UUID zero", [16]byte{}, true},
		{"array UUID non-zero", [16]byte{1}, false},

		// Named string type: previously always non-zero.
		{"named string type zero", customEmail(""), true},
		{"named string type non-zero", customEmail("x"), false},

		// Pointer to a primitive: nil-unwrap then generic zero check.
		{"pointer to zero string", new(string), true},
		{"pointer to non-zero string", &nonZeroStringPtr, false},

		// Interface path slice/map: non-nil empty is zero (len == 0),
		// deliberately different from the reflect.Value path below.
		{"non-nil empty slice (interface path)", []int{}, true},
		{"non-empty slice", []int{0}, false},
		{"non-nil empty map (interface path)", map[string]int{}, true},

		// reflect.Value ("path 1") semantics must stay byte-identical: a
		// non-nil empty slice is NOT zero here (opposite of the interface
		// path above), and a typed nil pointer is zero.
		{"reflect.Value non-nil empty slice (path 1 preserved)", reflect.ValueOf([]int{}), false},
		{"reflect.Value typed nil pointer", reflect.ValueOf((*int)(nil)), true},

		// math/big: reflect-level zero detection is wrong for computed
		// zeros (Sub(x, x) keeps a non-nil internal slice), so these are
		// checked via Sign() instead.
		{"computed big.Int zero (Sub(x,x))", big.NewInt(5).Sub(big.NewInt(5), big.NewInt(5)), true},
		{"nil *big.Int", (*big.Int)(nil), true},
		{"non-nil big.Float representing 0", big.NewFloat(0), true},

		// time.Time implements Zeroer and is covered generically, without
		// being named anywhere in isZero.
		{"zero time.Time", time.Time{}, true},
		{"non-zero time.Time", time.Now(), false},

		// Zeroer dispatch, value and pointer receiver, including the
		// panic-prone-under-the-old-code typed-nil-pointer case.
		{"value-receiver Zeroer zero", valueReceiverZeroer{n: 0}, true},
		{"value-receiver Zeroer non-zero", valueReceiverZeroer{n: 1}, false},
		{"pointer-receiver Zeroer zero", &pointerReceiverZeroer{n: 0}, true},
		{"pointer-receiver Zeroer non-zero", &pointerReceiverZeroer{n: 1}, false},
		{"typed nil pointer-receiver Zeroer (must not panic)", nilPointerReceiverZeroer, true},

		// Arbitrary struct with no Zeroer: previously always non-zero.
		{"plain struct zero", plainCoords{}, true},
		{"plain struct non-zero field", plainCoords{X: 1}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isZero(tc.input)
			if got != tc.want {
				t.Errorf("isZero(%#v) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
