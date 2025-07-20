package desc

import (
	"reflect"
	"regexp"
	"strings"

	"github.com/gertd/go-pluralize"
)

var (
	// ToStructName returns the struct name for the table name.
	// TODO: It can go to a NewTable function.
	ToStructName = func(tableName string) string { return PascalCase(Singular(tableName)) }
	// ToStructFieldName returns the struct field name for the column name.
	ToStructFieldName = func(columnName string) string { return PascalCase(columnName) }
	// ToColumnName returns the column name for the struct field.
	ToColumnName = func(field reflect.StructField) string { return SnakeCase(field.Name) }
)

var p = pluralize.NewClient()

func init() {
	p.AddIrregularRule("data", "data")
	p.AddSingularRule("*data", "data") // e.g. customer_health_data, we do NOT want it to become customer_health_datum.
}

// Singular returns the singular form of the given string.
func Singular(s string) string {
	s = p.Singular(s)
	return s
}

// SnakeCase converts a given string to a friendly snake case, e.g.
// - userId to user_id
// - ID     to id
// - ProviderAPIKey to provider_api_key
// - Option to option
// - URL to url
func SnakeCase(camel string) string {
	var (
		b            strings.Builder
		prevWasUpper bool
	)

	for i, c := range camel {
		if isUppercase(c) { // it's upper.
			if b.Len() > 0 && !prevWasUpper { // it's not the first and the previous was not uppercased too (e.g  "ID").
				b.WriteRune('_')
			} else { // check for XxxAPIKey, it should be written as xxx_api_key.
				next := i + 1
				if next > 1 && len(camel)-1 > next {
					if !isUppercase(rune(camel[next])) {
						b.WriteRune('_')
					}
				}
			}

			b.WriteRune(c - 'A' + 'a') // write its lowercase version.
			prevWasUpper = true
		} else {
			b.WriteRune(c) // write it as it is, it's already lowercased.
			prevWasUpper = false
		}
	}

	return b.String()
}

// isUppercase returns true if the given rune is uppercase.
func isUppercase(c rune) bool {
	return 'A' <= c && c <= 'Z'
}

// This should match id, api or url (case-insensitive)
// only if they are preceded or followed by either a word boundary or a non-word character.
var pascalReplacer = regexp.MustCompile(`(?i)(?:\b|[^a-z0-9])(id|api|url)(?:\b|[^a-z0-9])`)

// PascalCase converts a given string to a friendly pascal case, e.g.
// - user_id to UserID
// - id     to ID
// - provider_api_key to ProviderAPIKey
// - customer_provider to CustomerProvider
func PascalCase(snake string) string {
	var (
		b           strings.Builder
		shouldUpper bool
	)

	snake = pascalReplacer.ReplaceAllStringFunc(snake, strings.ToUpper)

	for i := range snake {
		c := rune(snake[i])

		if i >= len(snake)-1 { // it's the last character.
			b.WriteRune(c)
			break
		}

		if c == '_' { // it's a separator.
			shouldUpper = true // the next character should be uppercased.
		} else if isLowercase(c) { // it's lower.
			if b.Len() == 0 || shouldUpper { // it's the first character or it should be uppercased.
				b.WriteRune(c - 'a' + 'A') // write its uppercase version.
				shouldUpper = false
			} else {
				b.WriteRune(c) // write it as it is, it's already lowercased.
			}
		} else {
			b.WriteRune(c) // write it as it is, it's already uppercased.
			shouldUpper = false
		}
	}

	return b.String()
}

// isLowercase returns true if the given rune is lowercase.
func isLowercase(c rune) bool {
	return 'a' <= c && c <= 'z'
}
