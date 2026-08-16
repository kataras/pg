package pg

import "testing"

// TestPtr verifies that Ptr returns a non-nil pointer to a copy of v, for both a zero and a
// non-zero value, and that dereferencing it yields v back.
func TestPtr(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		for _, v := range []string{"", "hello"} {
			p := Ptr(v)
			if p == nil {
				t.Fatalf("Ptr(%q): expected a non-nil pointer", v)
			}
			if *p != v {
				t.Fatalf("Ptr(%q): expected *p == %q, got %q", v, v, *p)
			}
		}
	})

	t.Run("int", func(t *testing.T) {
		for _, v := range []int{0, 42} {
			p := Ptr(v)
			if p == nil {
				t.Fatalf("Ptr(%d): expected a non-nil pointer", v)
			}
			if *p != v {
				t.Fatalf("Ptr(%d): expected *p == %d, got %d", v, v, *p)
			}
		}
	})
}

// TestNullIfZero verifies that NullIfZero returns nil for the zero value of T and a pointer to
// v (dereferencing back to v) otherwise, for both string and int.
func TestNullIfZero(t *testing.T) {
	t.Run("string zero value returns nil", func(t *testing.T) {
		if p := NullIfZero(""); p != nil {
			t.Fatalf(`NullIfZero(""): expected nil, got a pointer to %q`, *p)
		}
	})

	t.Run("string non-zero value returns a pointer to it", func(t *testing.T) {
		p := NullIfZero("hello")
		if p == nil {
			t.Fatal(`NullIfZero("hello"): expected a non-nil pointer`)
		}
		if *p != "hello" {
			t.Fatalf(`NullIfZero("hello"): expected *p == "hello", got %q`, *p)
		}
	})

	t.Run("int zero value returns nil", func(t *testing.T) {
		if p := NullIfZero(0); p != nil {
			t.Fatalf("NullIfZero(0): expected nil, got a pointer to %d", *p)
		}
	})

	t.Run("int non-zero value returns a pointer to it", func(t *testing.T) {
		p := NullIfZero(42)
		if p == nil {
			t.Fatal("NullIfZero(42): expected a non-nil pointer")
		}
		if *p != 42 {
			t.Fatalf("NullIfZero(42): expected *p == 42, got %d", *p)
		}
	})
}
