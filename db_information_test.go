package pg

import (
	"context"
	"strings"
	"testing"

	"github.com/kataras/pg/desc"
)

// This should match the CI's postgres major version (see .github/workflows/ci.yml).
const expectedDBVersion = "16"

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

	major, _, _ := strings.Cut(version, ".")
	if major != expectedDBVersion {
		t.Fatalf("expected major version: %s but got: %s (full: %s)", expectedDBVersion, major, version)
	}
}

func TestTolerateUndeclaredUniqueMemberIndex(t *testing.T) {
	tests := []struct {
		name          string
		dbColumn      *desc.Column
		codeColumn    *desc.Column
		expectedIndex desc.IndexType // the db column's Index after the call.
	}{
		{
			name:          "undeclared single-column index on a unique index member is tolerated",
			dbColumn:      &desc.Column{Name: "food_id", Index: desc.Btree, UniqueIndex: "allergen"},
			codeColumn:    &desc.Column{Name: "food_id", UniqueIndex: "allergen"},
			expectedIndex: desc.InvalidIndex,
		},
		{
			name:          "declared index on a unique index member is still verified",
			dbColumn:      &desc.Column{Name: "customer_id", Index: desc.Btree, UniqueIndex: "customer_food_preference"},
			codeColumn:    &desc.Column{Name: "customer_id", Index: desc.Btree, UniqueIndex: "customer_food_preference"},
			expectedIndex: desc.Btree,
		},
		{
			name:          "mismatched declared index types remain visible",
			dbColumn:      &desc.Column{Name: "name_search_tokens", Index: desc.Gin, UniqueIndex: "uq_tokens"},
			codeColumn:    &desc.Column{Name: "name_search_tokens", Index: desc.Btree, UniqueIndex: "uq_tokens"},
			expectedIndex: desc.Gin,
		},
		{
			name:          "column outside of a unique index keeps its strict check",
			dbColumn:      &desc.Column{Name: "blog_id", Index: desc.Btree},
			codeColumn:    &desc.Column{Name: "blog_id"},
			expectedIndex: desc.Btree,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tolerateUndeclaredUniqueMemberIndex(tt.dbColumn, tt.codeColumn)

			if got := tt.dbColumn.Index; got != tt.expectedIndex {
				t.Fatalf("expected db column index: %s but got: %s", tt.expectedIndex.String(), got.String())
			}
		})
	}
}
