package pg

import (
	"context"
)

// QuerySlice executes the given query and returns a list of T entries.
// Note that the rows scanner will directly scan an element of T, meaning
// that the type of T should be a database scannabled type (e.g. string, int, time.Time, etc.).
//
// The ErrNoRows is discarded, an empty list and a nil error will be returned instead.
// If a string column is empty then it's skipped from the returning list.
// Example:
//
//	names, err := QuerySlice[string](ctx, db, "SELECT name FROM users;")
func QuerySlice[T any](ctx context.Context, db *DB, query string, args ...any) ([]T, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var t T
	_, isString := any(t).(string)

	var list []T

	for rows.Next() {
		var entry T
		if err = rows.Scan(&entry); err != nil {
			return nil, err
		}

		if isString {
			if any(entry).(string) == "" {
				continue
			}
		}

		list = append(list, entry)
	}

	if err = rows.Err(); err != nil && err != ErrNoRows {
		return nil, err
	}

	return list, nil
}

// QueryTwoSlices executes the given query and returns two lists of T and V entries.
// Same behavior as QuerySlice but with two lists.
func QueryTwoSlices[T, V any](ctx context.Context, db *DB, query string, args ...any) ([]T, []V, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var (
		tList []T
		vList []V
	)
	for rows.Next() {
		var (
			t T
			v V
		)
		if err = rows.Scan(&t, &v); err != nil {
			return nil, nil, err
		}

		tList = append(tList, t)
		vList = append(vList, v)
	}

	if err = rows.Err(); err != nil && err != ErrNoRows {
		return nil, nil, err
	}

	return tList, vList, nil
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

func scanQuery[T any](ctx context.Context, db *DB, scanner func(rows Rows) (T, error), query string, args ...any) ([]T, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []T

	for rows.Next() {
		entry, err := scanner(rows)
		if err != nil {
			return nil, err
		}

		list = append(list, entry)
	}

	if err = rows.Err(); err != nil && err != ErrNoRows {
		return nil, err
	}

	return list, nil
}
