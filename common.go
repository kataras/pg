package pg

import (
	"context"
)

// QuerySlice executes the given query and returns a list of T entries.
// Note that the rows scanner will directly scan an element of T, meaning
// that the type of T should be a database scannabled type (e.g. string, int, time.Time, etc.).
//
// Example:
//
//	names, err := QuerySlice[string](ctx, db, "SELECT name FROM users;")
func QuerySlice[T any](ctx context.Context, db *DB, query string, args ...any) ([]T, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []T

	for rows.Next() {
		var entry T
		if err = rows.Scan(&entry); err != nil {
			return nil, err
		}

		list = append(list, entry)
	}

	return list, rows.Err()
}

// QuerySingle executes the given query and returns a single T entry.
//
// Example:
//
//	names, err := QuerySingle[MyType](ctx, db, "SELECT a_json_field FROM users;")
func QuerySingle[T any](ctx context.Context, db *DB, query string, args ...any) (entry T, err error) {
	err = db.QueryRow(ctx, query, args...).Scan(&entry)
	return
}
