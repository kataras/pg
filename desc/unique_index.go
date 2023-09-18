package desc

// UniqueIndex is a struct that represents a unique index.
// See DB.ListUniqueIndexes method for more.
type UniqueIndex struct {
	TableName string   // table name
	IndexName string   // index name
	Columns   []string // column names.
}
