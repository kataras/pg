package gen

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"text/template"

	"github.com/kataras/pg"

	"golang.org/x/mod/modfile"
)

// GoImportsTool is the name of the tool that will be used to format the generated code.
var GoImportsTool = "goimports"

// ExportOptions is the options for the schema export.
// Used in GenerateSchemaFromDatabase and GenerateColumnsFromSchema.
type ImportOptions struct {
	ConnString string

	ListTables pg.ListTablesOptions
}

// TODO: Add support for base-type entities.
// Make base classes, e.g.
// Accept:
// { "TargetDater": ["source_id", "target_date"],
// "Base": "id", "created_at", "updated_at"] }.
//
// Output:
// $ROOT_DIR/base/target_dater.go
// $ROOT_DIR/base/base.go
//
// And on each table which contains these columns,
// replace the column printing with the base.TargetDater and/or base.Base
// and import this 'base' package to each table's file.
func GenerateSchemaFromDatabase(ctx context.Context, i ImportOptions, e ExportOptions) error {
	if err := e.apply(); err != nil {
		return err
	}

	db, err := pg.Open(ctx, pg.NewSchema(), i.ConnString)
	if err != nil {
		return err
	}
	defer db.Close()

	tables, err := db.ListTables(ctx, i.ListTables)
	if err != nil {
		return err
	}

	if len(tables) == 0 {
		return nil
	}

	checkAndPrintTableColumnsMissingTypes(i, e, tables)

	schemaFilename := e.GetFileName(e.RootDir, "schema.go")
	err = mkdir(schemaFilename)
	if err != nil {
		return fmt.Errorf("mkdir: %s: %w", e.RootDir, err)
	}

	goModuleName, modFileAbsPath, err := findNearestGoModName(e.RootDir)
	if err != nil {
		return fmt.Errorf("find nearest go.mod: %w", err)
	}

	rootImportPath := goModuleName + strings.TrimPrefix(filepath.ToSlash(e.RootDir), filepath.ToSlash(modFileAbsPath))
	// fmt.Printf("Root import path: %s\n", rootImportPath)

	rootPackageName := e.GetPackageName("")
	schemaData, err := generateSchemaFile(&e, rootPackageName, rootImportPath, goModuleName, db.ConnectionOptions.Database, tables)
	if err != nil {
		return fmt.Errorf("generate schema: %s: %w", schemaFilename, err)
	}

	err = os.WriteFile(schemaFilename, schemaData, e.FileMode)
	if err != nil {
		return fmt.Errorf("write schema: %s: %w", schemaFilename, err)
	}

	for _, td := range tables {
		data, err := generateTable(e.GetPackageName(td.Name), td)
		if err != nil {
			return fmt.Errorf("generate table: %s: %w", td.Name, err)
		}

		filename := e.GetFileName(e.RootDir, td.Name)
		if filename == "" {
			continue
		}

		mkdir(filename)

		err = os.WriteFile(filename, data, e.FileMode)
		if err != nil {
			return fmt.Errorf("write table: %s: defininion file: %s: %w", td.Name, filename, err)
		}
	}

	// Even if the file contents is a result of format.Source (gofmt), some times the import paths
	// are not formatted correctly, so we call the goimports if exists -w $ROOT_DIR directly.
	if _, err = exec.LookPath(GoImportsTool); err == nil {
		err = exec.Command(GoImportsTool, "-w", e.RootDir).Run()
		if err != nil {
			return fmt.Errorf("%s -w %s: %w", GoImportsTool, e.RootDir, err)
		}
	}

	return nil
}

func checkAndPrintTableColumnsMissingTypes(i ImportOptions, e ExportOptions, tables []*pg.Table) {
	if len(tables) == 0 {
		return
	}

	var columnsTypeMissingLines []string

	for _, td := range tables {
		for _, col := range td.Columns {
			if col.FieldType == nil || col.FieldType.Kind() == reflect.Invalid {
				missingLine := fmt.Sprintf("%s.%s.%s", td.Name, col.Name, col.Type.String())
				columnsTypeMissingLines = append(columnsTypeMissingLines, missingLine)
			}
		}
	}

	if len(columnsTypeMissingLines) == 0 {
		return
	}

	fmt.Printf(`Field type is unknown for %d type(s): %s.
To fix that you have to apply some rules through the ImportOptions of the GenerateSchemaFromDatabase function,
example:
i := gen.ImportOptions{
    ConnString: "%s",
    ListTables: pg.ListTablesOptions{
        Filter: pg.MapTypeFilter{
            "%s": YourCustomType{},
            // [...]
        }
}

e := gen.ExportOptions {
    RootDir: "%s",
    // your other options...
}

err := gen.GenerateSchemaFromDatabase(context.Background(), i, e)
// [handle error...]
`, len(columnsTypeMissingLines), strings.Join(columnsTypeMissingLines, ", "), i.ConnString, columnsTypeMissingLines[0], e.RootDir)
}

var generateSchemaTmpl = template.Must(
	template.New("").Funcs(template.FuncMap{
		"add": func(a, b int) int {
			return a + b
		},
	}).Parse(`
package {{.PackageName}}

import (
	"github.com/kataras/pg"

	{{range .Tables}}
		{{- if .ImportPath -}}
		"{{.ImportPath}}"
		{{end -}}
	{{end}}
)

// Schema describes the {{.DatabaseName}} database schema.
// Usage:
// db, err := pg.Open(context.Background(), Schema, "connection_string_secret_here")
var Schema = pg.NewSchema().
	{{- $length := len .Tables }} 
	{{- range $i, $table := .Tables}}
	MustRegister("{{$table.TableName}}", {{$table.StructInitText}} {{- if $table.ReadOnly}}, pg.View {{- end}}){{- if eq $length (add $i 1)}}{{- else}}.{{- end}}
	{{- end}}
`))

func generateSchemaFile(e *ExportOptions, packageName, rootImportPath, goModuleName, databaseName string /* we need it to import the generated table entity files */, tables []*pg.Table) ([]byte, error) {
	type TableSchemaData struct {
		ImportPath     string // e.g. github.com/kataras/pg/gen/_testdata/customer
		TableName      string // e.g. customers
		StructInitText string // e.g customer.Customer{}
		ReadOnly       bool
	}

	tableSchemaTmplData := make([]TableSchemaData, 0, len(tables))

	for _, table := range tables {
		var (
			tableFullPackageName string
			structInitText       = fmt.Sprintf("%s{}", table.StructName)
		)

		// testdata or customer
		tablePackageName := e.GetPackageName(table.Name)
		if tablePackageName != packageName {
			tableFullPackageName = filepath.ToSlash(filepath.Join(rootImportPath, tablePackageName))
			// customer.Customer{}
			structInitText = fmt.Sprintf("%s.%s{}", tablePackageName, table.StructName)
		}

		tableSchemaTmplData = append(tableSchemaTmplData,
			TableSchemaData{
				ImportPath:     tableFullPackageName,
				TableName:      table.Name,
				StructInitText: structInitText,
				ReadOnly:       table.IsReadOnly(),
			})
	}

	tmplData := map[string]interface{}{
		"PackageName":  packageName,
		"DatabaseName": databaseName,
		"Tables":       tableSchemaTmplData,
	}

	var buf bytes.Buffer
	if err := generateSchemaTmpl.Execute(&buf, tmplData); err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}

	result, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format source: %w\n%s", err, buf.String())
	}

	return result, nil
}

func findNearestGoModName(rootDir string) (string, string, error) {
	if rootDir == "" {
		// Get the current working directory
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		rootDir = cwd
	}

	// Find the nearest go.mod file and its module path
	modName, modFileAbsPath, err := findGoModName(rootDir)
	if err != nil {
		return "", modFileAbsPath, err
	}

	return modName, modFileAbsPath, nil
}

func findGoModName(cwd string) (string, string, error) {
	// Walk up the directory tree until finding go.mod or reaching root
	for {
		//	fmt.Println("Searching in", cwd)
		goModName, found, err := findGoMod(cwd)
		if err != nil {
			return "", "", err
		}
		if found {
			return goModName, cwd, nil
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			return "", "", fmt.Errorf("reached root without finding go.mod")
		}
		cwd = parent
	}
}

const goModFileName = "go.mod"

// findGoMod returns true if there is a file named go.mod in the given directory
func findGoMod(dir string) (string, bool, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return "", false, err
	}
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		filename := file.Name()
		if filename == goModFileName {
			fullpath := filepath.Join(dir, goModFileName)
			data, err := os.ReadFile(fullpath)
			if err != nil {
				return "", false, err
			}

			return modfile.ModulePath(data), true, nil
		}
	}

	return "", false, nil
}

var generateTableTmpl = template.Must(
	template.New("").
		Parse(`
package {{.PackageName}}
{{ $fileImports := .ListImportPaths }} {{ $length := len $fileImports }} {{if gt $length 0}}
import (
	{{range $fileImports}} "{{.}}"
	{{end}}
)

{{end}}

{{ if .Description }}
// {{.Description}}
{{- else }}
	{{ if eq .Type 0}}
// {{.StructName}} is a struct value that represents a record in the {{.Name}} table.
	{{- else }}
// {{.StructName}} is a struct value that represents a record in the {{.Name}} view.
	{{- end }}
{{- end }}
type {{.StructName}} struct {
    {{range .Columns}}
	{{- if .Description}}
	// {{.Description}}
	{{- end }}
	{{.FieldName}} {{if .FieldType}} {{.FieldType.String}} {{else }} UNKNOWN__USE_pg.MapTypeFilter {{end}} ` + "`" + `{{.FieldTagString true}}` + "`" + `
    {{- end}}
}
`))

func generateTable(packageName string, td *pg.Table) ([]byte, error) {
	tmplData := generateTemplateData{
		Table:       td,
		PackageName: packageName,
	}

	var buf bytes.Buffer
	if err := generateTableTmpl.Execute(&buf, tmplData); err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}

	result, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format source: %w\n%s", err, buf.String())
	}

	return result, nil
}
