package pg

import (
	"context"
	"testing"
)

// This should match the CI's postgres version.
const expectedDBVersion = "15"

func TestInformation_GetVersion(t *testing.T) {
	db, err := openEmptyTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	version, err := db.GetVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if version != expectedDBVersion {
		t.Fatalf("expected version: %s but got: %s", expectedDBVersion, version)
	}
}
