package gen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateTableFileName(t *testing.T) {
	absPath := filepath.Join(os.TempDir(), "x")

	invalid := []string{
		"",
		"../x",
		"a/../../b",
		absPath,
		"a/b",
		`a\b`,
	}

	for _, name := range invalid {
		if err := validateTableFileName(name); err == nil {
			t.Errorf("validateTableFileName(%q): expected an error, got nil", name)
		}
	}

	valid := []string{"users", "user_profiles"}

	for _, name := range valid {
		if err := validateTableFileName(name); err != nil {
			t.Errorf("validateTableFileName(%q): expected no error, got %v", name, err)
		}
	}
}
