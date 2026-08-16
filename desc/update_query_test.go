package desc

import (
	"reflect"
	"testing"
)

// passwordAlgUpdateAccount mirrors passwordAlgAccount (see insert_query_test.go) but adds a
// primary key, since BuildUpdateQuery requires one. It deliberately has no PasswordHandler, so
// updating Password takes the db-side crypt($N, gen_salt('<PasswordAlg>')) path in
// buildUpdateQuery.
type passwordAlgUpdateAccount struct {
	ID       string `pg:"type=uuid,primary"`
	Email    string `pg:"type=varchar(255)"`
	Password string `pg:"type=varchar(255),password"`
}

// TestBuildUpdateQueryRejectsInvalidPasswordAlg mirrors
// TestBuildInsertQueryRejectsInvalidPasswordAlg for the UPDATE path: buildUpdateQuery (called by
// the exported BuildUpdateQuery) is the third and last builder that emits the
// crypt($N, gen_salt('<PasswordAlg>')) SQL fragment via buildInsertPassword (see
// insert_query.go), alongside BuildInsertQuery and BuildBulkInsertQuery. It must validate
// PasswordAlg before building SQL too, instead of interpolating an attacker-controlled value.
func TestBuildUpdateQueryRejectsInvalidPasswordAlg(t *testing.T) {
	td, err := ConvertStructToTable("password_alg_update_accounts", reflect.TypeOf(passwordAlgUpdateAccount{}))
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}
	// td.PasswordHandler is intentionally left nil.

	primaryKey, ok := td.PrimaryKey()
	if !ok {
		t.Fatal("expected a primary key column")
	}

	value := passwordAlgUpdateAccount{
		ID:       "11111111-1111-1111-1111-111111111111",
		Email:    "a@example.com",
		Password: "secret",
	}

	original := PasswordAlg
	t.Cleanup(func() { PasswordAlg = original })

	PasswordAlg = `bf'); DROP TABLE x;--`
	if _, _, err := BuildUpdateQuery(value, nil, false, primaryKey); err == nil {
		t.Fatal("expected BuildUpdateQuery to reject a malicious PasswordAlg value, got nil error")
	}

	PasswordAlg = "bf"
	if _, _, err := BuildUpdateQuery(value, nil, false, primaryKey); err != nil {
		t.Fatalf("expected BuildUpdateQuery to succeed with a valid PasswordAlg, got: %v", err)
	}
}
