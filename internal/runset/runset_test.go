package runset

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
)

func TestCoverageUnionKeepsUncertaintyAndSourceEvidence(t *testing.T) {
	archive := func(state, run string) Archive {
		return Archive{report: &coverage.Report{Contracts: &coverage.ContractCoverage{Specs: []*coverage.ContractSpec{{APIID: "api", SchemaID: "schema", Origin: "provided", Revision: "digest", RulesVersion: "rules", Targets: []*coverage.ContractTarget{{Key: "condition", Family: "operation", Operation: "GET /", State: state, Evidence: []coverage.ContractEvidence{{RunID: run}}}}}}}}}
	}
	for _, states := range [][2]string{{"not_covered", "unknown"}, {"unknown", "not_covered"}} {
		result, err := (&Set{Archives: []Archive{archive(states[0], "a"), archive(states[1], "b")}}).Coverage(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Conditions) != 1 || result.Conditions[0].State != "unknown" || len(result.Conditions[0].Evidence) != 2 || result.Conditions[0].Revision != "digest" {
			t.Fatalf("lossy union: %+v", result)
		}
	}
	result, err := (&Set{Archives: []Archive{archive("unknown", "a"), archive("covered", "b")}}).Coverage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result.Conditions[0].State != "covered" || result.Conditions[0].Evidence[1].RunID != "b" {
		t.Fatalf("missing witness: %+v", result)
	}
}

func runSetEmptyArchive(t *testing.T) string {
	t.Helper()
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(bundle.Identity{Application: &bundle.Application{ID: "app"}, APIID: "api", SUTTargetKind: "service", SUTSource: map[string]any{"revision": "one-build"}}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return writer.Path()
}

func saveRunSet(t *testing.T, path string, manifest *Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageIsDeferredAndRejectsChangedArchive(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "unchanged"
		if changed {
			name = "changed after load"
		}
		t.Run(name, func(t *testing.T) {
			archive := runSetEmptyArchive(t)
			path := filepath.Join(t.TempDir(), "set.json")
			manifest, err := Create(t.Context(), []string{archive}, path)
			if err != nil {
				t.Fatal(err)
			}
			saveRunSet(t, path, manifest)
			set, err := Load(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			if set.Archives[0].report != nil {
				t.Fatal("loading a run set eagerly generated coverage")
			}
			if changed {
				file := filepath.Join(archive, "bundle.json")
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, append(data, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			report, err := set.Coverage(t.Context())
			if changed {
				if err == nil || !strings.Contains(err.Error(), "changed before coverage") || set.Archives[0].report != nil {
					t.Fatalf("changed archive was reported or cached: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Members) != 1 || report.Members[0].Capture.RunID != set.Archives[0].Run.RunID {
				t.Fatalf("deferred report lost its source run: %+v", report)
			}
		})
	}
}

func TestManifestPathsSurviveDirectoryAndFileAliases(t *testing.T) {
	archive := runSetEmptyArchive(t)
	canonicalArchive, err := filepath.EvalSymlinks(archive)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	physical := filepath.Join(root, "nested", "sets")
	if err := os.MkdirAll(physical, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(physical, alias); err != nil {
		t.Skip(err)
	}
	path := filepath.Join(alias, "set.json")
	manifest, err := Create(t.Context(), []string{archive}, path)
	if err != nil {
		t.Fatal(err)
	}
	saveRunSet(t, path, manifest)
	fileAlias := filepath.Join(root, "set-alias.json")
	if err := os.Symlink(filepath.Join(physical, "set.json"), fileAlias); err != nil {
		t.Skip(err)
	}
	for _, input := range []string{path, filepath.Join(physical, "set.json"), fileAlias} {
		set, err := Load(t.Context(), input)
		if err != nil || len(set.Archives) != 1 || set.Archives[0].Path != canonicalArchive {
			t.Fatalf("manifest via %s lost relative member: %v, %v", input, set, err)
		}
	}
}

func TestRunSetMemberOrderingIsStable(t *testing.T) {
	first, second := runSetEmptyArchive(t), runSetEmptyArchive(t)
	path := filepath.Join(t.TempDir(), "set.json")
	forward, err := Create(t.Context(), []string{first, second}, path)
	if err != nil {
		t.Fatal(err)
	}
	backward, err := Create(t.Context(), []string{second, first}, path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(forward, backward) {
		t.Fatalf("member argument order changed the manifest: %+v, %+v", forward, backward)
	}
	backward.Members[0], backward.Members[1] = backward.Members[1], backward.Members[0]
	saveRunSet(t, path, backward)
	set, err := Load(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if set.Archives[0].Run.RunID > set.Archives[1].Run.RunID {
		t.Fatal("loaded manifest order changed source evidence ordering")
	}
}

func TestRunSetRejectsDuplicateJSONDeclarations(t *testing.T) {
	archive := runSetEmptyArchive(t)
	path := filepath.Join(t.TempDir(), "set.json")
	manifest, err := Create(t.Context(), []string{archive}, path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"run_set_version":"1.0"`, `"run_set_version":"unsupported","run_set_version":"1.0"`, 1))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), path); err == nil {
		t.Fatal("duplicate declarations selected the last value")
	}
}

func TestRunSetRejectsFIFOWithoutOpeningIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix named pipe")
	}
	if _, err := exec.LookPath("mkfifo"); err != nil {
		t.Skip("mkfifo is unavailable")
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if output, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Fatalf("create FIFO: %v: %s", err, output)
	}
	result := make(chan error, 1)
	go func() {
		_, err := Load(t.Context(), path)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("named pipe was not rejected: %v", err)
		}
	case <-time.After(time.Second):
		// Release a regressed blocking Open before reporting failure, so no
		// goroutine or open pipe is left behind in the test process.
		file, err := os.OpenFile(path, os.O_RDWR, 0600)
		if err == nil {
			_ = file.Close()
		}
		<-result
		t.Fatal("run set tried to open a named pipe before rejecting it")
	}
}

func TestRunSetCreateRejectsExcessOrCancelledWorkBeforeReading(t *testing.T) {
	paths := make([]string, MaxMembers+1)
	for i := range paths {
		paths[i] = filepath.Join(t.TempDir(), "missing")
	}
	if _, err := Create(t.Context(), paths, "set.json"); err == nil || !strings.Contains(err.Error(), "members") {
		t.Fatalf("oversized member list reached the filesystem: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Create(ctx, paths[:1], "set.json"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled creation reached the filesystem: %v", err)
	}
}
