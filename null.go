package pg

// Ptr returns a pointer to v; convenient for optional (NULL-able) query arguments,
// since pgx v5 encodes a nil typed pointer (e.g. a nil *string or *int) as SQL NULL natively,
// without requiring a pgtype wrapper such as pgtype.Text or pgtype.UUID.
//
// Ptr is most useful for turning a literal or an already-non-pointer value into the pointer
// form a query argument needs, e.g.:
//
//	db.Exec(ctx, "UPDATE customers SET nickname = $1 WHERE id = $2", Ptr("bob"), id)
func Ptr[T any](v T) *T {
	return new(v)
}

// NullIfZero returns nil when v is the zero value of T, otherwise a pointer to v.
//
// It is meant for optional query parameters whose "unset" state is more naturally expressed
// as a Go zero value (e.g. "" for a string, 0 for an int, uuid.Nil for a UUID) than as an
// explicit pointer: NullIfZero(uuidString) binds SQL NULL for an empty uuid string instead of
// pgx (or the database) failing to parse it as a UUID, replacing pgtype.UUID{Valid: false} (and
// similar per-type pgtype escape hatches) with a single generic helper that relies on pgx v5's
// native nil-typed-pointer -> SQL NULL encoding.
func NullIfZero[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}

	return &v
}
