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
	// RootDir is the directory generated files are written under. It defaults to "./"
	// and is resolved to an absolute path by apply, since GetPackageName's default
	// implementation derives the root package name from it.
	RootDir string
	// FileMode is the file mode used when writing generated Go source files.
	// It defaults to 0o644 (owner read/write, group/other read-only) so that
	// generated source is not left world-writable; set it explicitly to
	// override that default.
	FileMode fs.FileMode

	// ToSingular converts a table name to its singular form, used by the default
	// GetFileName and GetPackageName strategies. Defaults to desc.Singular.
	ToSingular func(string) string
	// GetFileName returns the Go source file path for a given table name (or, called
	// with an empty tableName, the root/schema file). Defaults to one file per table
	// under RootDir, named after ToSingular(tableName); see EachTableToItsOwnPackage
	// and EachTableGroupToItsOwnPackage for alternative, ready-made strategies.
	GetFileName func(rootDir, tableName string) string
	// GetPackageName returns the Go package name a generated table's file belongs to,
	// derived by default from the directory GetFileName places it in.
	GetPackageName func(tableName string) string
}

// EachTableToItsOwnPackage is a GetFileName strategy that places every table's generated
// file in its own package, named after the table's singular form: rootDir/customer/customer.go
// for a "customers" table.
func EachTableToItsOwnPackage(rootDir, tableName string) string {
	if strings.HasSuffix(tableName, ".go") {
		return filepath.Join(rootDir, tableName)
	}

	packageName := desc.Singular(tableName)
	filename := filepath.Join(rootDir, packageName, packageName+".go")
	return filename
}

// EachTableGroupToItsOwnPackage returns a GetFileName strategy that groups related tables
// into the same package: the first table seen with a given prefix (e.g. "customer")
// establishes the group, and any later table whose singular name starts with
// "<group>_" (e.g. "customer_address") is placed in that same group's package instead of
// getting one of its own.
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
		opts.FileMode = 0o644
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
