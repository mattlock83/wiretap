package knowledge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

func TestCompletionOrderDoesNotChangePublication(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout", "checkout")
	c := configFixture("checkout")
	c.Tests[0].AttemptOrdinals = map[string]int{"checkouta": 1, "checkoutb": 0}
	archive, err := snapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	run, captures, err := bundle.Load(input)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := buildCandidate(input, archive, nil, run, captures, c)
	if err != nil {
		t.Fatal(err)
	}
	captures[0], captures[1] = captures[1], captures[0]
	second, _, err := buildCandidate(input, archive, nil, run, captures, c)
	if err != nil {
		t.Fatal(err)
	}
	for p, data := range first {
		if !bytes.Equal(data, second[p]) {
			t.Errorf("completion order changed %s", p)
		}
	}
}

func TestContractLinkOrderUsesStableIdentity(t *testing.T) {
	s := Object{"id": "api", "kind": "provided-api", "title": "API", "page": "apis/api/api.md", "contract_ids": []any{"z", "a"}}
	contracts := map[string]Object{"z": {"key": "z", "manifest": "contracts/z/manifest.json"}, "a": {"key": "a", "manifest": "contracts/a/manifest.json"}}
	a, err := renderSubject(s, nil, contracts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s["contract_ids"] = []any{"a", "z"}
	b, err := renderSubject(s, nil, contracts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("set ordering changed page")
	}
}

func TestSDKIdentitySurvivesInterruptedRetry(t *testing.T) {
	base := t.TempDir()
	w, err := bundle.NewAt(filepath.Join(base, "run"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	tc := artifact.TestCase{ID: "first", Name: "dynamic display name", StartedAt: start, FinishedAt: start.Add(time.Second), Status: "fail", Meta: map[string]any{"application_knowledge": Object{"test_id": "test:checkout", "execution_group": "integration", "attempt_ordinal": 0}}}
	if _, err := w.Register(tc, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit(&artifact.Artifact{Version: 3, TestCase: tc, Traffic: []artifact.Flow{}}); err != nil {
		t.Fatal(err)
	}
	tc.ID, tc.FinishedAt, tc.Status = "interrupted", time.Time{}, ""
	object(tc.Meta["application_knowledge"])["attempt_ordinal"] = 1
	if _, err := w.Register(tc, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Finalize("interrupted"); err != nil {
		t.Fatal(err)
	}
	c := configFixture("checkout")
	c.Tests = nil
	output := filepath.Join(base, "knowledge")
	if _, err := Publish(w.Path(), output, c, false); err != nil {
		t.Fatal(err)
	}
	retry := readJSON(t, filepath.Join(output, "evidence/tests/checkout/integration-retry-1/attempt.json"))
	if retry["attempt_id"] != "interrupted" || retry["outcome"] != "unknown" || retry["attempt_ordinal"] != json.Number("1") {
		t.Fatalf("lost interrupted identity: %+v", retry)
	}
}

func TestRemovalCanBeReapplied(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	c := configFixture("checkout")
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	c.Subjects = c.Subjects[:1]
	c.Selection["test_ids"] = []any{}
	c.RemovedSubjects = []Object{{"id": "test:checkout", "kind": "test", "key": "checkout", "path": "tests/checkout/test.md", "reason": "Retired"}}
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	result, err := Publish(input, output, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("removal is not a no-op: %+v", result.Changes)
	}
}

// Change a registered input while retaining consistent integrity metadata, so
// validation must check semantics rather than just detect a stale digest.
func replaceRegistered(t *testing.T, root, p string, value any) {
	t.Helper()
	writeJSON(t, filepath.Join(root, p), value)
	data, err := os.ReadFile(filepath.Join(root, p))
	if err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, filepath.Join(root, "bundle.json"))
	for _, e := range list(m["files"]) {
		e := object(e)
		if e["path"] == p {
			e["sha256"] = hash(data)
		}
	}
	writeJSON(t, filepath.Join(root, "bundle.json"), m)
}

func TestSameOwnerDoesNotAuthorizeProducerChange(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	c := configFixture("checkout")
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	p := readJSON(t, filepath.Join(output, "publication.json"))
	object(p["producer"])["name"] = "another-tool"
	replaceRegistered(t, output, "publication.json", p)
	before, _ := snapshot(output)
	if _, err := Publish(input, output, c, false); err == nil || !strings.Contains(err.Error(), "handoff") {
		t.Fatalf("producer handoff: %v", err)
	}
	after, _ := snapshot(output)
	if !sameSnapshot(before, after) {
		t.Fatal("conflict changed publication")
	}
}

func TestInvalidSubjectGraphFailsWithoutRecursion(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"))
	c := configFixture()
	c.Subjects = append(c.Subjects, Object{"id": "cycle", "key": "cycle", "title": "Cycle", "kind": "operation", "parent_id": "cycle", "protocol": "http", "operation": "GET /"})
	if _, err := Publish(input, filepath.Join(base, "knowledge"), c, true); err == nil {
		t.Fatal("accepted cycle")
	}
	if got := subjectPath(c.Subjects[1], map[string]Object{"cycle": c.Subjects[1]}); got != "" {
		t.Fatalf("invalid path: %s", got)
	}
}

func TestMissingBundleIsNotValid(t *testing.T) {
	if v := Validate(filepath.Join(t.TempDir(), "missing")); v.Valid() {
		t.Fatalf("missing directory valid: %+v", v)
	}
}

func TestExecutionIdentityIsGlobal(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout", "refund")
	output := filepath.Join(base, "knowledge")
	if _, err := Publish(input, output, configFixture("checkout", "refund"), false); err != nil {
		t.Fatal(err)
	}
	a := readJSON(t, filepath.Join(output, "evidence/tests/checkout/primary/attempt.json"))
	p := "evidence/tests/refund/primary/attempt.json"
	b := readJSON(t, filepath.Join(output, p))
	b["run_id"], b["attempt_id"], b["artifacts"] = a["run_id"], a["attempt_id"], []any{}
	replaceRegistered(t, output, p, b)
	v := Validate(output)
	for _, d := range v.Diagnostics {
		if d.Requirement == "ID-08" {
			return
		}
	}
	t.Fatalf("duplicate global execution accepted: %+v", v)
}

func TestProfileYAMLRejectsAliasesButPlainOKFRemainsIndependent(t *testing.T) {
	b := []byte("---\ntype: &kind Note\ncustom: *kind\n---\nBody\n")
	if _, _, err := frontmatter(b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := frontmatter(b, true); err == nil {
		t.Fatal("profile accepted YAML alias")
	}
}

func TestContractSourceIdentifiers(t *testing.T) {
	for _, source := range []string{"/Users/example/schema.json", "file:///tmp/schema.json", "C:/project/schema.json", `C:\project\schema.json`, "https://user:secret@example.com/schema.json", "//server/share/schema.json"} {
		if safeSourceIdentifier(source) {
			t.Errorf("accepted unsafe source %q", source)
		}
	}
	for _, source := range []string{"../shared/schema.json", "api.json", "https://example.com/schema.json", "urn:schemas:api"} {
		if !safeSourceIdentifier(source) {
			t.Errorf("rejected portable source %q", source)
		}
	}
}

func TestUnknownEvidenceMetadataSurvivesNoOp(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	c := configFixture("checkout")
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	p := "evidence/tests/checkout/primary/attempt.json"
	a := readJSON(t, filepath.Join(output, p))
	object(list(a["artifacts"])[0])["future_artifact"] = "keep"
	object(a["capture_scope"])["future_scope"] = "keep"
	replaceRegistered(t, output, p, a)
	m := readJSON(t, filepath.Join(output, "bundle.json"))
	object(list(m["evidence"])[0])["future_slot"] = "keep"
	writeJSON(t, filepath.Join(output, "bundle.json"), m)
	result, err := Publish(input, output, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("optional evidence metadata changed: %+v", result.Changes)
	}
}

func TestRootNavigationCannotBeEmpty(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	if _, err := Publish(input, output, configFixture("checkout"), false); err != nil {
		t.Fatal(err)
	}
	data := []byte("---\nokf_version: \"0.2\"\n---\n# Empty\n")
	if err := os.WriteFile(filepath.Join(output, "index.md"), data, 0644); err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, filepath.Join(output, "bundle.json"))
	for _, file := range list(m["files"]) {
		if object(file)["path"] == "index.md" {
			object(file)["sha256"] = hash(data)
		}
	}
	writeJSON(t, filepath.Join(output, "bundle.json"), m)
	v := Validate(output)
	if v.BaseOKF != "valid" || v.Application != "invalid" {
		t.Fatalf("empty navigation: %+v", v)
	}
}

func TestSourceArchiveRejectsTrailingJSON(t *testing.T) {
	input := captureFixture(t, filepath.Join(t.TempDir(), "run"), "checkout")
	p := filepath.Join(input, "bundle.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(data, []byte("{}")...), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bundle.Load(input); err == nil {
		t.Fatal("accepted trailing JSON in source archive")
	}
}
