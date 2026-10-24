package bundle

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func opaquePackageFixture(t *testing.T, kind string) (*Writer, []artifact.SchemaRef) {
	t.Helper()
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	refs, err := w.Register(artifact.TestCase{ID: "test-1"}, []artifact.SchemaRef{{
		ID: "events", Role: "dependency", AssetID: "orders", Type: kind,
		Source: artifact.SchemaSource{Type: "package", Path: "api/root.schema", Files: []artifact.ContractFile{
			{Path: "api/root.schema", Content: "# original comment\r\nimports ../types/event.schema\r\n"},
			{Path: "types/event.schema", Encoding: "base64", Content: base64.StdEncoding.EncodeToString([]byte{0, 255, 10})},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit(&artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "test-1"}, Schemas: refs}); err != nil {
		t.Fatal(err)
	}
	return w, refs
}

func TestOpaqueContractPackagesPortableAndBytePreserving(t *testing.T) {
	for _, kind := range []string{"avro", "asyncapi", "graphql"} {
		t.Run(kind, func(t *testing.T) {
			w, _ := opaquePackageFixture(t, kind)
			copied := filepath.Join(t.TempDir(), "copy")
			if err := os.CopyFS(copied, os.DirFS(w.Path())); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(w.Path()); err != nil {
				t.Fatal(err)
			}
			run, captures, err := Load(copied)
			if err != nil {
				t.Fatal(err)
			}
			if len(captures) != 1 || len(run.Schemas) != 1 {
				t.Fatalf("unexpected schema inventory: %+v", run.Schemas)
			}
			root := captures[0].Schemas[0].Source.Path
			data, err := os.ReadFile(root)
			if err != nil || string(data) != "# original comment\r\nimports ../types/event.schema\r\n" {
				t.Fatalf("original root bytes changed: %q, %v", data, err)
			}
			child, err := os.ReadFile(filepath.Join(filepath.Dir(root), "../types/event.schema"))
			if err != nil || !bytes.Equal(child, []byte{0, 255, 10}) {
				t.Fatalf("nested relative reference bytes changed: %v, %v", child, err)
			}
		})
	}
}

func TestContractPackageIdentityCoversFullSourceSet(t *testing.T) {
	files := []schemaFile{{Path: "api/root.yaml", SHA256: digest([]byte("root"))}, {Path: "types/event.avsc", SHA256: digest([]byte("event"))}}
	baseline := contractPackageDigest("asyncapi", "api/root.yaml", files)
	if reversed := contractPackageDigest("asyncapi", "api/root.yaml", []schemaFile{files[1], files[0]}); reversed != baseline {
		t.Fatal("source list order changes package identity")
	}
	for _, alternate := range []string{
		contractPackageDigest("avro", "api/root.yaml", files),
		contractPackageDigest("asyncapi", "types/event.avsc", files),
		contractPackageDigest("asyncapi", "api/root.yaml", files[:1]),
		contractPackageDigest("asyncapi", "api/root.yaml", []schemaFile{files[0], {Path: "types/changed.avsc", SHA256: files[1].SHA256}}),
	} {
		if alternate == baseline {
			t.Fatal("package identity fails to cover contract language, root or source inventory")
		}
	}
}

func TestOpaqueContractPackageDetectsTampering(t *testing.T) {
	for _, repairManifest := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed source bytes", true: "changed bytes and manifest hash"}[repairManifest], func(t *testing.T) {
			w, refs := opaquePackageFixture(t, "asyncapi")
			base := filepath.Dir(filepath.Dir(refs[0].Source.Path))
			child := filepath.Join(base, "types/event.schema")
			changed := []byte("changed child")
			if err := os.WriteFile(child, changed, 0600); err != nil {
				t.Fatal(err)
			}
			if repairManifest {
				name := filepath.Join(base, "manifest.json")
				raw, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				var manifest schemaManifest
				if err := json.Unmarshal(raw, &manifest); err != nil {
					t.Fatal(err)
				}
				for i := range manifest.Files {
					if strings.HasSuffix(manifest.Files[i].Path, "/types/event.schema") {
						manifest.Files[i].SHA256 = digest(changed)
					}
				}
				raw, err = json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := Load(w.Path()); err == nil {
				t.Fatal("tampered package accepted")
			}
		})
	}
}

func TestContractPackageRejectsAmbiguousAndUnsafeSources(t *testing.T) {
	for name, source := range map[string]artifact.SchemaSource{
		"missing root":             {Type: "package", Path: "missing.avsc", Files: []artifact.ContractFile{{Path: "event.avsc", Content: "{}"}}},
		"parent escape":            {Type: "package", Path: "../event.avsc", Files: []artifact.ContractFile{{Path: "../event.avsc", Content: "{}"}}},
		"unclean name":             {Type: "package", Path: "types/../event.avsc", Files: []artifact.ContractFile{{Path: "types/../event.avsc", Content: "{}"}}},
		"windows separator":        {Type: "package", Path: `types\event.avsc`, Files: []artifact.ContractFile{{Path: `types\event.avsc`, Content: "{}"}}},
		"drive path":               {Type: "package", Path: "C:/event.avsc", Files: []artifact.ContractFile{{Path: "C:/event.avsc", Content: "{}"}}},
		"duplicate file":           {Type: "package", Path: "event.avsc", Files: []artifact.ContractFile{{Path: "event.avsc", Content: "{}"}, {Path: "event.avsc", Content: "other"}}},
		"file directory collision": {Type: "package", Path: "types", Files: []artifact.ContractFile{{Path: "types", Content: "{}"}, {Path: "types/event.avsc", Content: "{}"}}},
		"manifest collision":       {Type: "package", Path: "manifest.json", Files: []artifact.ContractFile{{Path: "manifest.json", Content: "{}"}}},
		"remote source":            {Type: "url", URL: "https://example.invalid/schema.avsc"},
		"root digest mismatch":     {Type: "inline", Content: "{}", SHA256: "wrong"},
		"invalid base64":           {Type: "package", Path: "event.avsc", Files: []artifact.ContractFile{{Path: "event.avsc", Content: "!", Encoding: "base64"}}},
		"unknown encoding":         {Type: "package", Path: "event.avsc", Files: []artifact.ContractFile{{Path: "event.avsc", Content: "{}", Encoding: "gzip"}}},
		"unresolved import paths":  {Type: "inline", Content: "{}", ImportPaths: []string{"./types"}},
	} {
		t.Run(name, func(t *testing.T) {
			w, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.freezeSchema(artifact.SchemaRef{ID: "events", Role: "dependency", Type: "avro", Source: source}); err == nil {
				t.Fatal("invalid contract package source accepted")
			}
		})
	}
}

func TestOpaqueSingleFileAndInlineContracts(t *testing.T) {
	for _, kind := range []string{"graphql", "avro", "asyncapi"} {
		t.Run(kind, func(t *testing.T) {
			original := []byte("# preserved bytes including comments\r\n")
			name := filepath.Join(t.TempDir(), "root."+kind)
			if err := os.WriteFile(name, original, 0600); err != nil {
				t.Fatal(err)
			}
			for _, source := range []artifact.SchemaSource{{Type: "file", Path: name}, {Type: "inline", Content: string(original)}} {
				w, err := New(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if err := w.RegisterSchemas([]artifact.SchemaRef{{ID: "contract", Role: "sut", Type: kind, Source: source}}); err != nil {
					t.Fatal(err)
				}
				run, _, err := Load(w.Path())
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(run.Schemas[0].Source.Path)
				if err != nil || !bytes.Equal(data, original) {
					t.Fatalf("source bytes changed: %q, %v", data, err)
				}
			}
		})
	}
}
