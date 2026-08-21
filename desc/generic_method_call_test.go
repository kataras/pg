package desc

import (
	"reflect"
	"testing"
)

type genericMethodProbe struct {
	ID string `pg:"type=uuid,primary"`
}

// TestTableGenericMethodInstantiation compiles the exact call shapes the book and README show
// for the Go 1.27 generic methods on *Table, including explicit instantiation on the result of
// another call (the `repo.Table().RowsToStruct[T](rows)` form used in chapter 8's cursor
// example). The book is not compiled anywhere, so without this the syntax is only asserted.
func TestTableGenericMethodInstantiation(t *testing.T) {
	td, err := ConvertStructToTable("generic_method_probes", reflect.TypeFor[genericMethodProbe]())
	if err != nil {
		t.Fatalf("ConvertStructToTable: %v", err)
	}

	tableOf := func() *Table { return td }

	// Taking the methods as values proves they instantiate; calling them would need live
	// pgx.Rows, which belongs to the live tests.
	var (
		_ = td.RowsToStruct[genericMethodProbe]
		_ = td.RowToStruct[genericMethodProbe]
		_ = td.RowsToStructWithTotal[genericMethodProbe]
		_ = tableOf().RowsToStruct[genericMethodProbe]
	)
}
