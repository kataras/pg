package pg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
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

	if err = rows.Err(); err != nil && !errors.Is(err, ErrNoRows) {
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

	if err = rows.Err(); err != nil && !errors.Is(err, ErrNoRows) {
		return nil, nil, err
	}

	return tList, vList, nil
}

// QueryMap executes a two-column query and returns the rows as a map of the first
// column to the second; later duplicate keys overwrite earlier ones, and a query
// yielding no rows returns an empty non-nil map.
//
// Example:
//
//	idsByEmail, err := QueryMap[string, string](ctx, db, "SELECT email, id FROM users;")
func QueryMap[K comparable, V any](ctx context.Context, db *DB, query string, args ...any) (map[K]V, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[K]V)

	for rows.Next() {
		var (
			key K
			val V
		)
		if err = rows.Scan(&key, &val); err != nil {
			return nil, err
		}

		result[key] = val
	}

	if err = rows.Err(); err != nil && !errors.Is(err, ErrNoRows) {
		return nil, err
	}

	return result, nil
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

// ScanFunc converts the current position of rows (after a successful rows.Next()) into a
// value of type T; used by QueryFunc.
type ScanFunc[T any] func(rows Rows) (T, error)

// QueryFunc executes query and builds the result list by calling scan once per row: for row
// shapes that fit neither a single scannable value (QuerySlice) nor a registered struct
// (Repository.Select), e.g. a handful of columns combined into an ad-hoc T that scanning
// directly into &entry (as QuerySlice does) cannot express.
//
// As with QuerySlice, a query yielding no rows returns an empty (nil) list and a nil error.
//
// Example:
//
//	type nameAndCount struct {
//		Name  string
//		Count int64
//	}
//
//	rows, err := QueryFunc(ctx, db, func(rows pg.Rows) (nameAndCount, error) {
//		var nc nameAndCount
//		err := rows.Scan(&nc.Name, &nc.Count)
//		return nc, err
//	}, "SELECT name, COUNT(*) FROM users GROUP BY name;")
func QueryFunc[T any](ctx context.Context, db *DB, scan ScanFunc[T], query string, args ...any) ([]T, error) {
	return scanQuery(ctx, db, scan, query, args...)
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

	if err = rows.Err(); err != nil && !errors.Is(err, ErrNoRows) {
		return nil, err
	}

	return list, nil
}

// QuoteIdentifier sanitizes a string identifier for use in SQL queries.
func QuoteIdentifier(identifier string) string {
	return pgx.Identifier{identifier}.Sanitize()
}
