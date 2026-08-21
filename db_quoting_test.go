package pg

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestValidateSearchPath covers validateSearchPath's identifier check that guards the unquoted
// "CREATE SCHEMA IF NOT EXISTS <search path>;" statement in createDatabaseSchemaDump.
func TestValidateSearchPath(t *testing.T) {
	valid := []string{"public", "my_schema1"}
	for _, v := range valid {
		if err := validateSearchPath(v); err != nil {
			t.Errorf("expected %q to be accepted as a valid search path, got error: %v", v, err)
		}
	}

	invalid := []string{
		"pub lic",             // space is not a valid identifier character.
		`x";DROP SCHEMA y;--`, // SQL injection attempt.
		"",                    // empty.
	}
	for _, v := range invalid {
		if err := validateSearchPath(v); err == nil {
			t.Errorf("expected %q to be rejected as an invalid search path", v)
		}
	}
}

// pwdCustomColumnUser is a scratch table used only by TestSelectByUsernameAndPasswordCustomColumn.
// Its password column is deliberately renamed away from the field name via the "name=pwd" tag
// option, so that its actual column name ("pwd") differs from the literal "password" that the
// old, buggy SelectByUsernameAndPassword query hardcoded.
type pwdCustomColumnUser struct {
	ID       string `pg:"type=uuid,primary"`
	Username string `pg:"type=varchar(255),username,unique"`
	Password string `pg:"name=pwd,type=varchar(72),password"`
}

// TestSelectByUsernameAndPasswordCustomColumn is the regression test for bug M10:
// selectTableRecordByUsernameAndPassword used to build its WHERE clause with the column
// literally named "password" instead of passwordCol.Name, so any schema whose password column
// is not named "password" (like pwdCustomColumnUser's "pwd" here) failed with a
// `column "password" does not exist` database error. Requires the pgcrypto extension, which
// CreateSchema installs automatically because the schema has a password column.
func TestSelectByUsernameAndPasswordCustomColumn(t *testing.T) {
	const tableName = "test_pwd_custom_col_users"

	schema := NewSchema()
	schema.MustRegister(tableName, pwdCustomColumnUser{})

	db, err := Open(context.Background(), schema, getTestConnString())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	// Best-effort cleanup from a previous failed run, then guaranteed cleanup of this run: this
	// scratch table is private to this test, so dropping it (rather than the shared schema) is
	// both sufficient and safe.
	dropTestTables(ctx, db, tableName)
	defer dropTestTables(ctx, db, tableName)

	if err = db.CreateSchema(ctx); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if err = db.CheckSchema(ctx); err != nil {
		t.Fatalf("check schema: %v", err)
	}

	newUser := pwdCustomColumnUser{
		Username: "custom_pwd_col_user",
		Password: "s3cr3t-plain-password",
	}
	if err = db.InsertSingle(ctx, newUser, &newUser.ID); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var found pwdCustomColumnUser
	if err = db.SelectByUsernameAndPassword(ctx, &found, newUser.Username, newUser.Password); err != nil {
		t.Fatalf("SelectByUsernameAndPassword: %v", err)
	}
	if found.Username != newUser.Username {
		t.Fatalf("expected username: %s, got: %s", newUser.Username, found.Username)
	}

	// A wrong password must not match, and must fail with ErrNoRows (not a database error),
	// proving the fix compares against the real "pwd" column on both sides of the AND.
	var notFound pwdCustomColumnUser
	err = db.SelectByUsernameAndPassword(ctx, &notFound, newUser.Username, "wrong-password")
	if !errors.Is(err, ErrNoRows) {
		t.Fatalf("expected ErrNoRows for a wrong password, got: %v", err)
	}
}

// TestUpdateJSONBUnknownColumn verifies that UpdateJSONB validates columnName against the
// table's registered schema before building or executing any SQL: an unknown column returns a
// descriptive error naming the table and column, rather than reaching the database.
func TestUpdateJSONBUnknownColumn(t *testing.T) {
	db, err := openTestConnection(true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	const (
		table  = "customers"
		column = "definitely_not_a_real_column"
	)

	// rowID is not a valid uuid literal: if UpdateJSONB reached SQL execution despite the
	// unknown column, Postgres would reject it and the error would be a *pgconn.PgError instead
	// of our validation error.
	_, err = db.UpdateJSONB(ctx, table, column, "not-a-valid-uuid", map[string]any{"x": 1}, nil)
	if err == nil {
		t.Fatal("expected UpdateJSONB to fail for an unknown column, got nil error")
	}

	if !strings.Contains(err.Error(), table) || !strings.Contains(err.Error(), column) {
		t.Fatalf("expected the error to name the table and column, got: %v", err)
	}

	if _, ok := errors.AsType[*pgconn.PgError](err); ok {
		t.Fatalf("expected a validation error with no SQL executed, got a database error: %v", err)
	}
}

// TestDisableTableAutoVacuumQuoteName verifies that DisableTableAutoVacuum quotes the caller-
// supplied table name with QuoteIdentifier: a name containing a double quote can no longer break
// out of the generated ALTER TABLE statement to run injected SQL, it just fails to resolve to a
// real table. A normal, registered table name must keep working.
func TestDisableTableAutoVacuumQuoteName(t *testing.T) {
	db, err := openTestConnection(true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	t.Run("quote-injected name fails against the server instead of executing", func(t *testing.T) {
		const maliciousName = `customers"; DROP TABLE customers; --`

		err := db.DisableTableAutoVacuum(ctx, maliciousName)
		if err == nil {
			t.Fatal("expected an error for a quote-injected, non-existent table name, got nil")
		}

		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Fatalf("expected a *pgconn.PgError (undefined table) from the server, got: %v", err)
		}
		if pgErr.Code != "42P01" { // undefined_table.
			t.Fatalf("expected undefined_table (42P01), got code: %s (%v)", pgErr.Code, err)
		}

		// Prove the injected "DROP TABLE customers" never ran.
		var exists bool
		existsQuery := `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'customers');`
		if err = db.QueryRow(ctx, existsQuery, db.SearchPath()).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatal("the customers table no longer exists: the injected SQL executed")
		}
	})

	t.Run("normal registered table name still works", func(t *testing.T) {
		if err := db.DisableTableAutoVacuum(ctx, "customers"); err != nil {
			t.Fatalf("DisableTableAutoVacuum(customers): %v", err)
		}
	})
}
