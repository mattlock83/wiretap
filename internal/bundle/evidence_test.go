package bundle

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSnapshotUsesArchiveVerificationAndReadLimits(t *testing.T) {
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
	snapshot, err := LoadSnapshot(writer.Path())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Root != writer.Path() || snapshot.Run.RunID != writer.Snapshot().RunID || len(snapshot.Captures) != 0 || len(snapshot.EvidencePaths) != 1 || snapshot.EvidencePaths[0] != want {
		t.Fatalf("unexpected verified snapshot: %+v; want evidence %q", snapshot, want)
	}
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILE_BYTES", "1")
	if snapshot, err := LoadSnapshot(writer.Path()); !errors.Is(err, ErrLoadLimit) || snapshot != nil {
		t.Fatalf("expected budgeted verification failure, got %v, %v", snapshot, err)
	}
}
