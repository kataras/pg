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
		{big.NewInt(0), false},                  // big int pointer with value 0 should not be zero
		{big.NewInt(1), false},                  // big int pointer with value 1 should not be zero
		{big.NewRat(0, 1), false},               // big rational pointer with value 0/1 should be zero
		{big.NewRat(1, 2), false},               // big rational pointer with value 1/2 should not be zero
		{big.NewFloat(0.0), false},              // big float pointer with value 0.0 should not be zero
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
