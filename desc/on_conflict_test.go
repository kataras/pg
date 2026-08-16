package desc

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// onConflictGoldenValue is the goldenAccount value shared by the OnConflict golden tests below:
// Email, Nickname and CreatedAt are all non-zero so extractArguments includes all three as
// insert arguments (ID stays zero, and is skipped as an implicit-default UUID primary key), which
// lets the "full SET fallback" and "partial SetColumns" cases produce visibly different SQL.
func onConflictGoldenValue() reflect.Value {
	return reflect.ValueOf(goldenAccount{
		Email:     "a@example.com",
		Nickname:  "bee",
		CreatedAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	})
}

// TestBuildInsertQueryOnConflictGolden exercises the ON CONFLICT emission rules from the
// OnConflict spec: explicit Columns target with the full-SET fallback, explicit Columns target
// with a partial SetColumns, a Constraint target, DoNothing, SetWhere, the empty-target
// tag-derived fallback, and the always-RETURNING rule.
func TestBuildInsertQueryOnConflictGolden(t *testing.T) {
	td := goldenAccountTable(t)
	sv := onConflictGoldenValue()

	t.Run("columns target, full SET fallback", func(t *testing.T) {
		q, args, err := BuildInsertQueryOnConflict(td, sv, nil, OnConflict{Columns: []string{"email"}})
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (email,nickname,created_at) VALUES($1,$2,$3) ON CONFLICT("email") DO UPDATE SET "nickname" = EXCLUDED."nickname","created_at" = EXCLUDED."created_at";`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a@example.com", "bee", onConflictGoldenValue().Interface().(goldenAccount).CreatedAt}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("columns target, partial SetColumns", func(t *testing.T) {
		q, _, err := BuildInsertQueryOnConflict(td, sv, nil, OnConflict{
			Columns:    []string{"email"},
			SetColumns: []string{"nickname"},
		})
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (email,nickname,created_at) VALUES($1,$2,$3) ON CONFLICT("email") DO UPDATE SET "nickname" = EXCLUDED."nickname";`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})

	t.Run("constraint target", func(t *testing.T) {
		q, _, err := BuildInsertQueryOnConflict(td, sv, nil, OnConflict{
			Constraint: "golden_accounts_email_key",
			SetColumns: []string{"nickname"},
		})
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (email,nickname,created_at) VALUES($1,$2,$3) ON CONFLICT ON CONSTRAINT "golden_accounts_email_key" DO UPDATE SET "nickname" = EXCLUDED."nickname";`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})

	t.Run("do nothing", func(t *testing.T) {
		q, _, err := BuildInsertQueryOnConflict(td, sv, nil, OnConflict{
			Columns:   []string{"email"},
			DoNothing: true,
		})
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (email,nickname,created_at) VALUES($1,$2,$3) ON CONFLICT("email") DO NOTHING;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})

	t.Run("set where", func(t *testing.T) {
		q, _, err := BuildInsertQueryOnConflict(td, sv, nil, OnConflict{
			Columns:    []string{"email"},
			SetColumns: []string{"nickname"},
			SetWhere:   `"public"."golden_accounts".created_at < EXCLUDED.created_at`,
		})
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (email,nickname,created_at) VALUES($1,$2,$3) ON CONFLICT("email") DO UPDATE SET "nickname" = EXCLUDED."nickname" WHERE "public"."golden_accounts".created_at < EXCLUDED.created_at;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})

	t.Run("empty target falls back to tag-derived target, unquoted", func(t *testing.T) {
		q, _, err := BuildInsertQueryOnConflict(td, sv, nil, OnConflict{})
		if err != nil {
			t.Fatal(err)
		}

		// The conflict TARGET is derived exactly like Upsert's tag-driven path (raw, unquoted
		// column name via UniqueIndex), while the SET list still follows OnConflict's own
		// quoting rule: the two are governed by independent rules (see resolveOnConflict).
		wantQuery := `INSERT INTO "public"."golden_accounts" (email,nickname,created_at) VALUES($1,$2,$3) ON CONFLICT(email) DO UPDATE SET "nickname" = EXCLUDED."nickname","created_at" = EXCLUDED."created_at";`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})

	t.Run("do nothing always returns RETURNING when idPtr is set", func(t *testing.T) {
		var id string
		q, _, err := BuildInsertQueryOnConflict(td, sv, &id, OnConflict{
			Columns:   []string{"email"},
			DoNothing: true,
		})
		if err != nil {
			t.Fatal(err)
		}

		if !strings.HasSuffix(q, ` RETURNING id;`) {
			t.Fatalf("expected query to end with RETURNING id even for DO NOTHING, got: %s", q)
		}
	})
}

// TestBuildInsertQueryOnConflictValidation covers the OnConflict validation rules:
// Columns/Constraint are mutually exclusive, DoNothing rejects SetColumns/SetWhere, and unknown
// column names (in either Columns or SetColumns) are rejected with a descriptive error.
func TestBuildInsertQueryOnConflictValidation(t *testing.T) {
	td := goldenAccountTable(t)
	sv := onConflictGoldenValue()

	tests := []struct {
		name string
		oc   OnConflict
	}{
		{"unknown column in Columns", OnConflict{Columns: []string{"does_not_exist"}}},
		{"unknown column in SetColumns", OnConflict{Columns: []string{"email"}, SetColumns: []string{"does_not_exist"}}},
		{"Columns and Constraint both set", OnConflict{Columns: []string{"email"}, Constraint: "golden_accounts_email_key"}},
		{"DoNothing with SetColumns", OnConflict{DoNothing: true, SetColumns: []string{"nickname"}}},
		{"DoNothing with SetWhere", OnConflict{DoNothing: true, SetWhere: "1=1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := BuildInsertQueryOnConflict(td, sv, nil, tt.oc); err == nil {
				t.Fatalf("expected an error for %+v, got nil", tt.oc)
			}
		})
	}
}

// TestBuildBulkInsertQueryOnConflictGolden is BuildBulkInsertQueryOnConflict's counterpart to
// TestBuildInsertQueryOnConflictGolden: a Columns target with a partial SetColumns, and DoNothing.
// BuildBulkInsertQueryOnConflict never appends RETURNING (same as BuildBulkInsertQuery).
func TestBuildBulkInsertQueryOnConflictGolden(t *testing.T) {
	td := goldenAccountTable(t)
	values := []reflect.Value{
		reflect.ValueOf(goldenAccount{Email: "a@example.com"}),
		reflect.ValueOf(goldenAccount{Email: "b@example.com", Nickname: "bee"}),
	}

	t.Run("columns target, partial SetColumns", func(t *testing.T) {
		q, args, err := BuildBulkInsertQueryOnConflict(td, values, OnConflict{
			Columns:    []string{"email"},
			SetColumns: []string{"nickname"},
		})
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (id,email,nickname,created_at) VALUES (DEFAULT,$1,DEFAULT,DEFAULT),(DEFAULT,$2,$3,DEFAULT) ON CONFLICT("email") DO UPDATE SET "nickname" = EXCLUDED."nickname";`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a@example.com", "b@example.com", "bee"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("do nothing", func(t *testing.T) {
		q, _, err := BuildBulkInsertQueryOnConflict(td, values, OnConflict{
			Columns:   []string{"email"},
			DoNothing: true,
		})
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (id,email,nickname,created_at) VALUES (DEFAULT,$1,DEFAULT,DEFAULT),(DEFAULT,$2,$3,DEFAULT) ON CONFLICT("email") DO NOTHING;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})

	t.Run("validation error propagates", func(t *testing.T) {
		if _, _, err := BuildBulkInsertQueryOnConflict(td, values, OnConflict{Columns: []string{"nope"}}); err == nil {
			t.Fatal("expected an error for an unknown column, got nil")
		}
	})
}
