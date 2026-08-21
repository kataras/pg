package desc

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// goldenAccount is a representative struct (a UUID primary key with a DB-side default, a
// unique-indexed varchar, a nullable varchar and a timestamp with a DB-side default) shared by
// the golden SQL tests in this file and in create_table_query_test.go. All of its names are in
// the safe [A-Za-z_][A-Za-z0-9_$]* charset.
//
// The "want" literals in those tests were captured by running BuildInsertQuery,
// BuildBulkInsertQuery and BuildCreateTableQuery, unmodified, against the pre-B4 code (the
// strconv.Quote-based quoting, no identifier validation, the un-offset paren search) in a
// throwaway capture test, then hardcoded here verbatim: see the B4 task report for the exact
// commands and captured output. Because every name below is in the safe charset, strconv.Quote
// and pgx.Identifier.Sanitize produce byte-identical output for it, so a passing test here proves
// the B4 fix does not change any currently-valid generated SQL.
type goldenAccount struct {
	ID        string    `pg:"type=uuid,primary"`
	Email     string    `pg:"type=varchar(255),unique_index=golden_accounts_email_idx"`
	Nickname  string    `pg:"type=varchar(64),nullable"`
	CreatedAt time.Time `pg:"type=timestamp,default=clock_timestamp()"`
}

// goldenAccountTable builds the *Table for goldenAccount via ConvertStructToTable, the same path
// production code uses, so the golden tests exercise the real column/index resolution too.
func goldenAccountTable(t *testing.T) *Table {
	t.Helper()

	td, err := ConvertStructToTable("golden_accounts", reflect.TypeFor[goldenAccount]())
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}

	return td
}

// TestBuildInsertQueryGolden is the byte-identical golden test for BuildInsertQuery; see
// goldenAccount's doc comment for how the expected literals were captured.
func TestBuildInsertQueryGolden(t *testing.T) {
	td := goldenAccountTable(t)
	sv := reflect.ValueOf(goldenAccount{Email: "a@example.com"})

	t.Run("plain insert", func(t *testing.T) {
		q, args, err := BuildInsertQuery(td, sv, nil, "", false)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (email) VALUES($1);`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a@example.com"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("upsert", func(t *testing.T) {
		q, args, err := BuildInsertQuery(td, sv, nil, "", true)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (email) VALUES($1) ON CONFLICT(email) DO UPDATE SET ;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a@example.com"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v", args, wantArgs)
		}
	})

	// "do nothing upsert" proves the `conflict=DO NOTHING` struct-tag form now works: tracing
	// buildInsertQuery used to show a tag-derived `conflict` expression (Column.Conflict,
	// surfaced via Table.OnConflict) was only ever read into onConflictExpression and then
	// immediately discarded: every code path either overwrote it with a computed
	// "DO UPDATE SET ..." or (this case: forceOnConflictExpr=="" and upsert==true, so
	// hasConflict==true skipped the "!hasConflict" branch) fell into the catch-all
	// "else { conflicts = nil }", which dropped the whole ON CONFLICT clause, so this case used
	// to be byte-identical to a plain insert. tagConflictTarget now special-cases a tag value of
	// (case-insensitively) "DO NOTHING" (the same sentinel forceOnConflictExpr recognizes, and
	// the same value as the root package's pg.DoNothing constant) into a real
	// "ON CONFLICT(<tag-derived Unique target>) DO NOTHING". See goldenDoNothingAccount's doc
	// comment.
	t.Run("do nothing upsert", func(t *testing.T) {
		dnTd := goldenDoNothingAccountTable(t)
		dnSv := reflect.ValueOf(goldenDoNothingAccount{Email: "a@example.com"})

		q, args, err := BuildInsertQuery(dnTd, dnSv, nil, "", true)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_do_nothing_accounts" (email) VALUES($1) ON CONFLICT(email) DO NOTHING;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a@example.com"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v", args, wantArgs)
		}
	})

	// "do nothing insert (no upsert)" proves the `conflict=DO NOTHING` tag is NOT honored on a
	// plain (non-upsert) BuildInsertQuery call, mirroring the existing, unchanged rule that a
	// tag-derived DO UPDATE target only fires when upsert is true. A duplicate row still raises
	// the database's own unique-violation error in this case.
	t.Run("do nothing insert (no upsert)", func(t *testing.T) {
		dnTd := goldenDoNothingAccountTable(t)
		dnSv := reflect.ValueOf(goldenDoNothingAccount{Email: "a@example.com"})

		q, _, err := BuildInsertQuery(dnTd, dnSv, nil, "", false)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_do_nothing_accounts" (email) VALUES($1);`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})

	// "forced DO NOTHING via pg.DoNothing" proves forceOnConflictExpr==DoNothing ("DO NOTHING")
	// now emits a real ON CONFLICT ... DO NOTHING instead of erroring ("can't find unique index
	// with name: DO NOTHING") or silently emitting the opposite action (DO UPDATE SET), which is
	// what the pre-fix code did depending on whether the table had a tag-derived conflict target.
	// This exercises goldenAccount (unique_index, no `conflict` tag) so the target comes from the
	// UniqueIndex-derived branch, not the Unique/hasConflict branch "do nothing upsert" exercises.
	t.Run("forced DO NOTHING via pg.DoNothing", func(t *testing.T) {
		q, args, err := BuildInsertQuery(td, sv, nil, "DO NOTHING", false)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (email) VALUES($1) ON CONFLICT(email) DO NOTHING;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a@example.com"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v", args, wantArgs)
		}
	})

	// "forced DO NOTHING with no tag-derived target" proves forceOnConflictExpr==DoNothing still
	// succeeds, with a target-less "ON CONFLICT DO NOTHING" (valid Postgres: it applies to a
	// conflict against any unique constraint on the table), when the struct declares no
	// unique/unique_index/conflict tag at all, so no target can be derived. It must not error
	// merely because "DO NOTHING" is not a named unique index.
	t.Run("forced DO NOTHING with no tag-derived target", func(t *testing.T) {
		type noTagAccount struct {
			Email string `pg:"type=varchar(255)"`
		}
		ntTd, err := ConvertStructToTable("golden_no_tag_accounts", reflect.TypeFor[noTagAccount]())
		if err != nil {
			t.Fatalf("ConvertStructToTable: %v", err)
		}
		ntSv := reflect.ValueOf(noTagAccount{Email: "a@example.com"})

		q, _, err := BuildInsertQuery(ntTd, ntSv, nil, "DO NOTHING", false)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_no_tag_accounts" (email) VALUES($1) ON CONFLICT DO NOTHING;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})

	// "forced DO NOTHING is case-insensitive" proves forceOnConflictExpr is matched
	// case-insensitively against the DoNothing sentinel, per tagConflictTarget's doc comment.
	t.Run("forced DO NOTHING is case-insensitive", func(t *testing.T) {
		q, _, err := BuildInsertQuery(td, sv, nil, "do nothing", false)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (email) VALUES($1) ON CONFLICT(email) DO NOTHING;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})

	// "forced DO NOTHING with idPtr appends RETURNING" proves the single-row builder's fix-round-2
	// change: a DO NOTHING action (forced via forceOnConflictExpr==DoNothing here) always carries
	// RETURNING <primary key> when idPtr is non-nil (returningColumn != ""), instead of never
	// appending RETURNING for a DO NOTHING action (writeOnConflictClause's normal rule). Without
	// this, db.QueryRow(...).Scan(idPtr) would get zero rows, and so return pgx.ErrNoRows, on
	// EVERY call, including a genuinely successful non-conflicting insert, since there was no
	// RETURNING clause at all to report the inserted row back. Now a successful insert populates
	// idPtr and only a skipped conflicting row surfaces as ErrNoRows: the same contract
	// BuildInsertQueryOnConflict already guarantees for OnConflict{DoNothing: true} (see
	// TestBuildInsertQueryOnConflictGolden's "do nothing always returns RETURNING when idPtr is
	// set").
	t.Run("forced DO NOTHING with idPtr appends RETURNING", func(t *testing.T) {
		var id string
		q, _, err := BuildInsertQuery(td, sv, &id, "DO NOTHING", false)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (email) VALUES($1) ON CONFLICT(email) DO NOTHING RETURNING id;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})

	// "tag-derived DO NOTHING with idPtr appends RETURNING" is the same proof for the
	// `conflict=DO NOTHING` struct-tag route (as opposed to the forced-via-forceOnConflictExpr
	// route the previous subtest exercises): both set onConflictExpression to exactly "DO
	// NOTHING" via tagConflictTarget, so both go through the same alwaysReturning fix in
	// buildInsertQuery.
	t.Run("tag-derived DO NOTHING with idPtr appends RETURNING", func(t *testing.T) {
		type doNothingWithPKAccount struct {
			ID    string `pg:"type=uuid,primary"`
			Email string `pg:"type=varchar(255),unique,conflict=DO NOTHING"`
		}
		pkTd, err := ConvertStructToTable("golden_do_nothing_pk_accounts", reflect.TypeFor[doNothingWithPKAccount]())
		if err != nil {
			t.Fatalf("ConvertStructToTable: %v", err)
		}
		pkSv := reflect.ValueOf(doNothingWithPKAccount{Email: "a@example.com"})

		var id string
		q, _, err := BuildInsertQuery(pkTd, pkSv, &id, "", true)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_do_nothing_pk_accounts" (email) VALUES($1) ON CONFLICT(email) DO NOTHING RETURNING id;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}
	})
}

// TestBuildInsertQueryForceOnConflictExprUnknownIndexError pins tagConflictTarget's error for a
// forceOnConflictExpr value that is neither the DoNothing sentinel nor the name of any unique
// index/tag-derived conflict column on the table: the "can't find unique index with name: ..."
// path, unchanged by this fix (only the "DO NOTHING" value is special-cased; every other
// unrecognized value still errors exactly as before). Uses a struct with no unique/unique_index/
// conflict tag at all (unlike goldenAccount, which already carries a tag-derived "email" target
// that an unrecognized forceOnConflictExpr would otherwise silently fall back to, pre-existing
// behavior this test isn't about), so tagConflictTarget's conflicts list starts empty and the
// unmatched lookup has nothing to fall back to.
func TestBuildInsertQueryForceOnConflictExprUnknownIndexError(t *testing.T) {
	type noIndexAccount struct {
		Email string `pg:"type=varchar(255)"`
	}
	td, err := ConvertStructToTable("golden_no_index_accounts", reflect.TypeFor[noIndexAccount]())
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}
	sv := reflect.ValueOf(noIndexAccount{Email: "a@example.com"})

	_, _, err = BuildInsertQuery(td, sv, nil, "not_a_real_index", false)
	if err == nil {
		t.Fatal("expected an error for an unrecognized forceOnConflictExpr, got nil")
	}

	wantErr := `can't find unique index with name: not_a_real_index`
	if err.Error() != wantErr {
		t.Fatalf("error mismatch:\ngot:  %s\nwant: %s", err.Error(), wantErr)
	}
}

// goldenDoNothingAccount exercises the tag-derived DO NOTHING path: a `conflict` tag holding the
// raw "DO NOTHING" expression, and a `unique` (not unique_index) column that becomes the ON
// CONFLICT target once td.OnConflict() reports a tag-derived expression is present (see
// Table.OnConflict / buildInsertQuery's hasConflict branch).
type goldenDoNothingAccount struct {
	Email    string `pg:"type=varchar(255),unique,conflict=DO NOTHING"`
	Nickname string `pg:"type=varchar(64),nullable"`
}

// goldenDoNothingAccountTable builds the *Table for goldenDoNothingAccount via
// ConvertStructToTable, mirroring goldenAccountTable.
func goldenDoNothingAccountTable(t *testing.T) *Table {
	t.Helper()

	td, err := ConvertStructToTable("golden_do_nothing_accounts", reflect.TypeFor[goldenDoNothingAccount]())
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}

	return td
}

// TestBuildBulkInsertQueryGolden is the byte-identical golden test for BuildBulkInsertQuery; see
// goldenAccount's doc comment for how the expected literals were captured.
func TestBuildBulkInsertQueryGolden(t *testing.T) {
	td := goldenAccountTable(t)
	values := []reflect.Value{
		reflect.ValueOf(goldenAccount{Email: "a@example.com"}),
		reflect.ValueOf(goldenAccount{Email: "b@example.com", Nickname: "bee"}),
	}

	t.Run("plain bulk insert", func(t *testing.T) {
		q, args, err := BuildBulkInsertQuery(td, values, "", false)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (id,email,nickname,created_at) VALUES (DEFAULT,$1,DEFAULT,DEFAULT),(DEFAULT,$2,$3,DEFAULT);`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a@example.com", "b@example.com", "bee"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("bulk upsert", func(t *testing.T) {
		q, args, err := BuildBulkInsertQuery(td, values, "", true)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (id,email,nickname,created_at) VALUES (DEFAULT,$1,DEFAULT,DEFAULT),(DEFAULT,$2,$3,DEFAULT) ON CONFLICT(email) DO UPDATE SET id = EXCLUDED.id,nickname = EXCLUDED.nickname,created_at = EXCLUDED.created_at;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a@example.com", "b@example.com", "bee"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v", args, wantArgs)
		}
	})

	// "bulk do nothing upsert" is BuildBulkInsertQuery's counterpart to
	// TestBuildInsertQueryGolden's "do nothing upsert": the tag-derived `conflict=DO NOTHING`
	// expression is now honored by the same tagConflictTarget special-case, so this emits a real
	// ON CONFLICT(email) DO NOTHING instead of falling back to a plain bulk insert.
	t.Run("bulk do nothing upsert", func(t *testing.T) {
		dnTd := goldenDoNothingAccountTable(t)
		dnValues := []reflect.Value{
			reflect.ValueOf(goldenDoNothingAccount{Email: "a@example.com"}),
			reflect.ValueOf(goldenDoNothingAccount{Email: "b@example.com", Nickname: "bee"}),
		}

		q, args, err := BuildBulkInsertQuery(dnTd, dnValues, "", true)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_do_nothing_accounts" (email,nickname) VALUES ($1,DEFAULT),($2,$3) ON CONFLICT(email) DO NOTHING;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a@example.com", "b@example.com", "bee"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v", args, wantArgs)
		}
	})

	// "bulk forced DO NOTHING via pg.DoNothing" is BuildBulkInsertQuery's counterpart to
	// TestBuildInsertQueryGolden's "forced DO NOTHING via pg.DoNothing".
	t.Run("bulk forced DO NOTHING via pg.DoNothing", func(t *testing.T) {
		q, args, err := BuildBulkInsertQuery(td, values, "DO NOTHING", false)
		if err != nil {
			t.Fatal(err)
		}

		wantQuery := `INSERT INTO "public"."golden_accounts" (id,email,nickname,created_at) VALUES (DEFAULT,$1,DEFAULT,DEFAULT),(DEFAULT,$2,$3,DEFAULT) ON CONFLICT(email) DO NOTHING;`
		if q != wantQuery {
			t.Fatalf("query mismatch:\ngot:  %s\nwant: %s", q, wantQuery)
		}

		wantArgs := []any{"a@example.com", "b@example.com", "bee"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args mismatch: got %#v, want %#v", args, wantArgs)
		}
	})
}

// TestValidatePasswordAlg verifies validatePasswordAlg accepts every allowlisted gen_salt()
// algorithm identifier and rejects everything else, including a value crafted to break out of
// the SQL string literal it's interpolated into by buildInsertPassword.
func TestValidatePasswordAlg(t *testing.T) {
	original := PasswordAlg
	t.Cleanup(func() { PasswordAlg = original })

	t.Run("allowlisted values pass", func(t *testing.T) {
		for _, alg := range []string{"bf", "md5", "xdes", "des"} {
			PasswordAlg = alg
			if err := validatePasswordAlg(); err != nil {
				t.Errorf("expected %q to be a valid PasswordAlg, got error: %v", alg, err)
			}
		}
	})

	t.Run("malicious value is rejected", func(t *testing.T) {
		PasswordAlg = `bf'); DROP TABLE x;--`
		if err := validatePasswordAlg(); err == nil {
			t.Fatal("expected an error for a malicious PasswordAlg value, got nil")
		}
	})

	t.Run("unknown value is rejected", func(t *testing.T) {
		PasswordAlg = "sha512" // not in pgcrypto's gen_salt allowlist.
		if err := validatePasswordAlg(); err == nil {
			t.Fatal("expected an error for an unrecognized PasswordAlg value, got nil")
		}
	})
}

// passwordAlgAccount is a minimal table with a password column and (deliberately) no
// PasswordHandler, so BuildInsertQuery takes the db-side crypt($N, gen_salt('<PasswordAlg>'))
// path that embeds PasswordAlg into the generated SQL.
type passwordAlgAccount struct {
	Email    string `pg:"type=varchar(255)"`
	Password string `pg:"type=varchar(255),password"`
}

// TestBuildInsertQueryRejectsInvalidPasswordAlg verifies BuildInsertQuery propagates
// validatePasswordAlg's error, instead of interpolating an attacker-controlled PasswordAlg
// value into the query, when the table has a password column and no PasswordHandler, and that
// it still succeeds once PasswordAlg is restored to an allowlisted value.
func TestBuildInsertQueryRejectsInvalidPasswordAlg(t *testing.T) {
	td, err := ConvertStructToTable("password_alg_accounts", reflect.TypeFor[passwordAlgAccount]())
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}
	// td.PasswordHandler is intentionally left nil.

	original := PasswordAlg
	t.Cleanup(func() { PasswordAlg = original })

	sv := reflect.ValueOf(passwordAlgAccount{Email: "a@example.com", Password: "secret"})

	PasswordAlg = `bf'); DROP TABLE x;--`
	if _, _, err := BuildInsertQuery(td, sv, nil, "", false); err == nil {
		t.Fatal("expected BuildInsertQuery to reject a malicious PasswordAlg value, got nil error")
	}

	PasswordAlg = "bf"
	if _, _, err := BuildInsertQuery(td, sv, nil, "", false); err != nil {
		t.Fatalf("expected BuildInsertQuery to succeed with a valid PasswordAlg, got: %v", err)
	}
}

// TestBuildBulkInsertQueryRejectsInvalidPasswordAlg is TestBuildInsertQueryRejectsInvalidPasswordAlg's
// counterpart for the multi-row bulk-insert builder.
func TestBuildBulkInsertQueryRejectsInvalidPasswordAlg(t *testing.T) {
	td, err := ConvertStructToTable("password_alg_bulk_accounts", reflect.TypeFor[passwordAlgAccount]())
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}

	original := PasswordAlg
	t.Cleanup(func() { PasswordAlg = original })

	values := []reflect.Value{
		reflect.ValueOf(passwordAlgAccount{Email: "a@example.com", Password: "secret"}),
	}

	PasswordAlg = `bf'); DROP TABLE x;--`
	if _, _, err := BuildBulkInsertQuery(td, values, "", false); err == nil {
		t.Fatal("expected BuildBulkInsertQuery to reject a malicious PasswordAlg value, got nil error")
	}

	PasswordAlg = "bf"
	if _, _, err := BuildBulkInsertQuery(td, values, "", false); err != nil {
		t.Fatalf("expected BuildBulkInsertQuery to succeed with a valid PasswordAlg, got: %v", err)
	}
}

// TestNumInsertableColumns verifies Table.NumInsertableColumns counts exactly the columns
// BuildBulkInsertQuery would put in its column list: everything except AutoGenerated and
// Presenter columns.
func TestNumInsertableColumns(t *testing.T) {
	td := &Table{
		Columns: []*Column{
			{Name: "id", AutoGenerated: true},
			{Name: "email"},
			{Name: "created_at"},
			{Name: "computed_summary", Presenter: true},
			{Name: "search_vector", AutoGenerated: true},
		},
	}

	want := 2 // email, created_at.
	if got := td.NumInsertableColumns(); got != want {
		t.Fatalf("expected %d insertable columns, got %d", want, got)
	}
}

// TestNumInsertableColumnsBatchCapArithmetic exercises the 65535-bind-parameter batch-size
// arithmetic used by Repository.InsertMany / Repository.UpsertMany (batchSize =
// min(DefaultInsertBatchSize, 65535/NumInsertableColumns())) against a table wide enough (>131
// insertable columns) that DefaultInsertBatchSize (500) would otherwise overflow PostgreSQL's
// 65535 bind-parameter-per-statement ceiling.
func TestNumInsertableColumnsBatchCapArithmetic(t *testing.T) {
	const numColumns = 200 // 200 * 500 = 100000 > 65535.

	columns := make([]*Column, 0, numColumns)
	for i := range numColumns {
		columns = append(columns, &Column{Name: fmt.Sprintf("col_%d", i)})
	}
	td := &Table{Columns: columns}

	n := td.NumInsertableColumns()
	if n != numColumns {
		t.Fatalf("expected %d insertable columns, got %d", numColumns, n)
	}

	batchSize := DefaultInsertBatchSize
	if n > 0 {
		batchSize = min(batchSize, 65535/n)
	}

	if batchSize >= DefaultInsertBatchSize {
		t.Fatalf("expected the batch cap to shrink batchSize below DefaultInsertBatchSize (%d) for a %d-column table, got %d", DefaultInsertBatchSize, numColumns, batchSize)
	}

	if batchSize*n > 65535 {
		t.Fatalf("effective batch (%d rows * %d columns = %d params) still exceeds PostgreSQL's 65535 bind-parameter ceiling", batchSize, n, batchSize*n)
	}
}

// TestWriteTableNameQuoting verifies writeTableName now quotes with pgx.Identifier.Sanitize's
// ""-doubling instead of strconv.Quote's Go-string escaping: a quote-bearing or non-ASCII schema
// or table name must come out correctly SQL-escaped, with none of strconv.Quote's artifacts
// (a backslash-escaped quote or a \uXXXX escape).
func TestWriteTableNameQuoting(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		table  string
	}{
		{"quote-bearing table name", "public", `evil"table`},
		{"quote-bearing schema name", `ev"il`, "table"},
		{"non-ASCII table name", "public", "naïve_table"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeTableName(&b, tt.schema, tt.table)
			got := b.String()

			want := pgx.Identifier{tt.schema, tt.table}.Sanitize()
			if got != want {
				t.Fatalf("writeTableName(%q, %q) = %q, want %q (pgx.Identifier.Sanitize)", tt.schema, tt.table, got, want)
			}

			if strings.Contains(got, `\"`) {
				t.Fatalf("output still contains a backslash-escaped quote (strconv.Quote artifact): %q", got)
			}
			if strings.Contains(got, `\u`) {
				t.Fatalf(`output still contains a \u escape (strconv.Quote artifact): %q`, got)
			}
			if strings.ContainsRune(tt.schema+tt.table, '"') && !strings.Contains(got, `""`) {
				t.Fatalf("expected the embedded quote to be doubled (correct SQL identifier escaping), got: %q", got)
			}
		})
	}
}
