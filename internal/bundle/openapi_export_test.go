package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/mattlock83/wiretap/internal/artifact"
)

func openAPIExportFixture(t *testing.T) (artifact.SchemaRef, schemaManifest) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "root.yaml")
	other := filepath.Join(dir, "other.yaml")
	source := `openapi: 3.0.0
info: {title: Orders, version: "1"}
paths:
  /orders:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "other.yaml#/components/schemas/Order"}
components:
  schemas:
    ID: {type: integer}
`
	for name, data := range map[string]string{root: source, other: `components:
  schemas:
    Order:
      type: object
      properties:
        id: {$ref: "root.yaml#/components/schemas/ID"}
`} {
		if err := os.WriteFile(name, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := New(filepath.Join(dir, "output"))
	if err != nil {
		t.Fatal(err)
	}
	refs, err := w.RegisterPrepared(artifact.TestCase{ID: "one"}, []artifact.SchemaRef{{ID: "orders", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "file", Path: root}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit(&artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "one"}, Schemas: []artifact.SchemaRef{refs[0].Ref}}); err != nil {
		t.Fatal(err)
	}
	_, captures, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	ref := captures[0].Schemas[0]
	data, err := os.ReadFile(filepath.Join(filepath.Dir(ref.Source.Path), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest schemaManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	return ref, manifest
}

func TestOpenAPIFilesPreservesTransitiveCyclicContract(t *testing.T) {
	ref, manifest := openAPIExportFixture(t)
	root, files, err := OpenAPIFiles(ref)
	if err != nil {
		t.Fatal(err)
	}
	if root != manifest.Root || len(files) != 2 {
		t.Fatalf("unexpected portable contract: root %q, %d files", root, len(files))
	}
	for _, entry := range manifest.Files {
		data, found := files[entry.Path]
		if !found || digest(data) != entry.SHA256 || !bytes.Contains(data, []byte("doc-")) {
			t.Fatalf("member was omitted or changed: %q", entry.Path)
		}
	}
	// The returned members are sufficient even after moving away from the run.
	destination := t.TempDir()
	for name, data := range files {
		path := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	doc, err := loader.LoadFromFile(filepath.Join(destination, filepath.FromSlash(root)))
	if err != nil {
		t.Fatalf("load copied contract: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate copied contract: %v", err)
	}
}

func TestOpenAPIFilesRechecksIntegrityAndContainment(t *testing.T) {
	for _, name := range []string{"changed root", "changed sibling", "duplicate member", "outside set", "escaping symlink", "missing root", "wrong root", "wrong selected hash"} {
		t.Run(name, func(t *testing.T) {
			ref, manifest := openAPIExportFixture(t)
			setDir := filepath.Dir(ref.Source.Path)
			sibling := manifest.Files[0]
			if sibling.Path == manifest.Root {
				sibling = manifest.Files[1]
			}
			siblingPath := filepath.Join(setDir, filepath.Base(sibling.Path))
			switch name {
			case "changed root":
				if err := os.WriteFile(ref.Source.Path, []byte("changed"), 0644); err != nil {
					t.Fatal(err)
				}
			case "changed sibling":
				if err := os.WriteFile(siblingPath, []byte("changed"), 0644); err != nil {
					t.Fatal(err)
				}
			case "duplicate member":
				manifest.Files = append(manifest.Files, sibling)
			case "outside set":
				manifest.Files[0].Path = "contracts/" + manifest.SetHash + "/../outside.json"
			case "escaping symlink":
				outside := filepath.Join(t.TempDir(), "other.json")
				data, err := os.ReadFile(siblingPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(outside, data, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(siblingPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, siblingPath); err != nil {
					t.Fatal(err)
				}
			case "missing root":
				manifest.Files = []schemaFile{sibling}
			case "wrong root":
				manifest.Root = sibling.Path
			case "wrong selected hash":
				ref.Source.SHA256 = strings.Repeat("0", 64)
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(setDir, "manifest.json"), data, 0644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := OpenAPIFiles(ref); err == nil {
				t.Fatal("export accepted invalid frozen contract")
			}
			if name != "wrong selected hash" {
				root := filepath.Dir(filepath.Dir(setDir))
				if _, _, err := Load(root); err == nil {
					t.Fatal("archive loader accepted the same invalid frozen contract")
				}
			}
		})
	}
}

func TestOpenAPIFilesUsesConfiguredReadBudgets(t *testing.T) {
	ref, _ := openAPIExportFixture(t)
	for _, setting := range []string{"WIRETAP_MAX_ARCHIVE_FILE_BYTES", "WIRETAP_MAX_ARCHIVE_TOTAL_BYTES", "WIRETAP_MAX_ARCHIVE_FILES"} {
		t.Run(setting, func(t *testing.T) {
			t.Setenv(setting, "1")
			if _, _, err := OpenAPIFiles(ref); !errors.Is(err, ErrLoadLimit) {
				t.Fatalf("wanted bounded read failure, got %v", err)
			}
		})
	}
}

func TestParseOpenAPIUsesOnlyVerifiedInventory(t *testing.T) {
	ref, _ := openAPIExportFixture(t)
	root, files, err := OpenAPIFiles(ref)
	if err != nil {
		t.Fatal(err)
	}
	// The parsed references must be independent of the original filesystem,
	// including cyclic companions. Only the verified byte snapshot is available.
	if err := os.RemoveAll(filepath.Dir(ref.Source.Path)); err != nil {
		t.Fatal(err)
	}
	doc, err := ParseOpenAPI(t.Context(), root, files)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	for name := range files {
		if name != root {
			delete(files, name)
		}
	}
	if _, err := ParseOpenAPI(t.Context(), root, files); err == nil {
		t.Fatal("missing companion was resolved outside the verified inventory")
	}
}

func TestFrozenContractReadersPreserveCumulativeAndTighterBudgets(t *testing.T) {
	ref, manifest := openAPIExportFixture(t)
	root := filepath.Dir(filepath.Dir(filepath.Dir(ref.Source.Path)))
	manifestBytes := fileBytes(t, filepath.Join(filepath.Dir(ref.Source.Path), "manifest.json"))
	graphBytes := manifestBytes
	for _, file := range manifest.Files {
		graphBytes += fileBytes(t, filepath.Join(root, filepath.FromSlash(file.Path)))
	}
	// The same contract is verified for the run and its attempt. Neither call
	// receives a fresh archive budget or retains a second copy of the graph.
	archiveBytes := fileBytes(t, filepath.Join(root, "bundle.json")) + fileBytes(t, filepath.Join(root, "tests", "one.json")) + 2*graphBytes
	archiveReads := 2 + 2*(1+len(manifest.Files))
	if _, _, err := LoadWithLimits(root, LoadLimits{MaxTotalBytes: archiveBytes, MaxFileReads: archiveReads}); err != nil {
		t.Fatalf("exact archive budget: %v", err)
	}
	for _, limits := range []LoadLimits{{MaxTotalBytes: archiveBytes - 1}, {MaxFileReads: archiveReads - 1}} {
		if _, _, err := LoadWithLimits(root, limits); !errors.Is(err, ErrLoadLimit) {
			t.Fatalf("lost cumulative budget: %v", err)
		}
	}
	if _, _, err := OpenAPIFilesWithLimits(t.Context(), ref, LoadLimits{MaxTotalBytes: graphBytes, MaxFileReads: 1 + len(manifest.Files)}); err != nil {
		t.Fatalf("exact export budget: %v", err)
	}
	for _, limits := range []LoadLimits{{MaxTotalBytes: graphBytes - 1}, {MaxFileReads: len(manifest.Files)}, {MaxFileBytes: manifestBytes - 1}} {
		if _, _, err := OpenAPIFilesWithLimits(t.Context(), ref, limits); !errors.Is(err, ErrLoadLimit) {
			t.Fatalf("lost tighter export budget: %v", err)
		}
	}
}

func TestFrozenContractExportRetainsTighterManifestLimit(t *testing.T) {
	ref, manifest := openAPIExportFixture(t)
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value["unrecognized_metadata"] = strings.Repeat("x", 1<<20)
	data, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(ref.Source.Path), "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(ref.Source.Path)))
	if _, _, err := Load(root); err != nil {
		t.Fatalf("archive did not honor its own manifest byte budget: %v", err)
	}
	if _, _, err := OpenAPIFiles(ref); !errors.Is(err, ErrLoadLimit) {
		t.Fatalf("export lost tighter manifest budget: %v", err)
	}
}
