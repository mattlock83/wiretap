package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mattlock83/wiretap/internal/archiveoutput"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func TestCLIOutputsProtectDirectlyReferencedExportPayloads(t *testing.T) {
	for _, target := range []string{"report", "report hardlink", "arazzo", "all coverage", "all html", "all arazzo", "all receipt"} {
		t.Run(target, func(t *testing.T) {
			root := httpBundleFixture(t)
			relative := filepath.Join("exports", "coverage.json")
			if target == "all receipt" {
				relative = filepath.Join("exports", "result.json")
			}
			if target == "all html" {
				relative = filepath.Join("exports", "coverage.html")
			}
			if target == "all arazzo" {
				relative = filepath.Join("exports", "arazzo", "arazzo.json")
			}
			before := relocateHTTPBodyReference(t, root, relative)
			source := filepath.Join(root, relative)
			output := source
			if target == "report hardlink" {
				output = filepath.Join(t.TempDir(), "report.json")
				if err := os.Link(source, output); err != nil {
					t.Skip(err)
				}
			}
			args := []string{"report", "--bundle", root, "--output", output}
			if target == "arazzo" {
				args = []string{"report", "--bundle", root, "--format", "arazzo", "--output", output}
			}
			if target == "all coverage" || target == "all html" || target == "all arazzo" || target == "all receipt" {
				args = []string{"report", "--bundle", root, "--format", "all"}
			}
			if code := execute(context.Background(), args); code == 0 {
				t.Fatal("accepted output that replaces a referenced export payload")
			}
			if after, err := os.ReadFile(source); err != nil || string(after) != string(before) {
				t.Fatalf("output changed referenced payload: %v", err)
			}
			if _, _, err := bundle.Load(root); err != nil {
				t.Fatalf("output changed source archive: %v", err)
			}
		})
	}
}

// HTTP body references can use any contained archive path. Keep the original
// payload as a separate inode so protecting only canonical paths misses this case.
func relocateHTTPBodyReference(t *testing.T, root, relative string) []byte {
	t.Helper()
	manifestPath := filepath.Join(root, "bundle.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var run bundle.Run
	if err := json.Unmarshal(raw, &run); err != nil {
		t.Fatal(err)
	}
	reference := &run.Attempts[0]
	attemptPath := filepath.Join(root, filepath.FromSlash(reference.Path))
	raw, err = os.ReadFile(attemptPath)
	if err != nil {
		t.Fatal(err)
	}
	var attempt bundle.Attempt
	if err := json.Unmarshal(raw, &attempt); err != nil {
		t.Fatal(err)
	}
	body := &attempt.Bodies[0]
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(body.Path)))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, data, 0644); err != nil {
		t.Fatal(err)
	}
	body.Path = filepath.ToSlash(relative)
	raw, err = json.MarshalIndent(attempt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attemptPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	reference.SHA256 = hex.EncodeToString(hash[:])
	raw, err = json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatalf("relocated payload must remain valid evidence: %v", err)
	}
	return data
}

func TestCLICoverageOutputProtectsEvidence(t *testing.T) {

	for _, format := range []string{"json", "text", "html"} {
		for _, target := range []string{"manifest", "attempt", "directory alias", "leaf symlink", "hardlink"} {
			t.Run("report/"+format+"/"+target, func(t *testing.T) {
				root := httpBundleFixture(t)
				manifest := filepath.Join(root, "bundle.json")
				before, err := os.ReadFile(manifest)
				if err != nil {
					t.Fatal(err)
				}
				output := manifest
				switch target {
				case "attempt":
					output = filepath.Join(root, "tests", "read-items.json")
				case "directory alias":
					alias := filepath.Join(t.TempDir(), "capture")
					if err := os.Symlink(root, alias); err != nil {
						t.Skip(err)
					}
					output = filepath.Join(alias, "bundle.json")
				case "leaf symlink", "hardlink":
					output = filepath.Join(t.TempDir(), "output.json")
					link := os.Symlink
					if target == "hardlink" {
						link = os.Link
					}
					if err := link(manifest, output); err != nil {
						t.Skip(err)
					}
				}
				if code := execute(context.Background(), []string{"report", "--bundle", root, "--format", format, "--output", output}); code == 0 {
					t.Fatal("accepted output that aliases capture evidence")
				}
				if after, err := os.ReadFile(manifest); err != nil || string(after) != string(before) {
					t.Fatalf("changed source manifest: %v", err)
				}
				if _, _, err := bundle.Load(root); err != nil {
					t.Fatalf("changed source archive: %v", err)
				}
			})
		}
	}

}

func TestCLIReportRegeneratesInsideExports(t *testing.T) {
	root := httpBundleFixture(t)
	exports := filepath.Join(root, "exports")
	if err := os.Mkdir(exports, 0755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(exports, "coverage.json")
	if err := os.WriteFile(output, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}

	if code := execute(context.Background(), []string{"report", "--bundle", root, "--output", output}); code != 0 {
		t.Fatalf("report into exports = %d", code)
	}
	if info, err := os.Stat(output); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		t.Fatalf("lost existing report permissions: %v %v", info, err)
	}

	if _, _, err := bundle.Load(root); err != nil {
		t.Fatal(err)
	}
}

func TestCLIAllOutputsProtectSourceBackedByExports(t *testing.T) {
	root := httpBundleFixture(t)
	exports := filepath.Join(root, "exports")
	if err := os.Mkdir(exports, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "tests", "read-items.json")
	backing := filepath.Join(exports, "coverage.json")
	if err := os.Rename(source, backing); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backing, source); err != nil {
		t.Skip(err)
	}
	if code, _ := executeCapturingStdout(t, []string{"report", "--bundle", root, "--format", "all"}); code == 0 {
		t.Fatal("all outputs replaced backing source file")
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatalf("all outputs changed source archive: %v", err)
	}
}

func TestArchiveOutputGuardProtectsSourcesBackedByExports(t *testing.T) {
	for _, alias := range []string{"file", "directory"} {
		t.Run(alias, func(t *testing.T) {
			root := httpBundleFixture(t)
			exports := filepath.Join(root, "exports")
			if err := os.Mkdir(exports, 0755); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "tests", "read-items.json")
			backing := filepath.Join(exports, "source.json")
			if alias == "directory" {
				source = filepath.Join(root, "tests")
				backing = filepath.Join(exports, "source-tests")
			}
			if err := os.Rename(source, backing); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(backing, source); err != nil {
				t.Skip(err)
			}
			output := backing
			if alias == "directory" {
				output = filepath.Join(backing, "new", "report.json")
			}
			snapshot, err := bundle.LoadSnapshot(root)
			if err != nil {
				t.Fatal(err)
			}
			guard, err := archiveoutput.New(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := guard.Destination(output); err == nil {
				t.Fatal("accepted backing source path inside exports")
			}
			if _, _, err := bundle.Load(root); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCLIArazzoProtectsSourceDirectories(t *testing.T) {
	for _, target := range []string{"manifest", "source directory", "directory alias", "nested directory alias", "exports symlink"} {
		t.Run(target, func(t *testing.T) {
			root := httpMockBundleFixture(t)
			option, output := "--output", filepath.Join(root, "bundle.json")
			switch target {
			case "source directory":
				option, output = "--output-dir", filepath.Join(root, "tests")
			case "directory alias", "nested directory alias":
				alias := filepath.Join(t.TempDir(), "capture")
				if err := os.Symlink(root, alias); err != nil {
					t.Skip(err)
				}
				option, output = "--output-dir", filepath.Join(alias, "tests")
				if target == "nested directory alias" {
					output = filepath.Join(output, "new", "nested")
				}
			case "exports symlink":
				outside := t.TempDir()
				if err := os.Symlink(outside, filepath.Join(root, "exports")); err != nil {
					t.Skip(err)
				}
				option, output = "--output-dir", filepath.Join(root, "exports", "new", "nested")
			}
			if code := execute(context.Background(), []string{"report", "--bundle", root, "--format", "arazzo", option, output}); code == 0 {
				t.Fatal("accepted source destination")
			}
			if _, _, err := bundle.Load(root); err != nil {
				t.Fatalf("changed source archive: %v", err)
			}
			if target == "nested directory alias" || target == "exports symlink" {
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatalf("rejected export created destination: %v", err)
				}
			}
		})
	}
}

func TestCLIReportNewFilesRespectUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX file permissions")
	}
	if os.Getenv("WIRETAP_TEST_REPORT_UMASK") != "child" {
		command := exec.Command("sh", "-c", `umask 077; exec "$@"`, "sh", os.Args[0], "-test.run=^TestCLIReportNewFilesRespectUmask$")
		command.Env = append(os.Environ(), "WIRETAP_TEST_REPORT_UMASK=child")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("report with restrictive umask: %v\n%s", err, output)
		}
		return
	}
	output := filepath.Join(t.TempDir(), "report.json")
	existing := filepath.Join(t.TempDir(), "existing.json")
	if err := os.WriteFile(existing, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0640); err != nil {
		t.Fatal(err)
	}
	root := httpBundleFixture(t)
	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{{output, 0600}, {existing, 0640}} {
		if code := execute(context.Background(), []string{"report", "--bundle", root, "--output", tc.path}); code != 0 {
			t.Fatalf("report = %d", code)
		}
		if info, err := os.Stat(tc.path); err != nil || info.Mode().Perm() != tc.mode {
			t.Fatalf("wrong report permissions: %v %v", info, err)
		}
	}
}
