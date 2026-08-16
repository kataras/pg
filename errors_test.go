package pg

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// wrap simulates how these errors normally reach the classification helpers:
// wrapped with additional context via fmt.Errorf's %w, possibly more than once.
func wrap(err error) error {
	return fmt.Errorf("query failed: %w", fmt.Errorf("ctx: %w", err))
}

func TestIsErrDuplicate(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if key, ok := IsErrDuplicate(nil); ok || key != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", key, ok)
		}
	})

	t.Run("PgError SQLSTATE 23505", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Severity:       "ERROR",
			Code:           "23505",
			Message:        `duplicate key value violates unique constraint "customers_email_key"`,
			ConstraintName: "customers_email_key",
		}

		key, ok := IsErrDuplicate(wrap(pgErr))
		if !ok {
			t.Fatal("expected ok=true")
		}
		if key != "customers_email_key" {
			t.Fatalf("expected constraint name %q, got %q", "customers_email_key", key)
		}
	})

	t.Run("PgError unrelated code", func(t *testing.T) {
		pgErr := &pgconn.PgError{Severity: "ERROR", Code: "23503", Message: "unrelated"}

		if key, ok := IsErrDuplicate(wrap(pgErr)); ok || key != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", key, ok)
		}
	})

	t.Run("legacy text fallback", func(t *testing.T) {
		err := errors.New(`ERROR: duplicate key value violates unique constraint "customers_email_key" (SQLSTATE 23505)`)

		key, ok := IsErrDuplicate(err)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if key != "customers_email_key" {
			t.Fatalf("expected constraint name %q, got %q", "customers_email_key", key)
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		if key, ok := IsErrDuplicate(errors.New("boom")); ok || key != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", key, ok)
		}
	})
}

func TestIsErrForeignKey(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if key, ok := IsErrForeignKey(nil); ok || key != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", key, ok)
		}
	})

	t.Run("PgError SQLSTATE 23503", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Severity:       "ERROR",
			Code:           "23503",
			Message:        `insert or update on table "food_user_friendly_units" violates foreign key constraint "fk_food"`,
			ConstraintName: "fk_food",
		}

		key, ok := IsErrForeignKey(wrap(pgErr))
		if !ok {
			t.Fatal("expected ok=true")
		}
		if key != "fk_food" {
			t.Fatalf("expected constraint name %q, got %q", "fk_food", key)
		}
	})

	t.Run("PgError unrelated code", func(t *testing.T) {
		pgErr := &pgconn.PgError{Severity: "ERROR", Code: "23505", Message: "unrelated"}

		if key, ok := IsErrForeignKey(wrap(pgErr)); ok || key != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", key, ok)
		}
	})

	t.Run("legacy text fallback", func(t *testing.T) {
		// The legacy substring extraction returns whatever is between the FIRST
		// pair of double quotes in the message, which here is the table name,
		// not the constraint name; that pre-existing behavior is preserved
		// unchanged for non-PgError errors.
		err := errors.New(`ERROR: insert or update on table "food_user_friendly_units" violates foreign key constraint "fk_food" (SQLSTATE 23503)`)

		key, ok := IsErrForeignKey(err)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if key != "food_user_friendly_units" {
			t.Fatalf("expected extracted text %q, got %q", "food_user_friendly_units", key)
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		if key, ok := IsErrForeignKey(errors.New("boom")); ok || key != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", key, ok)
		}
	})
}

func TestIsErrInputSyntax(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if val, ok := IsErrInputSyntax(nil); ok || val != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", val, ok)
		}
	})

	t.Run("PgError SQLSTATE 22P02 with quoted value", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Severity: "ERROR",
			Code:     "22P02",
			Message:  `invalid input syntax for type uuid: "not-a-uuid"`,
		}

		val, ok := IsErrInputSyntax(wrap(pgErr))
		if !ok {
			t.Fatal("expected ok=true")
		}
		if val != "not-a-uuid" {
			t.Fatalf("expected %q, got %q", "not-a-uuid", val)
		}
	})

	t.Run("PgError SQLSTATE 22P02 without quoted value", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Severity: "ERROR",
			Code:     "22P02",
			Message:  "invalid input syntax for type uuid",
		}

		val, ok := IsErrInputSyntax(wrap(pgErr))
		if !ok {
			t.Fatal("expected ok=true")
		}
		if val != "invalid input syntax" {
			t.Fatalf("expected generic %q, got %q", "invalid input syntax", val)
		}
	})

	t.Run("PgError tsquery message under generic 42601", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Severity: "ERROR",
			Code:     "42601",
			Message:  `syntax error in tsquery: "foo &"`,
		}

		val, ok := IsErrInputSyntax(wrap(pgErr))
		if !ok {
			t.Fatal("expected ok=true")
		}
		if val != "foo &" {
			t.Fatalf("expected %q, got %q", "foo &", val)
		}
	})

	t.Run("PgError unrelated code", func(t *testing.T) {
		pgErr := &pgconn.PgError{Severity: "ERROR", Code: "23505", Message: "unrelated"}

		if val, ok := IsErrInputSyntax(wrap(pgErr)); ok || val != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", val, ok)
		}
	})

	t.Run("legacy text fallback", func(t *testing.T) {
		err := errors.New(`ERROR: invalid input syntax for type uuid: "not-a-uuid" (SQLSTATE 22P02)`)

		val, ok := IsErrInputSyntax(err)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if val != "not-a-uuid" {
			t.Fatalf("expected %q, got %q", "not-a-uuid", val)
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		if val, ok := IsErrInputSyntax(errors.New("boom")); ok || val != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", val, ok)
		}
	})
}

func TestIsErrColumnNotExists(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if IsErrColumnNotExists(nil, "email") {
			t.Fatal("expected false")
		}
	})

	t.Run("PgError SQLSTATE 42703", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Severity: "ERROR",
			Code:     "42703",
			Message:  `column "email" does not exist`,
		}

		if !IsErrColumnNotExists(wrap(pgErr), "email") {
			t.Fatal("expected true")
		}
	})

	t.Run("PgError SQLSTATE 42703 different column", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Severity: "ERROR",
			Code:     "42703",
			Message:  `column "email" does not exist`,
		}

		if IsErrColumnNotExists(wrap(pgErr), "name") {
			t.Fatal("expected false for a different column name")
		}
	})

	t.Run("PgError unrelated code", func(t *testing.T) {
		pgErr := &pgconn.PgError{Severity: "ERROR", Code: "23505", Message: `column "email" does not exist`}

		if IsErrColumnNotExists(wrap(pgErr), "email") {
			t.Fatal("expected false for an unrelated SQLSTATE")
		}
	})

	t.Run("legacy text fallback", func(t *testing.T) {
		err := errors.New(`ERROR: column "email" does not exist (SQLSTATE 42703)`)

		if !IsErrColumnNotExists(err, "email") {
			t.Fatal("expected true")
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		if IsErrColumnNotExists(errors.New("boom"), "email") {
			t.Fatal("expected false")
		}
	})
}

func TestAsConstraintError(t *testing.T) {
	knownCodes := []struct {
		code string
		kind ConstraintKind
	}{
		{"23505", ConstraintUnique},
		{"23503", ConstraintForeignKey},
		{"23502", ConstraintNotNull},
		{"23514", ConstraintCheck},
		{"23P01", ConstraintExclusion},
	}

	for _, tc := range knownCodes {
		t.Run("known code "+tc.code, func(t *testing.T) {
			pgErr := &pgconn.PgError{
				Severity:       "ERROR",
				Code:           tc.code,
				Message:        "boom",
				Detail:         "Key (name)=(x) already exists.",
				ConstraintName: "some_constraint",
				TableName:      "some_table",
				ColumnName:     "some_column",
			}

			constraintErr, ok := AsConstraintError(wrap(pgErr))
			if !ok {
				t.Fatalf("expected ok=true for code %s", tc.code)
			}

			if constraintErr.Kind != tc.kind {
				t.Fatalf("expected Kind %q, got %q", tc.kind, constraintErr.Kind)
			}
			if constraintErr.ConstraintName != "some_constraint" {
				t.Fatalf("expected ConstraintName %q, got %q", "some_constraint", constraintErr.ConstraintName)
			}
			if constraintErr.TableName != "some_table" {
				t.Fatalf("expected TableName %q, got %q", "some_table", constraintErr.TableName)
			}
			if constraintErr.ColumnName != "some_column" {
				t.Fatalf("expected ColumnName %q, got %q", "some_column", constraintErr.ColumnName)
			}
			if constraintErr.Detail != "Key (name)=(x) already exists." {
				t.Fatalf("expected Detail %q, got %q", "Key (name)=(x) already exists.", constraintErr.Detail)
			}
			if constraintErr.Code != tc.code {
				t.Fatalf("expected Code %q, got %q", tc.code, constraintErr.Code)
			}

			var pgErrOut *pgconn.PgError
			if !errors.As(constraintErr, &pgErrOut) {
				t.Fatal("expected errors.As to reach the underlying *pgconn.PgError via Unwrap")
			}
			if pgErrOut != pgErr {
				t.Fatalf("expected the unwrapped *pgconn.PgError to be the original instance")
			}
		})
	}

	t.Run("generic class-23 code has empty Kind", func(t *testing.T) {
		pgErr := &pgconn.PgError{Severity: "ERROR", Code: "23000", Message: "integrity constraint violation"}

		constraintErr, ok := AsConstraintError(wrap(pgErr))
		if !ok {
			t.Fatal("expected ok=true for SQLSTATE class 23")
		}
		if constraintErr.Kind != "" {
			t.Fatalf("expected empty Kind for an unmapped class-23 code, got %q", constraintErr.Kind)
		}
	})

	t.Run("non-PgError", func(t *testing.T) {
		if constraintErr, ok := AsConstraintError(errors.New("boom")); ok || constraintErr != nil {
			t.Fatalf("expected (nil, false), got (%v, %v)", constraintErr, ok)
		}
	})

	t.Run("PgError outside class 23", func(t *testing.T) {
		pgErr := &pgconn.PgError{Severity: "ERROR", Code: "42703", Message: "undefined column"}

		if constraintErr, ok := AsConstraintError(wrap(pgErr)); ok || constraintErr != nil {
			t.Fatalf("expected (nil, false), got (%v, %v)", constraintErr, ok)
		}
	})

	t.Run("nil error", func(t *testing.T) {
		if constraintErr, ok := AsConstraintError(nil); ok || constraintErr != nil {
			t.Fatalf("expected (nil, false), got (%v, %v)", constraintErr, ok)
		}
	})
}
