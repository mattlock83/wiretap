package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mattlock83/wiretap/internal/archiveoutput"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/postman"
)

func TestCLICoverageOutputProtectsEvidence(t *testing.T) {
	for _, command := range []string{"report", "export"} {
		for _, format := range []string{"json", "yaml", "text"} {
			for _, target := range []string{"manifest", "attempt", "directory alias", "leaf symlink", "hardlink"} {
				t.Run(command+"/"+format+"/"+target, func(t *testing.T) {
					root := postmanBundleFixture(t)
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
					if code := execute(context.Background(), []string{command, "--bundle", root, "--format", format, "--output", output}); code == 0 {
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
}

func TestCLIReportRegeneratesInsideExports(t *testing.T) {
	root := postmanBundleFixture(t)
	exports := filepath.Join(root, "exports")
	if err := os.Mkdir(exports, 0755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(exports, "coverage.json")
	if err := os.WriteFile(output, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"report", "export"} {
		if code := execute(context.Background(), []string{command, "--bundle", root, "--output", output}); code != 0 {
			t.Fatalf("report into exports = %d", code)
		}
		if info, err := os.Stat(output); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
			t.Fatalf("lost existing report permissions: %v %v", info, err)
		}
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatal(err)
	}
}

func TestCLIAllOutputsProtectSourceBackedByExports(t *testing.T) {
	root := postmanBundleFixture(t)
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
			root := postmanBundleFixture(t)
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
			guard, err := archiveoutput.New(root)
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

func TestCLIPostmanProtectsSourceDestinations(t *testing.T) {
	for _, target := range []string{"manifest", "source directory", "directory alias", "nested directory alias", "exports symlink"} {
		t.Run(target, func(t *testing.T) {
			root := postmanMockBundleFixture(t)
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
			if code := execute(context.Background(), []string{"export", "--bundle", root, "--format", "postman", option, output}); code == 0 {
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

func TestCLIPostmanProtectsEveryCollectionBeforeWriting(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		for index := 0; index < 3; index++ {
			t.Run(kind+string(rune('0'+index)), func(t *testing.T) {
				root := postmanMockBundleFixture(t)
				exports, err := postman.Generate(root, "")
				if err != nil {
					t.Fatal(err)
				}
				directory := t.TempDir()
				for i, export := range exports {
					path := filepath.Join(directory, export.Filename)
					if i == index {
						link := os.Symlink
						if kind == "hardlink" {
							link = os.Link
						}
						if err := link(filepath.Join(root, "bundle.json"), path); err != nil {
							t.Skip(err)
						}
					} else if err := os.WriteFile(path, []byte("keep"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				if code := execute(context.Background(), []string{"export", "--bundle", root, "--format", "postman", "--output-dir", directory}); code == 0 {
					t.Fatal("accepted linked source collection destination")
				}
				for i, export := range exports {
					if i != index {
						if data, err := os.ReadFile(filepath.Join(directory, export.Filename)); err != nil || string(data) != "keep" {
							t.Fatalf("changed earlier collection: %q %v", data, err)
						}
					}
				}
				if _, _, err := bundle.Load(root); err != nil {
					t.Fatalf("changed source archive: %v", err)
				}
			})
		}
	}
}

func TestCLIPostmanRegeneratesInsideExports(t *testing.T) {
	root := postmanMockBundleFixture(t)
	directory := filepath.Join(root, "exports", "postman")
	for i := 0; i < 2; i++ {
		if code := execute(context.Background(), []string{"export", "--bundle", root, "--format", "postman", "--output-dir", directory}); code != 0 {
			t.Fatalf("export inside exports = %d", code)
		}
		if collections := readPostmanCollections(t, directory); len(collections) != 3 {
			t.Fatalf("missing collections: %+v", collections)
		}
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatal(err)
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
	root := postmanBundleFixture(t)
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
