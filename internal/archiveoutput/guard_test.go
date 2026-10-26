package archiveoutput

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/bundle"
)

func emptyArchive(t testing.TB) string {
	t.Helper()
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return writer.Path()
}

func loadGuard(root string) (*Guard, error) {
	snapshot, err := bundle.LoadSnapshot(root)
	if err != nil {
		return nil, err
	}
	return New(snapshot)
}

func TestRawInventoryBudgetCountsUnreferencedEntries(t *testing.T) {
	root := emptyArchive(t)
	for _, name := range []string{"first", "second", "third"} {
		if err := os.Mkdir(filepath.Join(root, "tests", name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "orphan.json"), []byte("orphan"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILES", "3")
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatalf("unreferenced entries changed evidence loading: %v", err)
	}
	if guard, err := loadGuard(root); !errors.Is(err, bundle.ErrLoadLimit) || guard != nil ||
		!strings.Contains(err.Error(), "WIRETAP_MAX_ARCHIVE_FILES (3)") || !strings.Contains(err.Error(), "trusted archive") {
		t.Fatalf("expected actionable raw inventory limit, got %v, %v", guard, err)
	}
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILES", "4")
	guard, err := loadGuard(root)
	if err != nil {
		t.Fatalf("exact raw inventory budget: %v", err)
	}
	if _, err := guard.Destination(filepath.Join(root, "exports", "report.json")); err != nil {
		t.Fatalf("raised raw inventory budget did not permit output: %v", err)
	}
}

func TestRawInventoryAllowsEmptyArchiveWithOneRead(t *testing.T) {
	root := emptyArchive(t)
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILES", "1")
	if _, err := loadGuard(root); err != nil {
		t.Fatalf("fixed raw roots must not consume the entry budget: %v", err)
	}
}

func TestGuardChecksManyOutputsWithUnreferencedDirectories(t *testing.T) {
	root := emptyArchive(t)
	for i := range 2000 {
		if err := os.Mkdir(filepath.Join(root, "tests", fmt.Sprint(i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	guard, err := loadGuard(root)
	if err != nil {
		t.Fatal(err)
	}
	// A whole ordinary subtree has one protected root. Retaining every child
	// caused metadata work proportional to raw directories for each output.
	if len(guard.directories) != 1 {
		t.Fatalf("redundant protected raw directory roots: %d", len(guard.directories))
	}
	for i := range 100 {
		path := filepath.Join(root, "exports", "nested", fmt.Sprintf("report-%d.json", i))
		if _, err := guard.Destination(path); err != nil {
			t.Fatalf("output %d: %v", i, err)
		}
	}
	if _, err := guard.Destination(filepath.Join(root, "tests", "0", "nested", "report.json")); !errors.Is(err, ErrDestination) {
		t.Fatalf("raw descendant was not protected: %v", err)
	}
}

func TestGuardProtectsRawDirectoryAliasesBackedByExports(t *testing.T) {
	root := emptyArchive(t)
	backing := filepath.Join(root, "exports", "raw-tests")
	if err := os.MkdirAll(filepath.Join(backing, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backing, filepath.Join(root, "tests", "alias")); err != nil {
		t.Skip(err)
	}
	guard, err := loadGuard(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(backing, "report.json"),
		filepath.Join(backing, "nested", "new", "report.json"),
	} {
		if _, err := guard.Destination(path); !errors.Is(err, ErrDestination) {
			t.Fatalf("directory backing raw evidence was not protected: %s: %v", path, err)
		}
	}
	if _, err := guard.Destination(filepath.Join(root, "exports", "report.json")); err != nil {
		t.Fatalf("disposable export rejected: %v", err)
	}
}

func TestRawInventoryDoesNotTraverseExternalDirectoryAliases(t *testing.T) {
	root := emptyArchive(t)
	external := t.TempDir()
	for _, name := range []string{"first", "second", "third"} {
		if err := os.Mkdir(filepath.Join(external, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(external, filepath.Join(root, "tests", "outside")); err != nil {
		t.Skip(err)
	}
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILES", "1")
	guard, err := loadGuard(root)
	if err != nil {
		t.Fatalf("followed unrelated external directory: %v", err)
	}
	if _, err := guard.Destination(filepath.Join(external, "report.json")); err != nil {
		t.Fatalf("unrelated external output rejected: %v", err)
	}
}

func TestGuardRejectsExternalExportsThroughAnArchiveAlias(t *testing.T) {
	root := emptyArchive(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "exports")); err != nil {
		t.Skip(err)
	}
	alias := filepath.Join(t.TempDir(), "capture")
	if err := os.Symlink(root, alias); err != nil {
		t.Skip(err)
	}
	guard, err := loadGuard(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Destination(filepath.Join(alias, "exports", "nested", "report.json")); !errors.Is(err, ErrDestination) {
		t.Fatalf("archive alias bypassed exports-directory protection: %v", err)
	}
}

func TestGuardProtectsCaseAliasesAndPermitsExports(t *testing.T) {
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	root := writer.Path()
	alias := filepath.Join(filepath.Dir(root), strings.ToUpper(filepath.Base(root)))
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	aliasInfo, err := os.Stat(alias)
	if err != nil || !os.SameFile(rootInfo, aliasInfo) {
		t.Skip("filesystem distinguishes directory casing")
	}
	if err := os.Mkdir(filepath.Join(root, "exports"), 0700); err != nil {
		t.Fatal(err)
	}
	guard, err := loadGuard(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Destination(filepath.Join(alias, "TESTS", "nested", "report.json")); !errors.Is(err, ErrDestination) {
		t.Fatalf("casing alias bypassed raw-directory protection: %v", err)
	}
	if _, err := guard.Destination(filepath.Join(alias, "EXPORTS", "nested", "report.json")); err != nil {
		t.Fatalf("casing alias of disposable exports rejected: %v", err)
	}
}

func TestGuardProtectsHardlinkedRawAndVerifiedFiles(t *testing.T) {
	for _, source := range []string{"bundle.json", "tests/orphan.json"} {
		t.Run(source, func(t *testing.T) {
			root := emptyArchive(t)
			input := filepath.Join(root, filepath.FromSlash(source))
			if source != "bundle.json" {
				if err := os.WriteFile(input, []byte("orphan"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(t.TempDir(), "report.json")
			if err := os.Link(input, output); err != nil {
				t.Skip(err)
			}
			guard, err := loadGuard(root)
			if err != nil {
				t.Fatal(err)
			}
			// Identity checks must remain valid when file metadata changes after
			// the inventory. Size and timestamps cannot index hard links safely.
			if err := os.WriteFile(output, []byte("changed after inventory"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := guard.Destination(output); !errors.Is(err, ErrDestination) {
				t.Fatalf("hardlinked input was not protected: %v", err)
			}
		})
	}
}

func BenchmarkGuardDestinations(b *testing.B) {
	for _, count := range []int{0, 5000} {
		b.Run(fmt.Sprintf("raw-directories-%d", count), func(b *testing.B) {
			root := emptyArchive(b)
			for i := range count {
				if err := os.Mkdir(filepath.Join(root, "tests", fmt.Sprint(i)), 0700); err != nil {
					b.Fatal(err)
				}
			}
			guard, err := loadGuard(root)
			if err != nil {
				b.Fatal(err)
			}
			var outputs [32]string
			for i := range outputs {
				outputs[i] = filepath.Join(root, "exports", fmt.Sprintf("report-%d.json", i))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				if _, err := guard.Destination(outputs[i%len(outputs)]); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
