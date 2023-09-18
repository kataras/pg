package desc

import "testing"

func TestBuildDuplicateQuery(t *testing.T) {
	td := &Table{
		SearchPath: "public",
		Name:       "test",
	}

	td.AddColumns(
		&Column{
			Name:       "id",
			PrimaryKey: true,
			Type:       UUID,
			Default:    genRandomUUIDPGCryptoFunction1,
		},
		&Column{
			Name:    "created_at",
			Type:    Timestamp,
			Default: "clock_timestamp()",
		},
		&Column{
			Name:                "source_id",
			Type:                UUID,
			ReferenceTableName:  "test",
			ReferenceColumnName: "id",
		},
		&Column{
			Name:         "name",
			Type:         BitVarying,
			TypeArgument: "255",
		},
	)

	var newID string
	query, err := BuildDuplicateQuery(td, &newID)
	if err != nil {
		t.Fatal(err)
	}

	expected := `INSERT INTO "public"."test" (source_id,name) SELECT COALESCE(source_id, id),name FROM "public"."test" WHERE id = $1 RETURNING id;`
	if query != expected {
		t.Logf("expected duplicated query (returning id) to match: %s, but got: %s", expected, query)
	}

	queryNoReturningID, err := BuildDuplicateQuery(td, nil)
	if err != nil {
		t.Fatal(err)
	}

	expectedyNoReturningID := `INSERT INTO "public"."test" (source_id,name) SELECT COALESCE(source_id, id),name FROM "public"."test" WHERE id = $1;`
	if queryNoReturningID != expectedyNoReturningID {
		t.Logf("expected duplicated query (no returning id) to match: %s, but got: %s", expected, query)
	}
}
