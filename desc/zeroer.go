package desc

import (
	"encoding/json"
	"math/big"
	"net"
)

// Zeroer is an interface that defines a method to check if a value is zero.
//
// Zeroer can be implemented by custom types
// to report whether its current value is zero.
// Standard Time also implements that.
type Zeroer interface {
	IsZero() bool // IsZero returns true if the value is zero
}

// isZero takes an interface value and returns true if it is nil or zero.
func isZero(v any) bool {
	if v == nil {
		// if the value is nil, return true
		return true
	}

	switch t := v.(type) { // switch on the type of the value
	case Zeroer: // if the value implements the Zeroer interface (this includes time.Time as well)
		if t == nil { // if the value is nil, return true
			return true
		}
		return t.IsZero() // otherwise, call the IsZero method and return its result
	case string: // if the value is a string
		return t == "" // return true if the string is empty
	case int: // if the value is an int
		return t == 0 // return true if the int is zero
	case int8: // if the value is an int8
		return t == 0 // return true if the int8 is zero
	case int16: // if the value is an int16
		return t == 0 // return true if the int16 is zero
	case int32: // if the value is an int32
		return t == 0 // return true if the int32 is zero
	case int64: // if the value is an int64
		return t == 0 // return true if the int64 is zero
	case uint: // if the value is a uint
		return t == 0 // return true if the uint is zero
	case uint8: // if the value is a uint8
		return t == 0 // return true if the uint8 is zero
	case uint16: // if the value is a uint16
		return t == 0 // return true if the uint16 is zero
	case uint32: // if the value is a uint32
		return t == 0 // return true if the uint32 is zero
	case uint64: // if the value is a uint64
		return t == 0 // return true if the uint64 is zero
	case float32: // if the value is a float32
		return t == 0 // return true if the float32 is zero
	case float64: // if the value is a float64
		return t == 0 // return true if the float64 is zero
	case bool: // if the value is a bool
		return !t // return true if the bool is false (the opposite of its value)
	case []int: // if the value is a slice of ints
		return len(t) == 0 // return true if the slice has zero length
	case []string: // if the value is a slice of strings
		return len(t) == 0 // return true if the slice has zero length
	case [][]int: // if the value is a slice of slices of ints
		return len(t) == 0 // return true if the slice has zero length
	case [][]string: // if the value is a slice of slices of strings
		return len(t) == 0 // return true if the slice has zero length
	case json.Number: // if the value is a json.Number (a string that represents a number in JSON)
		return t.String() == "" // return true if the string representation of the number is empty
	case net.IP: // if the value is a net.IP (a slice of bytes that represents an IP address)
		return len(t) == 0 // return true if the slice has zero length
	case map[string]any:
		return len(t) == 0
	case map[int]any:
		return len(t) == 0
	case map[string]string:
		return len(t) == 0
	case map[string]int:
		return len(t) == 0
	case map[int]int:
		return len(t) == 0
	case struct{}:
		return true
	case *big.Int:
		return t == nil
	case big.Int:
		return isZero(t.Int64())
	case *big.Rat:
		return t == nil
	case big.Rat:
		return isZero(t.Num())
	case *big.Float:
		return t == nil
	default: // for any other type of value
		return false // return false (assume it's not zero)
	}
}
