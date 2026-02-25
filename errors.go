package pg

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrNoRows is fired from a query when no results are came back.
	// Usually it's ignored and an empty json array is sent to the client instead.
	//
	// This error should be compared using errors.Is() or IsErrNoRows package-level function.
	ErrNoRows = pgx.ErrNoRows
)

// IsErrNoRows reports whether the error is ErrNoRows.
func IsErrNoRows(err error) bool {
	return errors.Is(err, ErrNoRows)
}

// IsErrDuplicate reports whether the return error from `Insert` method
// was caused because of a violation of a unique constraint (it's not typed error at the underline driver).
// It returns the constraint key if it's true.
func IsErrDuplicate(err error) (string, bool) {
	if err != nil {
		errText := err.Error()
		if strings.Contains(errText, "ERROR: duplicate key value violates unique constraint") {
			if startIdx := strings.IndexByte(errText, '"'); startIdx > 0 && startIdx+1 < len(errText) {
				errText = errText[startIdx+1:]
				if endIdx := strings.IndexByte(errText, '"'); endIdx > 0 && endIdx < len(errText) {
					return errText[:endIdx], true
				}
			}
		}
	}

	return "", false
}

// IsErrForeignKey reports whether an insert or update command failed due
// to an invalid foreign key: a foreign key is missing or its source was not found.
// E.g. ERROR: insert or update on table "food_user_friendly_units" violates foreign key constraint "fk_food" (SQLSTATE 23503)
func IsErrForeignKey(err error) (string, bool) {
	if err != nil {
		errText := err.Error()
		if strings.Contains(errText, "violates foreign key constraint") {
			if startIdx := strings.IndexByte(errText, '"'); startIdx > 0 && startIdx+1 < len(errText) {
				errText = errText[startIdx+1:]
				if endIdx := strings.IndexByte(errText, '"'); endIdx > 0 && endIdx < len(errText) {
					return errText[:endIdx], true
				}
			}
		}
	}
	return "", false
}

// IsErrInputSyntax reports whether the return error from `Insert` method
// was caused because of invalid input syntax for a specific postgres column type.
func IsErrInputSyntax(err error) (string, bool) {
	if err != nil {
		errText := err.Error()
		if strings.HasPrefix(errText, "ERROR: ") {
			if strings.Contains(errText, "ERROR: invalid input syntax for type") || strings.Contains(errText, "ERROR: syntax error in tsquery") || strings.Contains(errText, "ERROR: no operand in tsquery") {
				if startIdx := strings.IndexByte(errText, '"'); startIdx > 0 && startIdx+1 < len(errText) {
					errText = errText[startIdx+1:]
					if endIdx := strings.IndexByte(errText, '"'); endIdx > 0 && endIdx < len(errText) {
						return errText[:endIdx], true
					}
				} else {
					// more generic error.
					return "invalid input syntax", true
				}
			}
		}
	}

	return "", false
}

// IsErrColumnNotExists reports whether the error is caused because the "col" defined
// in a select query was not exists in a row.
// There is no a typed error available in the driver itself.
func IsErrColumnNotExists(err error, col string) bool {
	if err == nil {
		return false
	}

	errText := fmt.Sprintf(`column "%s" does not exist`, col)
	return strings.Contains(err.Error(), errText)
}
