package pg

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// asPgError reports whether err wraps a *pgconn.PgError (checked with errors.As,
// so it also matches errors wrapped multiple levels deep, e.g. via fmt.Errorf's
// %w) and, if so, returns that PgError.
//
// It is intentionally small and generic on purpose: the classification helpers
// below use it to prefer the driver's stable SQLSTATE Code over parsing English
// error text, and it is meant to be reused by other error classification
// helpers in this package.
func asPgError(err error) (*pgconn.PgError, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr, true
	}

	return nil, false
}

// extractQuoted returns the substring between the first pair of double quotes
// found in text (e.g. pulling `customers_email_key` out of `duplicate key
// value violates unique constraint "customers_email_key"`), mirroring the
// quote-scan historically used to pull an offending value out of a PostgreSQL
// error message.
//
// If text has no opening quote it reports ("invalid input syntax", true) as a
// generic fallback value. If it has an opening quote but no matching closing
// quote, it reports ("", false).
func extractQuoted(text string) (string, bool) {
	if startIdx := strings.IndexByte(text, '"'); startIdx > 0 && startIdx+1 < len(text) {
		text = text[startIdx+1:]
		if endIdx := strings.IndexByte(text, '"'); endIdx > 0 && endIdx < len(text) {
			return text[:endIdx], true
		}

		return "", false
	}

	return "invalid input syntax", true
}

// ConstraintKind classifies which class of constraint a ConstraintError violated, based on
// the SQLSTATE reported by PostgreSQL. It is empty for a class-23 (integrity-constraint
// violation) SQLSTATE that is not one of the five specific codes below (e.g. "23000",
// the generic integrity_constraint_violation code).
type ConstraintKind string

const (
	// ConstraintUnique is the ConstraintKind for SQLSTATE 23505 (unique_violation): a row
	// would duplicate an existing value in a unique index or unique constraint.
	ConstraintUnique ConstraintKind = "unique"
	// ConstraintForeignKey is the ConstraintKind for SQLSTATE 23503 (foreign_key_violation):
	// a row references a value that does not exist in the referenced table, or a referenced
	// row was deleted/updated while still referenced.
	ConstraintForeignKey ConstraintKind = "foreign_key"
	// ConstraintNotNull is the ConstraintKind for SQLSTATE 23502 (not_null_violation): a
	// column declared NOT NULL was given a null value.
	ConstraintNotNull ConstraintKind = "not_null"
	// ConstraintCheck is the ConstraintKind for SQLSTATE 23514 (check_violation): a row
	// failed a CHECK constraint.
	ConstraintCheck ConstraintKind = "check"
	// ConstraintExclusion is the ConstraintKind for SQLSTATE 23P01 (exclusion_violation): a
	// row conflicted with an existing row under an EXCLUDE constraint.
	ConstraintExclusion ConstraintKind = "exclusion"
)

// constraintKindsByCode maps the SQLSTATE codes with a known, named ConstraintKind to that
// kind. Any other SQLSTATE beginning with "23" is still an integrity-constraint violation
// (and AsConstraintError still returns ok=true for it) but its Kind is left empty.
var constraintKindsByCode = map[string]ConstraintKind{
	"23505": ConstraintUnique,
	"23503": ConstraintForeignKey,
	"23502": ConstraintNotNull,
	"23514": ConstraintCheck,
	"23P01": ConstraintExclusion,
}

// ConstraintError is a typed view over a PostgreSQL integrity-constraint violation
// (SQLSTATE class 23, e.g. a duplicate key, a missing foreign key or a failed CHECK),
// extracted from the driver's *pgconn.PgError so callers can stop parsing error text
// to figure out what went wrong.
//
// AsConstraintError is the only way to obtain a *ConstraintError; the library itself
// never wraps its returned errors in one (see AsConstraintError's doc for why).
type ConstraintError struct {
	// Kind classifies the violation as one of the ConstraintKind constants above.
	// It is empty for a class-23 SQLSTATE that isn't one of those five specific codes.
	Kind ConstraintKind
	// ConstraintName is the name of the violated constraint or index, e.g.
	// "customers_email_key". It may be empty if PostgreSQL did not report one.
	ConstraintName string
	// TableName is the name of the table the violation occurred on. It may be empty
	// if PostgreSQL did not report one.
	TableName string
	// ColumnName is the name of the offending column. PostgreSQL only populates this
	// for not-null violations (ConstraintNotNull); it is empty for the other kinds.
	ColumnName string
	// Detail is the server's DETAIL line, e.g. `Key (email)=(x@y.com) already exists.`
	// for a unique violation. It may be empty if PostgreSQL did not report one.
	Detail string
	// Code is the raw SQLSTATE reported by PostgreSQL, e.g. "23505".
	Code string

	cause *pgconn.PgError
}

// Error returns a compact, single-line description of the violation, including its kind
// (when known), constraint name, table and DETAIL text, e.g.:
//
//	unique constraint "customers_email_key" on table "customers": Key (email)=(x@y.com) already exists.
func (e *ConstraintError) Error() string {
	var b strings.Builder

	if e.Kind != "" {
		b.WriteString(string(e.Kind))
		b.WriteString(" constraint")
	} else {
		b.WriteString("constraint")
	}

	if e.ConstraintName != "" {
		fmt.Fprintf(&b, " %q", e.ConstraintName)
	}

	if e.TableName != "" {
		fmt.Fprintf(&b, " on table %q", e.TableName)
	}

	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}

	return b.String()
}

// Unwrap returns the underlying *pgconn.PgError this ConstraintError was extracted from,
// so that errors.As(err, &pgErr) still reaches it through a returned *ConstraintError.
func (e *ConstraintError) Unwrap() error {
	return e.cause
}

// AsConstraintError reports whether err (or anything it wraps, per errors.As) is a
// PostgreSQL integrity-constraint violation (i.e. a *pgconn.PgError whose SQLSTATE Code
// begins with "23") and, if so, returns its typed *ConstraintError form.
//
// Extraction-only: this package never wraps the errors it returns in ConstraintError, so
// callers get one only by calling AsConstraintError themselves, at whichever layer maps
// database errors to a response (an HTTP handler, a GraphQL resolver, etc.) instead of
// hand-parsing error text at that layer.
func AsConstraintError(err error) (*ConstraintError, bool) {
	pgErr, ok := asPgError(err)
	if !ok || !strings.HasPrefix(pgErr.Code, "23") {
		return nil, false
	}

	return &ConstraintError{
		Kind:           constraintKindsByCode[pgErr.Code],
		ConstraintName: pgErr.ConstraintName,
		TableName:      pgErr.TableName,
		ColumnName:     pgErr.ColumnName,
		Detail:         pgErr.Detail,
		Code:           pgErr.Code,
		cause:          pgErr,
	}, true
}

// IsErrDuplicate reports whether the return error from `Insert` method
// was caused because of a violation of a unique constraint (it's not typed error at the underline driver).
// It returns the constraint key if it's true.
//
// Classification is SQLSTATE-based: if err wraps a *pgconn.PgError, it delegates to
// AsConstraintError and reports true if and only if the resulting Kind is
// ConstraintUnique (SQLSTATE 23505), in which case the structured ConstraintName field is
// returned directly. For errors that do not wrap a *pgconn.PgError (e.g. already-formatted
// strings, or errors from older/other drivers), it falls back to the original English
// message-text substring extraction.
func IsErrDuplicate(err error) (string, bool) {
	if err == nil {
		return "", false
	}

	if _, ok := asPgError(err); ok {
		if constraintErr, ok := AsConstraintError(err); ok && constraintErr.Kind == ConstraintUnique {
			return constraintErr.ConstraintName, true
		}

		return "", false
	}

	errText := err.Error()
	if strings.Contains(errText, "ERROR: duplicate key value violates unique constraint") {
		if startIdx := strings.IndexByte(errText, '"'); startIdx > 0 && startIdx+1 < len(errText) {
			errText = errText[startIdx+1:]
			if endIdx := strings.IndexByte(errText, '"'); endIdx > 0 && endIdx < len(errText) {
				return errText[:endIdx], true
			}
		}
	}

	return "", false
}

// IsErrForeignKey reports whether an insert or update command failed due
// to an invalid foreign key: a foreign key is missing or its source was not found.
// E.g. ERROR: insert or update on table "food_user_friendly_units" violates foreign key constraint "fk_food" (SQLSTATE 23503)
//
// Classification is SQLSTATE-based: if err wraps a *pgconn.PgError, it delegates to
// AsConstraintError and reports true if and only if the resulting Kind is
// ConstraintForeignKey (SQLSTATE 23503), in which case the structured ConstraintName field
// is returned directly. For errors that do not wrap a *pgconn.PgError, it falls back to the
// original English message-text substring extraction.
func IsErrForeignKey(err error) (string, bool) {
	if err == nil {
		return "", false
	}

	if _, ok := asPgError(err); ok {
		if constraintErr, ok := AsConstraintError(err); ok && constraintErr.Kind == ConstraintForeignKey {
			return constraintErr.ConstraintName, true
		}

		return "", false
	}

	errText := err.Error()
	if strings.Contains(errText, "violates foreign key constraint") {
		if startIdx := strings.IndexByte(errText, '"'); startIdx > 0 && startIdx+1 < len(errText) {
			errText = errText[startIdx+1:]
			if endIdx := strings.IndexByte(errText, '"'); endIdx > 0 && endIdx < len(errText) {
				return errText[:endIdx], true
			}
		}
	}
	return "", false
}

// IsErrInputSyntax reports whether the return error from `Insert` method
// was caused because of invalid input syntax for a specific postgres column type.
//
// Classification is SQLSTATE-based: if err wraps a *pgconn.PgError, it is
// treated as an input-syntax error when its Code is "22P02"
// (invalid_text_representation), extracting the quoted offending value out of
// the structured Message field (or a generic "invalid input syntax" when no
// quoted value is present). Postgres also reports tsquery syntax errors under
// the much more generic "42601" (syntax_error) SQLSTATE, so the historical
// tsquery message-text checks are still applied against PgError.Message when a
// PgError is available. For errors that do not wrap a *pgconn.PgError, it
// falls back to the original whole-text checks and quote-scan.
func IsErrInputSyntax(err error) (string, bool) {
	if err == nil {
		return "", false
	}

	if pgErr, ok := asPgError(err); ok {
		switch {
		case pgErr.Code == "22P02":
			return extractQuoted(pgErr.Message)
		case strings.Contains(pgErr.Message, "syntax error in tsquery") || strings.Contains(pgErr.Message, "no operand in tsquery"):
			return extractQuoted(pgErr.Message)
		default:
			return "", false
		}
	}

	errText := err.Error()
	if strings.HasPrefix(errText, "ERROR: ") {
		if strings.Contains(errText, "ERROR: invalid input syntax for type") || strings.Contains(errText, "ERROR: syntax error in tsquery") || strings.Contains(errText, "ERROR: no operand in tsquery") {
			return extractQuoted(errText)
		}
	}

	return "", false
}

// IsErrRetryableTx reports whether err is a transient transaction failure, SQLSTATE 40001
// (serialization_failure) or 40P01 (deadlock_detected), that is safe to retry from scratch
// in a fresh transaction. It is the default classifier InTransactionRetry uses when
// RetryOptions.IsRetryable is nil (see retry.go).
//
// Classification is SQLSTATE-based via the shared asPgError helper: err must wrap a
// *pgconn.PgError (checked with errors.As, so it also matches errors wrapped multiple levels
// deep, e.g. one that surfaced from tx.Commit rather than from one of the transaction's own
// statements) whose Code is exactly "40001" or "40P01". Both codes describe PostgreSQL
// detecting, after the fact, that this transaction cannot be allowed to complete as if it had
// run alone. The cause is a serializable (or repeatable-read) transaction that lost a write
// skew/read-write conflict race, or a transaction chosen as the victim to break a deadlock cycle,
// rather than anything wrong with the statements themselves, which is exactly the situation
// where simply retrying the whole transaction, from a clean starting snapshot, is expected to
// succeed. No other SQLSTATE is treated as retryable here, including the rest of PostgreSQL's
// own "40" (transaction_rollback) class, such as 40000 (transaction_rollback, the generic
// code) or 40003 (statement_completion_unknown). Retrying those is not generally safe, so
// callers who want to retry on additional conditions need their own
// RetryOptions.IsRetryable that also consults IsErrRetryableTx, rather than relying on this
// function to grow more codes over time.
func IsErrRetryableTx(err error) bool {
	pgErr, ok := asPgError(err)
	if !ok {
		return false
	}

	switch pgErr.Code {
	case "40001", "40P01":
		return true
	default:
		return false
	}
}

// IsErrColumnNotExists reports whether the error is caused because the "col" defined
// in a select query was not exists in a row.
// There is no a typed error available in the driver itself.
//
// Classification is SQLSTATE-based: if err wraps a *pgconn.PgError, it reports
// true only when its Code is "42703" (undefined_column) and its structured
// Message field mentions the given column name. For errors that do not wrap a
// *pgconn.PgError, it falls back to the original whole-text substring check.
func IsErrColumnNotExists(err error, col string) bool {
	if err == nil {
		return false
	}

	wantText := fmt.Sprintf(`column "%s" does not exist`, col)

	if pgErr, ok := asPgError(err); ok {
		return pgErr.Code == "42703" && strings.Contains(pgErr.Message, wantText)
	}

	return strings.Contains(err.Error(), wantText)
}
