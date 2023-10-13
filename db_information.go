package pg

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/kataras/pg/desc"
)

// CreateSchema creates the database schema by executing a series of SQL commands in a transaction.
func (db *DB) CreateSchema(ctx context.Context) error {
	createDumpSQL, err := db.CreateSchemaDumpSQL(ctx)
	if err != nil {
		return err
	}

	return db.InTransaction(ctx, func(db *DB) error {
		_, err = db.Exec(ctx, createDumpSQL)
		if err != nil {
			return fmt.Errorf("%w:\n%s", err, createDumpSQL)
		}

		return nil
	})
}

type sqlDumperFunc func(context.Context, *strings.Builder) error

// CreateSchemaDumpSQL dumps the SQL commands for creating the database schema.
func (db *DB) CreateSchemaDumpSQL(ctx context.Context) (string, error) {
	var dumpers = []sqlDumperFunc{
		db.createDatabaseSchemaDump,
		db.createExtensionsDump,
		db.createTablesDump,
		db.createFunctionsAndTriggersDump,
	}

	b := new(strings.Builder)
	for _, d := range dumpers {
		if err := d(ctx, b); err != nil {
			return "", err
		}
	}

	return b.String(), nil
}

// createDatabaseSchema creates the database schema.
func (db *DB) createDatabaseSchemaDump(_ context.Context, b *strings.Builder) error {
	query := `CREATE SCHEMA IF NOT EXISTS ` + db.searchPath + `;`
	b.WriteString(query)
	return nil
}

// createExtensions creates the necessary PostgreSQL extensions for the database schema.
func (db *DB) createExtensionsDump(_ context.Context, b *strings.Builder) error {
	if db.schema.HasColumnType(desc.UUID) || db.schema.HasPassword() {
		query := `CREATE EXTENSION IF NOT EXISTS pgcrypto;`
		b.WriteString(query)
	}

	if db.schema.HasColumnType(desc.CIText) {
		query := `CREATE EXTENSION IF NOT EXISTS citext;`
		b.WriteString(query)
	}

	if db.schema.HasColumnType(desc.HStore) {
		query := `CREATE EXTENSION IF NOT EXISTS hstore;`
		b.WriteString(query)
	}

	return nil
}

// createTables creates the tables for the database schema.
func (db *DB) createTablesDump(ctx context.Context, b *strings.Builder) error {
	tables := db.schema.Tables(desc.TableTypeBase)
	if len(tables) == 0 {
		return nil // if no tables are defined, there is nothing to create.
	}

	for _, td := range tables {
		if err := db.createTableDump(ctx, b, td); err != nil {
			return fmt.Errorf("%s: %w", td.Name, err)
		}
	}

	// 2nd loop, so order doesn't matter.
	for _, td := range tables {
		if err := db.createTableForeignKeysDump(ctx, b, td); err != nil {
			return fmt.Errorf("%s: foreign keys: %w", td.Name, err)
		}
	}

	return nil
}

// createTable creates a table in the database according to the given table definition.
func (db *DB) createTableDump(_ context.Context, b *strings.Builder, td *desc.Table) error {
	if td.IsReadOnly() {
		return nil
	}

	query := desc.BuildCreateTableQuery(td)
	b.WriteString(query)
	return nil
}

// createTableForeignKeys creates the foreign keys for the given table definition.
func (db *DB) createTableForeignKeysDump(_ context.Context, b *strings.Builder, td *desc.Table) error {
	if td.IsReadOnly() {
		return nil
	}

	queries := desc.BuildAlterTableForeignKeysQueries(td) // these run on transaction on the caller level.

	for _, query := range queries {
		b.WriteString(query)
	}

	return nil
}

// createFunctionsAndTriggers creates the functions and triggers for the database schema.
func (db *DB) createFunctionsAndTriggersDump(ctx context.Context, b *strings.Builder) error {
	if db.schema.SetTimestampTriggerName == "" || db.schema.UpdatedAtColumnName == "" {
		// Do not register triggers if the end-developer disabled this feature by
		// setting these fields to empty.
		return nil
	}

	var (
		createSetTimestampFunctionQuery = fmt.Sprintf(`CREATE OR REPLACE FUNCTION trigger_%s()
		RETURNS TRIGGER AS $$
		BEGIN
		NEW.%s = NOW();
		RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;`, db.schema.SetTimestampTriggerName, db.schema.UpdatedAtColumnName)

		createSetTimestampTriggerQueryTmpl = `CREATE TRIGGER %s
		BEFORE UPDATE ON %s
		FOR EACH ROW
		EXECUTE PROCEDURE trigger_%s();`
	)

	var (
		setTimestampFunctionCreated bool
	)

	triggers, err := db.ListTriggers(ctx)
	if err != nil {
		return fmt.Errorf("list triggers: %w", err)
	}

	tables := db.schema.Tables(desc.TableTypeBase)

tablesLoop:
	for _, td := range tables {
		if td.IsReadOnly() {
			continue
		}

		var setTimestampTriggerCreated bool

		for _, trigger := range triggers {
			if trigger.Name == db.schema.SetTimestampTriggerName && trigger.TableName == td.Name {
				setTimestampTriggerCreated = true
				continue tablesLoop
			}
		}

		for _, column := range td.Columns {
			if !setTimestampTriggerCreated {
				if column.Name == db.schema.UpdatedAtColumnName && column.Type == desc.Timestamp {
					if !setTimestampFunctionCreated { // global function.
						b.WriteString(createSetTimestampFunctionQuery)
						setTimestampFunctionCreated = true
					}

					// Create the trigger for each table.
					query := fmt.Sprintf(createSetTimestampTriggerQueryTmpl, db.schema.SetTimestampTriggerName, td.Name, db.schema.SetTimestampTriggerName)
					b.WriteString(query)

					setTimestampTriggerCreated = true

					continue
				}
			}
		}
	}

	return nil
}

// CheckSchema checks if the database schema matches the expected table definitions by querying the information schema and
// comparing the results.
func (db *DB) CheckSchema(ctx context.Context) error {
	tableNames := db.schema.TableNames(desc.DatabaseTableTypes...)
	if len(tableNames) == 0 {
		return nil // if no tables are defined, there is nothing to check.
	}

	tables, err := db.ListTables(ctx, ListTablesOptions{
		TableNames: tableNames,
	})
	if err != nil {
		return err
	}

	if len(tables) != len(tableNames) {
		return fmt.Errorf("expected %d tables, got %d", len(tableNames), len(tables))
	}

	// var fixQueries []string

	for _, table := range tables {
		tableName := table.Name

		td, err := db.schema.GetByTableName(tableName)
		if err != nil {
			return err // this should never happen as we get the table names from the schema.
		}

		if td.Description == "" {
			td.Description = table.Description
		}

		for _, col := range table.Columns {
			column := td.GetColumnByName(col.Name) // get code column.

			if column == nil {
				return fmt.Errorf("column %q in table %q not found in schema", col.Name, tableName)
			}

			// if column.Unique { // modify it, so checks are correct.
			// 	column.UniqueIndex = fmt.Sprintf("%s_%s_key", tableName, column.Name)
			// 	column.Unique = false
			// }

			if expected, got := strings.ToLower(col.FieldTagString(false)), strings.ToLower(column.FieldTagString(false)); expected != got {
				// if strings.Contains(expected, "nullable") && !strings.Contains(got, "nullable") {
				// 	// database has nullable, but code doesn't.
				// 	fixQuery := fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;`, tableName, col.Name)
				// 	fixQueries = append(fixQueries, fixQuery)
				// } else {
				return fmt.Errorf("column %q in table %q has wrong field tag: db:\n%s\nvs code:\n%s", col.Name, tableName, expected, got)
				//	}
			}

			if column.Description == "" {
				column.Description = col.Description
			}
		}
	}

	// Maybe a next feature but we must be very careful, skip it for now and ofc move it to a different developer-driven method:
	// if len(fixQueries) > 0 {
	// 	return db.InTransaction(ctx, func(db *DB) error {
	// 		for _, fixQuery := range fixQueries {
	// 			// fmt.Println(fixQuery)
	// 			_, err = db.Exec(ctx, fixQuery)
	// 			if err != nil {
	// 				return err
	// 			}
	// 		}

	// 		return nil
	// 	})
	// }

	return nil // return nil if no mismatch is found
}

// DeleteSchema drops the database schema.
func (db *DB) DeleteSchema(ctx context.Context) error {
	query := `DROP SCHEMA IF EXISTS "` + db.searchPath + `" CASCADE;`
	_, err := db.Exec(ctx, query)
	return err
}

// IsAutoVacuumEnabled returns true if autovacuum is enabled for the database.
//
// Read more about autovacuum at: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html.
func (db *DB) IsAutoVacuumEnabled(ctx context.Context) (enabled bool, err error) {
	query := `SHOW autovacuum;`
	err = db.QueryRow(ctx, query).Scan(&enabled)
	return
}

// DisableAutoVacuum disables autovacuum for the whole database.
func (db *DB) DisableAutoVacuum(ctx context.Context) error {
	query := `ALTER DATABASE "` + db.ConnectionOptions.Database + `" SET autovacuum = off;`
	_, err := db.Exec(ctx, query)
	return err
}

// DisableTableAutoVacuum disables autovacuum for a specific table.
func (db *DB) DisableTableAutoVacuum(ctx context.Context, tableName string) error {
	query := `ALTER TABLE "` + tableName + `" SET (autovacuum_enabled = false);`
	_, err := db.Exec(ctx, query)
	return err
}

// GetVersion returns the version number of the PostgreSQL database as a string.
func (db *DB) GetVersion(ctx context.Context) (string, error) {
	query := `SELECT version();`

	var version string

	// Query the database to retrieve the version string
	err := db.QueryRow(ctx, query).Scan(&version)
	if err != nil {
		return "", err // return an empty string and the error if the query fails
	}

	// Parse the version string to extract the version number
	start := strings.Index(version, "PostgreSQL ")
	if start == -1 {
		// return an error if the version string does not contain "PostgreSQL"
		return "", fmt.Errorf("could not find PostgreSQL version in version string: %s", version)
	}
	start += len("PostgreSQL ") // move the start index to the beginning of the version number
	end := strings.Index(version[start:], " ")
	if end == -1 {
		// return an error if the version string does not have a space after the version number
		return "", fmt.Errorf("could not find end of version number in version string: %s", version)
	}

	end += start                            // move the end index to the end of the version number
	versionNumber := version[start : end-1] // -1 to remove the trailing comma. Slice the version string to get only the version number
	versionNumber = strings.TrimSuffix(versionNumber, ".")
	return versionNumber, nil // return the version number and nil as no error occurred
}

// SizeInfo is a struct which contains the size information (for individual table or the whole database).
type SizeInfo struct {
	SizePretty string `json:"size_pretty"`
	// The on-disk size in bytes of one fork of that relation.
	// A fork is a variant of the main data file that stores additional information,
	// such as the free space map, the visibility map, or the initialization fork.
	// By default, this is the size of the main data fork only.
	Size float64 `json:"size"`

	SizeTotalPretty string `json:"size_total_pretty"`
	// The total on-disk space used for that table, including all associated indexes. This is equivalent to pg_table_size + pg_indexes_size.
	SizeTotal float64 `json:"size_total"`
}

// TableSizeInfo is a struct which contains the table size information used as an output parameter of the `db.ListTableSizes` method.
type TableSizeInfo struct {
	TableName string `json:"table_name"`
	SizeInfo
}

// ListTableSizes lists the disk size of tables (not only the registered ones) in the database.
func (db *DB) ListTableSizes(ctx context.Context) ([]TableSizeInfo, error) {
	query := `SELECT
	table_name,
	pg_size_pretty(pg_relation_size(quote_ident(table_name))) AS size_pretty,
	pg_relation_size(quote_ident(table_name)) AS size,
	  pg_size_pretty(pg_total_relation_size(quote_ident(table_name))) AS size_total_pretty,
	  pg_total_relation_size(quote_ident(table_name)) AS size_total
  FROM information_schema.tables
  WHERE table_schema = $1
  ORDER BY 3 DESC;`

	return scanQuery[TableSizeInfo](ctx, db, func(rows Rows) (t TableSizeInfo, err error) {
		err = rows.Scan(&t.TableName, &t.SizePretty, &t.Size, &t.SizeTotalPretty, &t.SizeTotal)
		return
	}, query, db.searchPath)
}

// GetSize returns the sum of size of all the database tables.
func (db *DB) GetSize(ctx context.Context) (t SizeInfo, err error) {
	query := `SELECT
	 pg_size_pretty(SUM(size)) AS size_pretty,
	 SUM(size) AS size, pg_size_pretty(SUM(size_total)) AS size_total_pretty,
	 SUM(size_total) AS size_total
	 FROM (
		SELECT
		  table_name,
		  pg_relation_size(quote_ident(table_name)) AS size,
			pg_total_relation_size(quote_ident(table_name)) AS size_total
		FROM information_schema.tables
		WHERE table_schema = $1
		ORDER BY 1 DESC
	) f;`

	err = db.QueryRow(ctx, query, db.searchPath).Scan(&t.SizePretty, &t.Size, &t.SizeTotalPretty, &t.SizeTotal)
	return
}

// MapTypeFilter is a map of expressions inputs text to field type.
// It's a TableFilter.
//
// Example on LsitTableOptions of the ListTables method:
//
//	Filter: pg.MapTypeFilter{
//		"customer_profiles.fields.jsonb": entity.Fields{},
//	},
type MapTypeFilter map[string]any

var _ desc.TableFilter = MapTypeFilter{}

// FilterTable implements the TableFilter interface.
func (m MapTypeFilter) FilterTable(t *Table) bool {
	expressions := make(desc.Expressions, 0, len(m))
	for k, v := range m {
		expressions = append(expressions, desc.NewExpression(k, reflect.TypeOf(v)))
	}
	return expressions.FilterTable(t)
}

// ListTableOptions are the options for listing tables.
type ListTablesOptions struct {
	TableNames []string

	Filter desc.TableFilter // Filter allows to customize the StructName and its Column field types.
}

// ListTables returns a list of converted table definitions from the remote database schema.
func (db *DB) ListTables(ctx context.Context, opts ListTablesOptions) ([]*desc.Table, error) {
	columns, err := db.ListColumns(ctx, opts.TableNames...)
	if err != nil {
		return nil, err
	}

	var (
		tableNamesOrdered        = make([]string, 0)
		tableDescriptionsOrdered = make([]string, 0)
		tableTypesOrdered        = make([]desc.TableType, 0)
		tableColumnsMap          = make(map[string][]*desc.Column)
	)

	for _, column := range columns {
		t, ok := tableColumnsMap[column.TableName]
		if !ok {
			tableNamesOrdered = append(tableNamesOrdered, column.TableName)
			tableDescriptionsOrdered = append(tableDescriptionsOrdered, column.TableDescription)
			tableTypesOrdered = append(tableTypesOrdered, column.TableType)
		}

		tableColumnsMap[column.TableName] = append(t, column)
	}

	tables := make([]*desc.Table, 0, len(tableNamesOrdered))

	for i, tableName := range tableNamesOrdered {
		columns := tableColumnsMap[tableName]

		table := &desc.Table{
			RegisteredPosition: i,
			StructName:         desc.ToStructName(tableName),
			StructType:         nil,
			SearchPath:         db.searchPath,
			Name:               tableName,
			Description:        tableDescriptionsOrdered[i],
			Type:               tableTypesOrdered[i],
		}

		table.AddColumns(columns...)

		filter := opts.Filter
		if filter != nil {
			if ok := filter.FilterTable(table); !ok {
				continue // skip this table.
			}
		}

		tables = append(tables, table)
	}

	// Sort so "parent" tables are going first to the list.
	sort.SliceStable(tables, func(i, j int) bool {
		tb1 := tables[i]
		tb2 := tables[j]
		if tb2.IsReadOnly() {
			return true // tb1 comes first.
		}

		if tb1.IsReadOnly() {
			return false
		}

		return !strings.Contains(tb1.Name, "_")
	})

	return tables, nil
}

// ListColumns returns a list of columns definitions for the given table names.
func (db *DB) ListColumns(ctx context.Context, tableNames ...string) ([]*desc.Column, error) {
	basicInfos, err := db.ListColumnsInformationSchema(ctx, tableNames...)
	if err != nil {
		return nil, err
	}

	constraints, err := db.ListConstraints(ctx, tableNames...)
	if err != nil {
		return nil, err
	}

	uniqueIndexes, err := db.ListUniqueIndexes(ctx, tableNames...)
	if err != nil {
		return nil, err
	}

	columns := make([]*desc.Column, 0, len(basicInfos))

	for _, basicInfo := range basicInfos {
		var column desc.Column
		basicInfo.BuildColumn(&column)

		for _, constraint := range constraints {
			if constraint.TableName == column.TableName && constraint.ColumnName == column.Name {
				constraint.BuildColumn(&column)
			}
		}

	uniqueIndexLoop:
		for _, uniqueIndex := range uniqueIndexes {
			if uniqueIndex.TableName == column.TableName {
				for _, columnName := range uniqueIndex.Columns {
					if columnName == column.Name {
						column.Unique = false
						column.UniqueIndex = uniqueIndex.IndexName
						break uniqueIndexLoop
					}
				}
			}
		}

		// No need to put index types on these type of columns, postgres manages these.
		if column.PrimaryKey || column.Unique || column.UniqueIndex != "" {
			column.Index = desc.InvalidIndex
		}

		columns = append(columns, &column)
	}

	return columns, nil
}

// ListConstraints returns a list of constraint definitions in the database schema by querying the pg_constraint table and.
func (db *DB) ListConstraints(ctx context.Context, tableNames ...string) ([]*desc.Constraint, error) {
	if tableNames == nil {
		tableNames = make([]string, 0)
	}

	query := `SELECT
    cl.relname AS table_name,
    a.attname AS column_name,
    con.conname AS constraint_name,
    con.contype AS constraint_type,
    pg_get_constraintdef(con.oid) AS constraint_definition,
    COALESCE(am.amname, '') AS index_type
FROM
    pg_catalog.pg_class cl
JOIN
    pg_catalog.pg_namespace n ON n.oid = cl.relnamespace
JOIN
    pg_catalog.pg_attribute a ON a.attrelid = cl.oid
JOIN
    pg_catalog.pg_constraint con ON con.conrelid = cl.oid AND a.attnum = ANY (con.conkey)
LEFT JOIN
    pg_catalog.pg_index idx ON idx.indrelid = cl.oid AND idx.indexrelid = con.conindid
LEFT JOIN
    pg_catalog.pg_class i ON i.oid = idx.indexrelid
LEFT JOIN
    pg_catalog.pg_am am ON am.oid = i.relam
WHERE
    n.nspname = $1 AND
    ( CARDINALITY($2::varchar[]) = 0 OR cl.relname = ANY($2::varchar[]) )
-- 	ORDER BY
--   cl.relname,
--   a.attnum
UNION ALL

SELECT
    tablename AS table_name,
    '' AS column_name,  -- retrieved by definition
    indexname AS constraint_name,
    'i' AS constraint_type,
    indexdef AS constraint_definition,
    '' AS index_type -- retrieved by definition
FROM
    pg_indexes i
WHERE
    schemaname = $1 AND
    ( CARDINALITY($2::varchar[]) = 0 OR tablename = ANY($2::varchar[]) ) AND
    indexdef NOT LIKE '%UNIQUE%' -- don't collect unique indexes here, they are (or should be) collected in the first part of the query OR by the ListUniqueIndexes.
ORDER BY table_name, column_name;`

	/*
		table_name	column_name	constraint_name	constraint_type	constraint_definition	index_type
		blog_posts	blog_posts_blog_id_fkey	i	CREATE INDEX blog_posts_blog_id_fkey ON public.blog_posts USING btree (blog_id)
		blog_posts	blog_id	blog_posts_blog_id_fkey	f	FOREIGN KEY (blog_id) REFERENCES blogs(id) ON DELETE CASCADE DEFERRABLE
		blog_posts	id	blog_posts_pkey	p	PRIMARY KEY (id)	btree
		blog_posts	read_time_minutes	blog_posts_read_time_minutes_check	c	CHECK ((read_time_minutes > 0))
		blog_posts	source_url	uk_blog_post	u	UNIQUE (title, source_url)	btree
		blog_posts	title	uk_blog_post	u	UNIQUE (title, source_url)	btree
		blogs	id	blogs_pkey	p	PRIMARY KEY (id)	btree
		customers	customers_name_idx	i	CREATE INDEX customers_name_idx ON public.customers USING btree (name)
		customers	cognito_user_id	customer_unique_idx	u	UNIQUE (cognito_user_id, email)	btree
		customers	email	customer_unique_idx	u	UNIQUE (cognito_user_id, email)	btree
		customers	id	customers_pkey	p	PRIMARY KEY (id)	btree
	*/

	// Execute the query using db.Query and pass in the search path as a parameter
	rows, err := db.Query(ctx, query, db.searchPath, tableNames)
	if err != nil {
		return nil, err // return nil and the error if the query fails
	}
	defer rows.Close() // close the rows instance when done

	// constraintNames := make(map[string]struct{})

	cs := make([]*desc.Constraint, 0) // create an empty slice to store the constraint definitions
	for rows.Next() {                 // loop over the rows returned by the query
		var (
			c                    desc.Constraint
			constraintDefinition string
		)

		if err = rows.Scan(
			&c.TableName,
			&c.ColumnName,
			&c.ConstraintName,
			&c.ConstraintType,
			&constraintDefinition,
			&c.IndexType,
		); err != nil {
			return nil, err
		}

		// if _, exists := constraintNames[c.ConstraintName]; !exists {
		//	constraintNames[c.ConstraintName] = struct{}{}

		c.Build(constraintDefinition)
		cs = append(cs, &c)
		// }
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return cs, nil
}

// ListUniqueIndexes returns a list of unique indexes in the database schema by querying the pg_index table and
// filtering the results to only include unique indexes.
func (db *DB) ListUniqueIndexes(ctx context.Context, tableNames ...string) ([]*desc.UniqueIndex, error) {
	if tableNames == nil {
		tableNames = make([]string, 0)
	}

	query := `SELECT
	-- n.nspname AS schema_name,
	t.relname AS table_name,
	i.relname AS index_name,
	array_agg(a.attname ORDER BY a.attnum) AS index_columns
  FROM pg_index p
  JOIN pg_class t ON t.oid = p.indrelid -- the table
  JOIN pg_class i ON i.oid = p.indexrelid -- the index
  JOIN pg_namespace n ON n.oid = t.relnamespace -- the schema
  JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(p.indkey) -- the columns
  WHERE n.nspname = $1
  AND ( CARDINALITY($2::varchar[]) = 0 OR t.relname = ANY($2::varchar[]) )
  AND p.indisunique -- only unique indexes
  AND NOT p.indisprimary -- not primary keys
  AND NOT EXISTS ( -- not created by a constraint
	SELECT 1 FROM pg_constraint c
	WHERE c.conindid = p.indexrelid
  )
  GROUP BY n.nspname, t.relname, i.relname;`
	/*
		public	customer_allergies	customer_allergy	{customer_id,allergy_id}
		public	customer_cheat_foods	customer_cheat_food	{customer_id,food_id}
		public	customer_devices	customer_devices_unique	{customer_id,type}
	*/

	// Execute the query using db.Query and pass in the search path as a parameter
	rows, err := db.Query(ctx, query, db.searchPath, tableNames)
	if err != nil {
		return nil, err // return nil and the error if the query fails
	}
	defer rows.Close() // close the rows instance when done

	cs := make([]*desc.UniqueIndex, 0) // create an empty slice to store the unique index definitions

	for rows.Next() { // loop over the rows returned by the query
		var (
			tableName string
			indexName string
			columns   []string
		)

		if err = rows.Scan(
			&tableName,
			&indexName,
			&columns,
		); err != nil {
			return nil, err
		}

		c := desc.UniqueIndex{
			TableName: tableName,
			IndexName: indexName,
			Columns:   columns,
		}

		cs = append(cs, &c)

	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return cs, nil
}

// ListTriggers returns a list of triggers in the database for a given set of tables
// The method takes a context and returns a slice of Trigger pointers, and an error if any.
func (db *DB) ListTriggers(ctx context.Context) ([]*desc.Trigger, error) {
	query := `SELECT
	event_object_catalog,
	event_object_schema,
	trigger_name,
	event_manipulation,
	event_object_table,
	action_statement,
	action_orientation,
	action_timing FROM information_schema.triggers 
	WHERE event_object_catalog = $1 AND event_object_table = ANY($2) ORDER BY event_object_table;`

	rows, err := db.Query(ctx, query, db.ConnectionOptions.Config.Database, db.schema.TableNames())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	triggers := make([]*desc.Trigger, 0)
	for rows.Next() {
		var trigger desc.Trigger
		err = rows.Scan(
			&trigger.Catalog,
			&trigger.SearchPath,
			&trigger.Name,
			&trigger.Manipulation,
			&trigger.TableName,
			&trigger.ActionStatement,
			&trigger.ActionOrientation,
			&trigger.ActionTiming,
		)

		if err != nil {
			return nil, err
		}

		triggers = append(triggers, &trigger)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return triggers, nil
}

// ListColumnsInformationSchema returns a list of basic columns information for the given table names or all.
func (db *DB) ListColumnsInformationSchema(ctx context.Context, tableNames ...string) ([]*desc.ColumnBasicInfo, error) {
	if tableNames == nil {
		tableNames = make([]string, 0)
	}

	columns := make([]*desc.ColumnBasicInfo, 0)

	query := `SELECT
	c.table_name,
	obj_description(p.attrelid::regclass) as table_description,
	t.table_type,
	c.column_name,
	c.ordinal_position,
	col_description(p.attrelid::regclass, p.attnum) as column_description,
	c.column_default,
	pg_catalog.format_type(p.atttypid, p.atttypmod) AS data_type,
	CASE WHEN c.is_nullable = 'YES' THEN true ELSE false END AS is_nullable,
	CASE WHEN c.is_identity = 'YES' THEN true ELSE false END AS is_identity,
	CASE WHEN c.is_generated = 'ALWAYS' THEN true ELSE false END AS is_generated
	FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_catalog = c.table_catalog AND t.table_schema = c.table_schema AND t.table_name = c.table_name
		JOIN pg_catalog.pg_attribute p ON p.attrelid = (c.table_schema || '.' || c.table_name)::regclass AND p.attname = c.column_name
   WHERE
   	    c.table_catalog = $1 AND
   	    c.table_schema = $2 AND
   	    ( CARDINALITY($3::varchar[]) = 0 OR c.table_name = ANY($3::varchar[]) )
   ORDER BY table_name, ordinal_position;`
	rows, err := db.Query(ctx, query, db.ConnectionOptions.Database, db.searchPath, tableNames)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			tableName         string
			tableDescription  sql.NullString
			fullTableType     string
			columnName        string
			ordinalPosition   int // for FieldIndex and OrdinalPosition.
			columnDescription sql.NullString
			columnDefault     sql.NullString
			fullDataType      string
			isNullable        bool
			isIdentity        bool
			isGenerated       bool
		)

		if err := rows.Scan(
			&tableName,
			&tableDescription,
			&fullTableType,
			&columnName,
			&ordinalPosition,
			&columnDescription,
			&columnDefault,
			&fullDataType,
			&isNullable,
			&isIdentity,
			&isGenerated,
		); err != nil {
			return nil, err
		}

		tableDesc := tableDescription.String
		if tableDesc != "" {
			if tableDesc[len(tableDesc)-1] != '.' {
				tableDesc += "."
			}
		}

		columnDesc := columnDescription.String
		if columnDesc != "" {
			if columnDesc[len(columnDesc)-1] != '.' {
				columnDesc += "."
			}
		}

		tableType := desc.ParseTableType(fullTableType)
		dataType, dataTypeArgument := desc.ParseDataType(fullDataType)
		column := &desc.ColumnBasicInfo{
			TableName:        tableName,
			TableDescription: tableDesc,
			TableType:        tableType,
			Name:             columnName,
			OrdinalPosition:  ordinalPosition,
			Description:      columnDesc,
			Default:          columnDefault.String,
			DataType:         dataType,
			DataTypeArgument: dataTypeArgument,
			IsNullable:       isNullable,
			IsIdentity:       isIdentity,
			IsGenerated:      isGenerated,
		}

		columns = append(columns, column)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return columns, nil
}
