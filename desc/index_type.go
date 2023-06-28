package desc

import (
	"fmt"
	"strings"
)

// IndexType is an enumeration type that represents different types of indexes in a database.
type IndexType uint8

// These are the possible values for IndexType.
const (
	InvalidIndex IndexType = iota // InvalidIndex is the zero value for IndexType and indicates an invalid or unknown index type
	Btree                         // Btree is an index type that uses a balanced tree data structure
	Hash                          // Hash is an index type that uses a hash table data structure
	Gist                          // Gist is an index type that supports generalized search trees for various data types
	Spgist                        // Spgist is an index type that supports space-partitioned generalized search trees for various data types
	Gin                           // Gin is an index type that supports inverted indexes for various data types
	Brin                          // Brin is an index type that supports block range indexes for large tables
)

// indexTypeText is a map from IndexType to its string representation.
var indexTypeText = map[IndexType]string{
	Btree:  "btree",
	Hash:   "hash",
	Gist:   "gist",
	Spgist: "spgist",
	Gin:    "gin",
	Brin:   "brin",
}

// String returns the string representation of an IndexType value.
func (t IndexType) String() string {
	if name, ok := indexTypeText[t]; ok {
		return name // if the value is in the map, return the corresponding name
	}

	return fmt.Sprintf("IndexType(unexpected %d)", t) // otherwise, return a formatted string with the numeric value
}

func (t *IndexType) Scan(src interface{}) error {
	if src == nil {
		return nil
	}

	s, ok := src.(string)
	if !ok {
		return fmt.Errorf("index type: unknown type of: %T", src)
	}

	if s == "" { // allow empty strings to be scanned as nil.
		return nil
	}

	for k, v := range indexTypeText {
		if v == s {
			*t = k
			return nil
		}
	}

	return fmt.Errorf("index type: unknown value of: %s", s)
}

// parseIndexType takes a string and returns the corresponding IndexType value.
func parseIndexType(s string) IndexType {
	s = strings.ToLower(s) // convert the string to lower case for case-insensitive comparison
	for t, name := range indexTypeText {
		if s == name {
			return t // if the string matches a name in the map, return the corresponding value
		}
	}

	return InvalidIndex // otherwise, return InvalidIndex to indicate an invalid or unknown index type
}
