package pg

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/kataras/pg/desc"
)

func handleExampleError(err error) {
	if err != nil {
		fmt.Println(err.Error())
	}
}

func ExampleDB_ListColumns() {
	db, err := openTestConnection(false)
	if err != nil {
		handleExampleError(err)
		return
	}
	defer db.Close()

	columns, err := db.ListColumns(context.Background())
	if err != nil {
		handleExampleError(err)
		return
	}

	expectedTags := []string{
		`[blog_posts.id] pg:"name=id,type=uuid,primary,default=gen_random_uuid()"`,
		`[blog_posts.created_at] pg:"name=created_at,type=timestamp,default=clock_timestamp()"`,
		`[blog_posts.updated_at] pg:"name=updated_at,type=timestamp,default=clock_timestamp()"`,
		`[blog_posts.blog_id] pg:"name=blog_id,type=uuid,ref=blogs(id CASCADE deferrable),index=btree"`,
		`[blog_posts.title] pg:"name=title,type=varchar(255),unique_index=uk_blog_post"`,
		`[blog_posts.photo_url] pg:"name=photo_url,type=varchar(255)"`,
		`[blog_posts.source_url] pg:"name=source_url,type=varchar(255),unique_index=uk_blog_post"`,
		`[blog_posts.read_time_minutes] pg:"name=read_time_minutes,type=smallint,default=1,check=read_time_minutes > 0"`,
		`[blog_posts.category] pg:"name=category,type=smallint,default=0"`,
		`[blog_posts.search_terms] pg:"name=search_terms,type=varchar[]"`,
		`[blog_posts.read_durations] pg:"name=read_durations,type=bigint[]"`,
		`[blog_posts.feature] pg:"name=feature,type=jsonb"`,
		`[blog_posts.other_features] pg:"name=other_features,type=jsonb"`,
		`[blog_posts.tags] pg:"name=tags,type=jsonb"`,
		`[blogs.id] pg:"name=id,type=uuid,primary,default=gen_random_uuid()"`,
		`[blogs.created_at] pg:"name=created_at,type=timestamp,default=clock_timestamp()"`,
		`[blogs.updated_at] pg:"name=updated_at,type=timestamp,default=clock_timestamp()"`,
		`[blogs.name] pg:"name=name,type=varchar(255)"`,
		`[customers.id] pg:"name=id,type=uuid,primary,default=gen_random_uuid()"`,
		`[customers.created_at] pg:"name=created_at,type=timestamp,default=clock_timestamp()"`,
		`[customers.updated_at] pg:"name=updated_at,type=timestamp,default=clock_timestamp()"`,
		`[customers.cognito_user_id] pg:"name=cognito_user_id,type=uuid,unique_index=customer_unique_idx"`,
		`[customers.email] pg:"name=email,type=varchar(255),unique_index=customer_unique_idx"`,
		`[customers.name] pg:"name=name,type=varchar(255),index=btree"`,
		`[customers.username] pg:"name=username,type=varchar(255),default=''::character varying"`, // before columns convert from struct field should match this.
	}

	if len(columns) != len(expectedTags) {
		fmt.Printf("expected %d columns but got %d\n%s", len(expectedTags), len(columns), strings.Join(expectedTags, "\n"))
		fmt.Println("\n=========")
		for _, c := range columns {
			fmt.Println(c.Name)
		}
		return
	}

	for i, column := range columns {
		expected := expectedTags[i]
		got := fmt.Sprintf("[%s.%s] %s", column.TableName, column.Name, column.FieldTagString(true))

		if expected != got {
			fmt.Printf("expected tag:\n%s\nbut got:\n%s\n", expected, got)
		}
	}

	fmt.Println("OK")
	// Output:
	// OK
}

/*
func TestDB_ListTablesInformationSchema(t *testing.T) {
	connString := getTestConnString()
	// connString = "host=localhost port=5432 user=postgres password=admin!123 search_path=public dbname=nut_dev sslmode=disable"

	schema := NewSchema()
	db, err := Open(context.Background(), schema, connString)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	opts := ListTablesInformationSchemaOptions{
		// IncludeTableNames: []string{"blog_posts"},
		GetType: func(tableName, columnName string, dataType DataType) (reflect.Type, bool) {
			switch tableName {
			case "blog_posts":
				switch columnName {
				case "feature":
					return reflect.TypeOf(Feature{}), true
				case "other_features":
					return reflect.TypeOf(Features{}), true
				case "tags":
					return reflect.TypeOf([]Tag{}), true
				}
			}

			return nil, true
		},
	}
	tables, err := db.ListTablesInformationSchema(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	for _, tb := range tables {
		t.Logf("[%d] Table: %s, StructName: %s\n", tb.RegisteredPosition, tb.Name, tb.StructName)
		for _, col := range tb.Columns {
			fieldType := "UNKNOWN"
			if col.FieldType != nil {
				fieldType = col.FieldType.String()
			}

			t.Logf("	[%s]: field name: %s, field type: %s, field position: %d: %s\n",
				col.Name, col.FieldName, fieldType, col.OrdinalPosition, col.FieldTagString())
		}
	}
}
*/

func ExampleDB_ListColumnsInformationSchema() {
	if err := createTestConnectionSchema(); err != nil {
		handleExampleError(err)
		return
	}

	db, err := openEmptyTestConnection()
	if err != nil {
		handleExampleError(err)
		return
	}
	defer db.Close()

	columns, err := db.ListColumnsInformationSchema(context.Background())
	if err != nil {
		handleExampleError(err)
		return
	}

	for _, column := range columns {
		fmt.Printf("%#+v\n", column)
	}

	// Output:
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"id", OrdinalPosition:1, Description:"", Default:"gen_random_uuid()", DataType:0x31, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"created_at", OrdinalPosition:2, Description:"", Default:"clock_timestamp()", DataType:0x2c, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"updated_at", OrdinalPosition:3, Description:"", Default:"clock_timestamp()", DataType:0x2c, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"blog_id", OrdinalPosition:4, Description:"", Default:"", DataType:0x31, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"title", OrdinalPosition:5, Description:"", Default:"", DataType:0xb, DataTypeArgument:"255", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"photo_url", OrdinalPosition:6, Description:"", Default:"", DataType:0xb, DataTypeArgument:"255", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"source_url", OrdinalPosition:7, Description:"", Default:"", DataType:0xb, DataTypeArgument:"255", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"read_time_minutes", OrdinalPosition:8, Description:"", Default:"1", DataType:0x24, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"category", OrdinalPosition:9, Description:"", Default:"0", DataType:0x24, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"search_terms", OrdinalPosition:10, Description:"", Default:"", DataType:0xc, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"read_durations", OrdinalPosition:11, Description:"", Default:"", DataType:0x2, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"feature", OrdinalPosition:12, Description:"", Default:"", DataType:0x18, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"other_features", OrdinalPosition:13, Description:"", Default:"", DataType:0x18, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blog_posts", TableDescription:"", TableType:0x0, Name:"tags", OrdinalPosition:14, Description:"", Default:"", DataType:0x18, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blogs", TableDescription:"", TableType:0x0, Name:"id", OrdinalPosition:1, Description:"", Default:"gen_random_uuid()", DataType:0x31, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blogs", TableDescription:"", TableType:0x0, Name:"created_at", OrdinalPosition:2, Description:"", Default:"clock_timestamp()", DataType:0x2c, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blogs", TableDescription:"", TableType:0x0, Name:"updated_at", OrdinalPosition:3, Description:"", Default:"clock_timestamp()", DataType:0x2c, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"blogs", TableDescription:"", TableType:0x0, Name:"name", OrdinalPosition:4, Description:"", Default:"", DataType:0xb, DataTypeArgument:"255", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"customers", TableDescription:"", TableType:0x0, Name:"id", OrdinalPosition:1, Description:"", Default:"gen_random_uuid()", DataType:0x31, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"customers", TableDescription:"", TableType:0x0, Name:"created_at", OrdinalPosition:2, Description:"", Default:"clock_timestamp()", DataType:0x2c, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"customers", TableDescription:"", TableType:0x0, Name:"updated_at", OrdinalPosition:3, Description:"", Default:"clock_timestamp()", DataType:0x2c, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"customers", TableDescription:"", TableType:0x0, Name:"cognito_user_id", OrdinalPosition:4, Description:"", Default:"", DataType:0x31, DataTypeArgument:"", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"customers", TableDescription:"", TableType:0x0, Name:"email", OrdinalPosition:5, Description:"", Default:"", DataType:0xb, DataTypeArgument:"255", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"customers", TableDescription:"", TableType:0x0, Name:"name", OrdinalPosition:6, Description:"", Default:"", DataType:0xb, DataTypeArgument:"255", IsNullable:false, IsIdentity:false, IsGenerated:false}
	// &desc.ColumnBasicInfo{TableName:"customers", TableDescription:"", TableType:0x0, Name:"username", OrdinalPosition:7, Description:"", Default:"''::character varying", DataType:0xb, DataTypeArgument:"255", IsNullable:false, IsIdentity:false, IsGenerated:false}
}

func ExampleDB_ListConstraints() {
	connString := getTestConnString()
	schema := NewSchema()
	db, err := Open(context.Background(), schema, connString)
	if err != nil {
		handleExampleError(err)
		return
	}
	defer db.Close()

	/*
		table_name	column_name	constraint_name	constraint_type	constraint_definition	index_type
		blog_posts		blog_posts_blog_id_fkey	i	CREATE INDEX blog_posts_blog_id_fkey ON public.blog_posts USING btree (blog_id)
		blog_posts	blog_id	blog_posts_blog_id_fkey	f	FOREIGN KEY (blog_id) REFERENCES blogs(id) ON DELETE CASCADE DEFERRABLE
		blog_posts	id	blog_posts_pkey	p	PRIMARY KEY (id)	btree
		blog_posts	read_time_minutes	blog_posts_read_time_minutes_check	c	CHECK ((read_time_minutes > 0))
		blog_posts	source_url	uk_blog_post	u	UNIQUE (title, source_url)	btree
		blog_posts	title	uk_blog_post	u	UNIQUE (title, source_url)	btree
		blogs	id	blogs_pkey	p	PRIMARY KEY (id)	btree
		customers		customers_name_idx	i	CREATE INDEX customers_name_idx ON public.customers USING btree (name)
		customers	cognito_user_id	customer_unique_idx	u	UNIQUE (cognito_user_id, email)	btree
		customers	email	customer_unique_idx	u	UNIQUE (cognito_user_id, email)	btree
		customers	id	customers_pkey	p	PRIMARY KEY (id)	btree
	*/
	var expectedConstraints = []*desc.Constraint{
		{
			TableName:      "blog_posts",
			ColumnName:     "blog_id",
			ConstraintName: "blog_posts_blog_id_fkey",
			ConstraintType: desc.IndexConstraintType,
			IndexType:      desc.Btree,
		},
		{
			TableName:      "blog_posts",
			ColumnName:     "blog_id",
			ConstraintName: "blog_posts_blog_id_fkey",
			ConstraintType: desc.ForeignKeyConstraintType,
			ForeignKey: &desc.ForeignKeyConstraint{
				ColumnName:          "blog_id",
				ReferenceTableName:  "blogs",
				ReferenceColumnName: "id",
				OnDelete:            "CASCADE",
				Deferrable:          true,
			},
		},
		{
			TableName:      "blog_posts",
			ColumnName:     "id",
			ConstraintName: "blog_posts_pkey",
			ConstraintType: desc.PrimaryKeyConstraintType,
			IndexType:      desc.Btree,
		},
		{
			TableName:      "blog_posts",
			ColumnName:     "read_time_minutes",
			ConstraintName: "blog_posts_read_time_minutes_check",
			ConstraintType: desc.CheckConstraintType,
			Check: &desc.CheckConstraint{
				Expression: "read_time_minutes > 0",
			},
		},
		{
			TableName:      "blog_posts",
			ColumnName:     "source_url",
			ConstraintName: "uk_blog_post",
			ConstraintType: desc.UniqueConstraintType,
			IndexType:      desc.Btree,
			Unique: &desc.UniqueConstraint{
				Columns: []string{"title", "source_url"},
			},
		},
		{
			TableName:      "blog_posts",
			ColumnName:     "title",
			ConstraintName: "uk_blog_post",
			ConstraintType: desc.UniqueConstraintType,
			IndexType:      desc.Btree,
			Unique: &desc.UniqueConstraint{
				Columns: []string{"title", "source_url"},
			},
		},
		{
			TableName:      "blogs",
			ColumnName:     "id",
			ConstraintName: "blogs_pkey",
			ConstraintType: desc.PrimaryKeyConstraintType,
			IndexType:      desc.Btree,
		},
		{
			TableName:      "customers",
			ColumnName:     "name",
			ConstraintName: "customers_name_idx",
			ConstraintType: desc.IndexConstraintType,
			IndexType:      desc.Btree,
		},
		{
			TableName:      "customers",
			ColumnName:     "cognito_user_id",
			ConstraintName: "customer_unique_idx",
			ConstraintType: desc.UniqueConstraintType,
			IndexType:      desc.Btree,
			Unique: &desc.UniqueConstraint{
				Columns: []string{"cognito_user_id", "email"},
			},
		},
		{
			TableName:      "customers",
			ColumnName:     "email",
			ConstraintName: "customer_unique_idx",
			ConstraintType: desc.UniqueConstraintType,
			IndexType:      desc.Btree,
			Unique: &desc.UniqueConstraint{
				Columns: []string{"cognito_user_id", "email"},
			},
		},
		{
			TableName:      "customers",
			ColumnName:     "id",
			ConstraintName: "customers_pkey",
			ConstraintType: desc.PrimaryKeyConstraintType,
			IndexType:      desc.Btree,
		},
	}

	columns, err := db.ListConstraints(context.Background())
	if err != nil {
		handleExampleError(err)
		return
	}

	for i, got := range columns {
		expected := expectedConstraints[i]
		if !reflect.DeepEqual(expected, got) {

			if expected.ForeignKey != nil && got.ForeignKey != nil {
				if !reflect.DeepEqual(expected.ForeignKey, got.ForeignKey) {
					fmt.Printf("expected foreign key:\n%#+v\nbut got:\n%#+v", expected.ForeignKey, got.ForeignKey)
				}
				continue
			}
			if expected.Unique != nil && got.Unique != nil {
				if !reflect.DeepEqual(expected.Unique, got.Unique) {
					fmt.Printf("expected unique:\n%#+v\nbut got:\n%#+v", expected.Unique, got.Unique)
				}
				continue
			}

			if expected.Check != nil && got.Check != nil {
				if !reflect.DeepEqual(expected.Check, got.Check) {
					fmt.Printf("expected check:\n%#+v\nbut got:\n%#+v", expected.Check, got.Check)
				}
				continue
			}

			fmt.Printf("expected:\n%#+v\nbut got:\n%#+v", expected, got)
		}
	}

	fmt.Println("OK")

	// Output:
	// OK
}
