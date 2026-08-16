package gen

import "testing"

// TestExportOptionsApplyDefaultFileMode verifies that ExportOptions.apply sets
// a safe, non-world-writable default FileMode (0o644) when the caller leaves
// FileMode unset (its zero value).
func TestExportOptionsApplyDefaultFileMode(t *testing.T) {
	opts := ExportOptions{RootDir: t.TempDir()}

	if err := opts.apply(); err != nil {
		t.Fatalf("apply: unexpected error: %v", err)
	}

	if opts.FileMode != 0o644 {
		t.Fatalf("expected default FileMode 0o644, got %o", opts.FileMode)
	}
}

// TestExportOptionsApplyExplicitFileMode verifies that an explicitly configured
// FileMode is preserved as-is by ExportOptions.apply, i.e. defaulting only
// kicks in for the zero value.
func TestExportOptionsApplyExplicitFileMode(t *testing.T) {
	opts := ExportOptions{RootDir: t.TempDir(), FileMode: 0o600}

	if err := opts.apply(); err != nil {
		t.Fatalf("apply: unexpected error: %v", err)
	}

	if opts.FileMode != 0o600 {
		t.Fatalf("expected explicit FileMode 0o600 to be preserved, got %o", opts.FileMode)
	}
}
