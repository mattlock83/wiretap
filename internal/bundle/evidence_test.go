package bundle

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestEvidencePathsUsesArchiveVerificationAndReadLimits(t *testing.T) {
	writer, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(writer.Path(), "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	paths, err := EvidencePaths(writer.Path())
	if err != nil || len(paths) != 1 || paths[0] != want {
		t.Fatalf("verified evidence paths = %v, %v; want %q", paths, err, want)
	}
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILE_BYTES", "1")
	if paths, err := EvidencePaths(writer.Path()); !errors.Is(err, ErrLoadLimit) || paths != nil {
		t.Fatalf("expected budgeted verification failure, got %v, %v", paths, err)
	}
}
