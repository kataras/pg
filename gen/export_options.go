package gen

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/kataras/pg/desc"
)

// ExportOptions is the options for the GenerateColumnsFromSchema function.
type ExportOptions struct {
	RootDir  string
	FileMode fs.FileMode

	ToSingular     func(string) string
	GetFileName    func(rootDir, tableName string) string
	GetPackageName func(tableName string) string
}

func EachTableToItsOwnPackage(rootDir, tableName string) string {
	if strings.HasSuffix(tableName, ".go") {
		return filepath.Join(rootDir, tableName)
	}

	packageName := desc.Singular(tableName)
	filename := filepath.Join(rootDir, packageName, packageName+".go")
	return filename
}

func EachTableGroupToItsOwnPackage() func(rootDir, tableName string) string {
	visitedTables := make(map[string]struct{}) // table group.

	getTableGroup := func(rootDir, tableName string) string {
		tableName = desc.Singular(tableName)
		for t := range visitedTables {
			if strings.HasPrefix(tableName, t+"_") {
				return t
			}
		}

		visitedTables[tableName] = struct{}{}
		return tableName
	}

	return func(rootDir, tableName string) string {
		if strings.HasSuffix(tableName, ".go") {
			return filepath.Join(rootDir, tableName)
		}

		tableGroup := getTableGroup(rootDir, tableName)
		return filepath.Join(rootDir, tableGroup, desc.Singular(tableName)+".go")
	}
}

func (opts *ExportOptions) apply() error {
	if opts.RootDir == "" {
		opts.RootDir = "./"
	}

	if opts.FileMode <= 0 {
		opts.FileMode = 0777
	}

	rootDir, err := filepath.Abs(opts.RootDir)
	if err != nil {
		return fmt.Errorf("filepath.Abs: %w", err)
	}
	opts.RootDir = rootDir // we need the fullpath in order to find the package name if missing.

	if opts.ToSingular == nil {
		opts.ToSingular = desc.Singular
	}

	if opts.GetFileName == nil {
		opts.GetFileName = func(rootDir, tableName string) string {
			filename := tableName

			if filename == "" { // if empty default the filename to the last part of the root dir +.go.
				filename = strings.TrimPrefix(filepath.Base(rootDir), "_")
			} else if strings.HasSuffix(filename, ".go") {
				return filepath.Join(rootDir, filename)
			} else { // otherwise get the singular form of the tablename + .go.
				filename = opts.ToSingular(tableName)
			}

			filename = filepath.Join(rootDir, filename)
			return fmt.Sprintf("%s.go", filename)
		}
	}

	if opts.GetPackageName == nil {
		opts.GetPackageName = func(tableName string) string {
			if tableName == "" {
				return strings.TrimPrefix(filepath.Base(opts.RootDir), "_")
			}

			filename := opts.GetFileName(opts.RootDir, tableName) // contains the full path let's get the last part of it as package name.
			packageName := filepath.Base(filepath.Dir(filename))
			packageName = strings.TrimPrefix(packageName, "_")
			if packageName == "" {
				packageName = filepath.Base(opts.RootDir) // else it's current dir.
			}

			return packageName
		}
	}

	return nil
}
