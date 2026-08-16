package desc

import (
	"math/big"
	"reflect"
)

// Zeroer is an interface that defines a method to check if a value is zero.
//
// Zeroer can be implemented by custom types
// to report whether its current value is zero.
// Standard Time also implements that.
type Zeroer interface {
	IsZero() bool // IsZero returns true if the value is zero
}

// isZero reports whether v represents an absent/empty value for the purposes of INSERT
// default handling: a zero v causes the column's DB-side default (e.g. DEFAULT,
// gen_random_uuid(), clock_timestamp()) to fire instead of binding v as a parameter.
//
// isZero has two independent code paths, chosen by v's dynamic type:
//
//  1. v is a reflect.Value: the main per-field default-skip decision
//     (desc/argument.go and desc/insert_query.go). These semantics are fixed and must
//     never change: a pointer is zero iff nil, anything else is zero iff its
//     reflect.Value.IsZero() is true. A non-nil empty slice is therefore NOT zero on
//     this path.
//  2. v is anything else: the UUID-primary-key skip and the full-update path. Trust
//     order:
//     a. math/big pointer and value types (*big.Int, *big.Rat, *big.Float, big.Int,
//     big.Rat) are special-cased: reflect-level zero detection is wrong for them (a
//     big.Int produced by Sub(x, x) keeps a non-nil internal slice, so its reflect
//     zero value differs from its numeric zero), and none of them implement Zeroer.
//     Sign() == 0 is the correct check for all of them.
//     b. Pointers are unwrapped one level at a time. A nil pointer, at any depth, is
//     zero. A non-nil pointer whose type implements Zeroer (via a pointer or a
//     promoted value receiver) defers to that method instead of being dereferenced
//     further, so a typed-nil pointer never reaches an IsZero call on a nil
//     receiver, unlike the type switch this replaces.
//     c. Once fully dereferenced, a value that implements Zeroer (this is how
//     time.Time is covered, without needing to name it) defers to its IsZero method.
//     d. A slice or map, nil or not, is zero iff its length is 0: the historical
//     "empty is zero" rule for this path, deliberately different from path 1 above.
//     e. Everything else falls back to reflect.Value.IsZero(), which is correct for
//     arbitrary types (arrays such as a [16]byte UUID, named primitives, plain
//     structs, ...) that an exhaustive type switch could only get right by naming
//     them one by one.
func isZero(v any) bool {
	if v == nil {
		return true
	}

	switch t := v.(type) {
	case reflect.Value: // path 1: byte-identical semantics
		if t.Kind() == reflect.Pointer {
			return t.IsNil()
		}
		return t.IsZero()
	// explicit big-number cases: reflect.IsZero is wrong for computed zeros
	// (a big.Int produced by Sub(x,x) keeps a non-nil internal slice), and
	// the big types implement no IsZero method.
	case *big.Int:
		return t == nil || t.Sign() == 0
	case *big.Rat:
		return t == nil || t.Sign() == 0
	case *big.Float:
		return t == nil || t.Sign() == 0
	case big.Int:
		return t.Sign() == 0
	case big.Rat:
		return t.Sign() == 0
	}

	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return true // typed nil → zero
		}
		if z, ok := rv.Interface().(Zeroer); ok { // pointer-receiver IsZero, non-nil
			return z.IsZero()
		}
		rv = rv.Elem() // *string("") counts as zero (preserved)
	}
	if z, ok := rv.Interface().(Zeroer); ok { // value-receiver IsZero (incl. time.Time)
		return z.IsZero()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Map: // preserve len==0 → zero (nil or not)
		return rv.Len() == 0
	}
	return rv.IsZero() // the actual fix: unknown types now correct
}
