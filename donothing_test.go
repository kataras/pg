package pg

import (
	"context"
	"testing"
)

// TestDoNothingConstantValue pins the DoNothing constant's exact value. desc.tagConflictTarget
// (desc/insert_query.go) can't reference this constant directly (importing the root package
// from desc would be an import cycle, since the root package imports desc), so it carries its
// own unexported copy (doNothingExpr) that must stay byte-identical to this one, or
// forceOnConflictExpr==DoNothing would silently stop being recognized as the DO NOTHING
// sentinel. This test is the tripwire for that drift: it only pins DoNothing itself (desc has no
// exported way to read back doNothingExpr), but every desc golden test that passes DoNothing's
// literal value ("DO NOTHING") already proves the two stay in sync in practice.
func TestDoNothingConstantValue(t *testing.T) {
	const want = "DO NOTHING"
	if DoNothing != want {
		t.Fatalf("DoNothing = %q, want %q", DoNothing, want)
	}
}

// TestUpsertSingleDoNothingReturning is a live-database test (see conflict_live_test.go's
// header comment for how to run it: PG_CONNSTRING and the same conflictScratchItem/
// conflictScratchTable/openConflictTestConnection/setupConflictScratchTable helpers) proving
// Repository.UpsertSingle(ctx, DoNothing, value, idPtr)'s corrected RETURNING/idPtr contract:
// a genuinely successful (non-conflicting) insert populates idPtr, and a DO NOTHING-skipped
// conflicting insert is reported as ErrNoRows instead of leaving idPtr unset with a nil error:
// the failure mode fix round 1 introduced (every call, successful or skipped, returned
// ErrNoRows, because the query never carried a RETURNING clause at all) and fix round 2 (see
// buildInsertQuery's alwaysReturning) resolves. conflictScratchItem carries no unique/
// unique_index Go struct tag, only the DB-side UNIQUE (a,b) constraint, so
// forceOnConflictExpr==DoNothing here exercises the target-less "ON CONFLICT DO NOTHING" form
// (see tagConflictTarget's "no tag-derived target" case), which still catches the (a,b)
// violation because Postgres's target-less DO NOTHING applies to a conflict against any unique
// constraint on the table, not just ones this package's tags can see.
func TestUpsertSingleDoNothingReturning(t *testing.T) {
	db, err := openConflictTestConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err = setupConflictScratchTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP TABLE IF EXISTS "+conflictScratchTable)

	repo := NewRepository[conflictScratchItem](db)

	var firstID int64
	if err := repo.UpsertSingle(ctx, DoNothing, conflictScratchItem{A: "x", B: "y", C: "v1"}, &firstID); err != nil {
		t.Fatalf("UpsertSingle (first insert, no conflict): %v", err)
	}
	if firstID == 0 {
		t.Fatal("expected a non-zero id from the first, non-conflicting insert")
	}

	var secondID int64
	err = repo.UpsertSingle(ctx, DoNothing, conflictScratchItem{A: "x", B: "y", C: "v2"}, &secondID)
	if !IsErrNoRows(err) {
		t.Fatalf("expected ErrNoRows for a DoNothing-skipped duplicate insert, got: %v", err)
	}
	if secondID != 0 {
		t.Fatalf("expected idPtr to stay unset (0) on a DoNothing-skipped insert, got %d", secondID)
	}

	items, err := repo.Select(ctx, "SELECT * FROM "+conflictScratchTable+" WHERE a = 'x' AND b = 'y'")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 row for (a,b)=('x','y'), got %d", len(items))
	}
	if items[0].C != "v1" {
		t.Fatalf("expected the DoNothing-skipped duplicate to leave c untouched (still v1), got %q", items[0].C)
	}
}
