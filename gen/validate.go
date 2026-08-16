package gen

import (
	"fmt"
	"path/filepath"
	"strings"
)

// validateTableFileName reports an error if name is not safe to use as the
// path component that a GetFileName strategy derives a generated file name
// from.
//
// Table names originate from the remote database (see ListTables) and are
// later joined onto ExportOptions.RootDir. An empty, absolute, `..`-escaping,
// or separator-containing table name (e.g. "../../etc/passwd" or "a/b")
// could otherwise make the generators write outside RootDir. Legitimate path
// separators are only ever added by the GetFileName strategy functions
// themselves, never by a raw table name, so any separator here is rejected.
func validateTableFileName(name string) error {
	if name == "" {
		return fmt.Errorf("gen: table name is empty")
	}

	if !filepath.IsLocal(name) {
		return fmt.Errorf("gen: table name %q is not a valid local path", name)
	}

	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("gen: table name %q must not contain path separators", name)
	}

	return nil
}
