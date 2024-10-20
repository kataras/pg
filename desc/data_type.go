package desc

import (
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"
)

// RegisterDataType registers a new data type with the given names.
// It panics if the data type or any of the names already exists.
// This function is used to extend the supported data types by custom ones.
func RegisterDataType(t DataType, names ...string) {
	if names, ok := dataTypeText[t]; ok {
		panic(fmt.Sprintf("DataType(%d) %s already exists", t, strings.Join(names, ", ")))
	}

	dataTypeText[t] = names
}

// DataType represents the sql data type for a column.
type DataType uint8

// Available data types.
const (
	InvalidDataType DataType = iota // InvalidDataType is a placeholder for an invalid data type.
	BigInt                          // BigInt represents a signed 64-bit integer.
	BigIntArray                     // BigIntArray represents an array of signed 64-bit integers.
	BigSerial                       // BigSerial represents a 64-bit auto-incrementing integer.
	Bit                             // Bit represents a fixed-length bit string.
	BitVarying                      // BitVarying represents a variable-length bit string.
	Boolean                         // Boolean represents a logical value (true or false).
	Box                             // Box represents a rectangular box on a plane.
	Bytea                           // Bytea represents a binary string (byte array).
	Character                       // Character represents a fixed-length character string.
	CharacterArray
	CharacterVarying      // CharacterVarying represents a variable-length character string.
	CharacterVaryingArray // CharacterVaryingArray represents an array of variable-length character strings.
	Cidr                  // Cidr represents an IPv4 or IPv6 network address.
	Circle                // Circle represents a circle on a plane.
	Date                  // Date represents a calendar date (year, month, day).
	DoublePrecision       // DoublePrecision represents a double-precision floating-point number (8 bytes).
	Inet                  // Inet represents an IPv4 or IPv6 host address.
	Integer               // Integer represents a signed 32-bit integer.
	IntegerArray          // IntegerArray represents an array of signed 32-bit integers.
	IntegerDoubleArray    // IntegerDoubleArray represents an array of arrays of signed 32-bit integers.
	Array                 // Array represents an array of any dimension.
	Interval              // Interval represents a time span.
	JSON                  // JSON represents a JSON data structure in text format.
	JSONB                 // JSONB represents a JSON data structure in binary format.
	Line                  // Line represents an infinite line on a plane.
	Lseg                  // Lseg represents a line segment on a plane.
	MACAddr               // MACAddr represents a MAC (Media Access Control) address (6 bytes).
	MACAddr8              // MACAddr8 represents an EUI-64 MAC address (8 bytes).
	Money                 // Money represents a currency amount with fixed fractional precision.
	Numeric               // Numeric represents an exact numeric value with variable precision and scale.
	Path                  // Path represents a geometric path on a plane. It can be open or closed, and can have multiple subpaths.
	PgLSN                 // PgLSN represents a PostgreSQL Log Sequence Number, which is used to identify log positions in the write-ahead log (WAL).
	Point                 // Point represents a geometric point on a plane. It has two coordinates: x and y.
	Polygon               // Polygon represents a closed geometric path on a plane. It consists of one or more points that form the vertices of the polygon.
	Real                  // Real represents a single-precision floating-point number (4 bytes).
	SmallInt              // SmallInt represents a signed 16-bit integer.
	SmallSerial           // SmallSerial represents a 16-bit auto-incrementing integer.
	Serial                // Serial represents a 32-bit auto-incrementing integer.
	Text                  // Text represents a variable-length character string with unlimited length.
	TextArray             // TextArray represents an array of variable-length character strings with unlimited length.
	TextDoubleArray       // TextDoubleArray represents an array of arrays of variable-length character strings with unlimited length.
	Time                  // Time represents the time of day (without time zone).
	TimeTZ                // TimeTZ represents the time of day (with time zone).
	Timestamp             // Timestamp represents the date and time of day (without time zone).
	TimestampTZ           // TimestampTZ represents the date and time of day (with time zone).
	TsQuery               // TsQuery represents a text search query that can be used to search tsvector values.
	TsVector              // TsVector represents a text search document that contains lexemes and their positions.
	TxIDSnapshot          // TxIDSnapshot represents the state of transactions at some point in time. It can be used to implement multiversion concurrency control (MVCC).
	UUID                  // UUID represents an universally unique identifier (16 bytes).
	UUIDArray             // UUIDArray represents an array of universally unique identifiers (16 bytes).
	XML                   // XML represents an XML data structure in text format.
	// Multiranges: https://www.postgresql.org/docs/14/rangetypes.html#RANGETYPES-BUILTIN.
	Int4Range // Range of integer, int4multirange.
	Int4MultiRange
	Int8Range // Range of bigint, int8multirange.
	Int8MultiRange
	NumRange // Range of numeric, nummultirange.
	NumMultiRange
	TsRange // Range of timestamp without time zone, tsmultirange.
	TsMultirange
	TsTzRange // Range of timestamp with time zone, tstzmultirange.
	TsTzMultiRange
	DateRange // Range of date, datemultirange.
	DateMultiRange
	//
	CIText // CIText is an extension data type that provides case-insensitive
	HStore // hstore is an extension data type that provides key-value into a single column.
)

var dataTypeText = map[DataType][]string{ // including their aliases.
	BigInt:                {"bigint", "int8"},
	BigIntArray:           {"bigint[]", "int8[]"},
	BigSerial:             {"bigserial", "serial8"},
	Bit:                   {"bit"},
	BitVarying:            {"varbit", "bit varying"},
	Boolean:               {"boolean", "bool"},
	Box:                   {"box"},
	Bytea:                 {"bytea"},
	Character:             {"character", "char"},
	CharacterArray:        {"character[]", "char[]"},
	CharacterVarying:      {"varchar", "character varying"}, //  "varying[]",
	CharacterVaryingArray: {"varchar[]", "character varying[]"},
	Cidr:                  {"cidr"},
	Circle:                {"circle"},
	Date:                  {"date"},
	DoublePrecision:       {"float8", "double precision"},
	Inet:                  {"inet"},
	Integer:               {"int", "int4", "integer"},
	IntegerArray:          {"int[]", "int4[]", "integer[]"},
	IntegerDoubleArray:    {"int[][]", "int4[][]", "integer[][]"},
	Array:                 {"array"},
	Interval:              {"interval"},
	JSON:                  {"json"},
	JSONB:                 {"jsonb"},
	Line:                  {"line"},
	Lseg:                  {"lseg"},
	MACAddr:               {"macaddr"},
	MACAddr8:              {"macaddr8"},
	Money:                 {"money"},
	Numeric:               {"numeric", "decimal"},
	Path:                  {"path"},
	PgLSN:                 {"pg_lsn"},
	Point:                 {"point"},
	Polygon:               {"polygon"},
	Real:                  {"real", "float4"},
	SmallInt:              {"smallint", "int2"},
	SmallSerial:           {"smallserial", "serial2"},
	Serial:                {"serial4"},
	Text:                  {"text"},
	TextArray:             {"text[]"},
	TextDoubleArray:       {"text[][]"},
	Time:                  {"time", "time without time zone", "time(6) without time zone"},
	TimeTZ:                {"timetz", "time with time zone", "time(6) with time zone"},
	Timestamp:             {"timestamp", "timestamp without time zone", "timestamp(6) without time zone"},
	TimestampTZ:           {"timestamptz", "timestamp with time zone", "timestamp(6) with time zone"},
	TsQuery:               {"tsquery"},
	TsVector:              {"tsvector"},
	TxIDSnapshot:          {"txid_snapshot"},
	UUID:                  {"uuid"},
	UUIDArray:             {"uuid[]"},
	XML:                   {"xml"},
	Int4Range:             {"int4range"},
	Int4MultiRange:        {"int4multirange"},
	Int8Range:             {"int8range"},
	Int8MultiRange:        {"int8multirange"},
	NumRange:              {"numrange"},
	NumMultiRange:         {"nummultirange"},
	TsRange:               {"tsrange"},
	TsMultirange:          {"tsmultirange"},
	TsTzRange:             {"tstzrange"},
	TsTzMultiRange:        {"tstzmultirange"},
	DateRange:             {"daterange"},
	DateMultiRange:        {"datemultirange"},
	CIText:                {"citext"}, // case-insensitive text using the citext extension: CREATE EXTENSION citext;
	HStore:                {"hstore"},
}

// String returns the first name of the data type, or a formatted string if the data type is unexpected.
func (t DataType) String() string {
	if names, ok := dataTypeText[t]; ok {
		return names[0]
	}

	return fmt.Sprintf("DataType(unexpected %d)", t)
}

// IsString returns true if the given string matches any of the names of the data type, ignoring case and whitespace.
func (t DataType) IsString(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == Array.String() {
		return t.IsArray()
	}

	if names, ok := dataTypeText[t]; ok {
		for _, name := range names {
			if s == name {
				return true
			}
		}
	}

	return false
}

// IsArray returns true if the data type is an array.
func (t DataType) IsArray() bool {
	switch t {
	case BigIntArray, IntegerArray, IntegerDoubleArray, CharacterArray, CharacterVaryingArray, TextArray, TextDoubleArray, UUIDArray:
		return true
	default:
		return false
	}
}

// IsTime returns true if the data type is a time type.
func (t DataType) IsTime() bool {
	switch t {
	case Time, TimeTZ, Timestamp, TimestampTZ:
		return true
	default:
		return false
	}
}

// GoType returns the Go type for the data type.
func (t DataType) GoType() reflect.Type {
	return dataTypeToGoType(t)
}

// ParseDataType converts a string to a DataType value, ignoring case and parentheses.
// It returns Invalid if the string does not match any of the registered data types.
func ParseDataType(s string) (DataType, string) {
	s = strings.ToLower(s)

	var typeArgument string
	if idx := strings.LastIndexByte(s, ')'); idx+1 != len(s) {
		// argument is not the last part, we assume it's timestamp(6) without time zone,
		// so skip type argument.
	} else {
		if idx := strings.IndexByte(s, '('); idx != -1 {
			// Keep the type argument first.
			typeArgument = s[idx+1 : len(s)-1]
			// Remove type's argument, results at:
			// varchar(255) == varchar.
			s = s[0:idx]
		}
	}

	// remove array type's argument, results at, invalid:
	// integer[] == array.
	// if idx := strings.IndexByte(s, leftBracketLiteral); idx != -1 {
	// 	s = s[0:idx]
	// }

	for t, names := range dataTypeText {
		for _, name := range names {
			if s == name {
				return t, typeArgument
			}
		}
	}

	return InvalidDataType, ""
}

var (
	stringType            = reflect.TypeOf("")
	bytesType             = reflect.TypeOf([]byte{})
	intType               = reflect.TypeOf(int(0))
	int32Type             = reflect.TypeOf(int32(0))
	int64Type             = reflect.TypeOf(int64(0))
	uint16Type            = reflect.TypeOf(uint16(0))
	uint32Type            = reflect.TypeOf(uint32(0))
	uint64Type            = reflect.TypeOf(uint64(0))
	float32Type           = reflect.TypeOf(float32(0))
	float64Type           = reflect.TypeOf(float64(0))
	timeType              = reflect.TypeOf(time.Time{})
	ipTyp                 = reflect.TypeOf(net.IP{})
	jsonNumberTyp         = reflect.TypeOf(json.Number(""))
	stringerTyp           = reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
	arrayIntegerTyp       = reflect.TypeOf([]int{})
	arrayStringTyp        = reflect.TypeOf([]string{})
	doubleArrayIntegerTyp = reflect.TypeOf([][]int{})
	doubleArrayStringTyp  = reflect.TypeOf([][]string{})
	booleanTyp            = reflect.TypeOf(false)
	timeDurationTyp       = reflect.TypeOf(time.Duration(0))
	timeDurationArrayTyp  = reflect.TypeOf([]time.Duration{})
)

// goTypeToDataType takes a reflect.Type that represents a Go type
// and returns a DataType that represents a corresponding data type for the database.
func goTypeToDataType(typ reflect.Type) DataType {
	switch typ {
	case stringType:
		return Text
	case ipTyp, bytesType:
		return Bytea
	case intType, int32Type, int64Type, uint32Type, uint64Type:
		return Integer
	case uint16Type, uint32Type:
		return SmallInt
	case float32Type, float64Type, jsonNumberTyp:
		return Numeric
	case arrayIntegerTyp:
		return IntegerArray
	case doubleArrayIntegerTyp:
		return IntegerDoubleArray
	case arrayStringTyp:
		return CharacterVaryingArray
	case doubleArrayStringTyp:
		return TextDoubleArray
	case booleanTyp:
		return Boolean
	case timeType:
		return Timestamp
	case timeDurationTyp:
		return Interval
	case timeDurationArrayTyp:
		return BigIntArray
	default:
		if typ.Implements(stringerTyp) {
			return Text
		}

		return InvalidDataType
	}
}

func dataTypeToGoType(dataType DataType) reflect.Type {
	switch dataType {
	case Text, CharacterVarying, UUID:
		return stringType
	case Bytea:
		return bytesType
	case Integer, Serial, BigSerial, BigInt:
		return int64Type
	case SmallInt:
		return uint16Type
	case Numeric:
		return float64Type
	case IntegerArray:
		return arrayIntegerTyp
	case IntegerDoubleArray:
		return doubleArrayIntegerTyp
	case CharacterVaryingArray, TextArray, UUIDArray:
		return arrayStringTyp
	case Boolean:
		return booleanTyp
	case TextDoubleArray:
		return doubleArrayStringTyp
	case Time, TimeTZ, Timestamp, TimestampTZ:
		return timeType
	case Interval:
		return timeDurationTyp
	case BigIntArray:
		return timeDurationArrayTyp
	case TsVector:
		return stringType
	default:
		return nil
	}
}
