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

func TestExamplesAndIndependentOKFLevels(t *testing.T) {
	for _, name := range []string{"minimal", "tested-api", "wiretap-http"} {
		t.Run(name, func(t *testing.T) {
			v := Validate(filepath.Join("../../docs/spec/examples", name))
			if !v.Valid() || v.Application != "valid" {
				t.Fatalf("%+v", v)
			}
		})
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "note.md"), []byte("---\ntype: Custom thing\nunknown: preserved\n---\n[broken](missing.md)\n"), 0644)
	v := Validate(root)
	if v.BaseOKF != "valid" || v.Application != "not-present" {
		t.Fatalf("plain OKF: %+v", v)
	}
	os.MkdirAll(filepath.Join(root, "authored"), 0755)
	os.WriteFile(filepath.Join(root, "authored", "bad.md"), []byte("unlisted invalid page"), 0644)
	if v := Validate(root); v.BaseOKF != "invalid" {
		t.Fatalf("unlisted Markdown: %+v", v)
	}
}
func configFixture(ids ...string) Config {
	subjects := []Object{{"id": "application", "kind": "application", "key": "shop", "title": "Shop"}}
	tests := []TestMapping{}
	selected := []any{}
	for _, id := range ids {
		subjects = append(subjects, Object{"id": "test:" + id, "kind": "test", "key": id, "title": id, "parent_id": "application"})
		tests = append(tests, TestMapping{ID: "test:" + id, Name: id})
		selected = append(selected, "test:"+id)
	}
	return Config{Version: 1, BundleID: "urn:test:shop:knowledge", Application: Object{"id": "urn:test:shop", "title": "Shop"}, Subjects: subjects, Tests: tests, Selection: Object{"mode": "replace-selected-tests", "scope": "complete", "test_ids": selected}}
}
func captureFixture(t *testing.T, root string, names ...string) string {
	t.Helper()
	w, err := bundle.NewAt(root)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		tc := artifact.TestCase{ID: strings.ReplaceAll(name, " ", "-") + string(rune('a'+i)), Name: name, StartedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 10, 6, 0, 0, 1, 0, time.UTC), Status: "passed"}
		if _, err := w.Register(tc, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Commit(&artifact.Artifact{Version: 3, TestCase: tc, Traffic: []artifact.Flow{}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return w.Path()
}
func TestPublicationNoOpAndBoundedFreshExecutionDiff(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	c := configFixture("checkout")
	first, err := Publish(input, output, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Changes) == 0 {
		t.Fatal("check must describe candidate")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("check wrote destination")
	}
	written, err := Publish(input, output, c, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written.Changes) != len(first.Changes) {
		t.Fatal("check differs from publish")
	}
	before, _ := snapshot(output)
	noOp, err := Publish(input, output, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(noOp.Changes) != 0 {
		t.Fatalf("identical input changed: %+v", noOp.Changes)
	}
	after, _ := snapshot(output)
	if !sameSnapshot(before, after) {
		t.Fatal("check changed bytes")
	}
	input2 := captureFixture(t, filepath.Join(base, "fresh"), "checkout")
	fresh, err := Publish(input2, output, c, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range fresh.Changes {
		if strings.HasSuffix(change.Path, ".md") {
			t.Fatalf("new execution rewrote concept: %+v", change)
		}
	}
	if len(fresh.Changes) == 0 {
		t.Fatal("fresh execution must change evidence")
	}
	if _, err := Read(output); err != nil {
		t.Fatal(err)
	}
}
func TestOwnershipAndAuthoredPreservation(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	c := configFixture("checkout")
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	authored := []byte("---\ntype: Team note\ncustom: true\n---\nKeep me.\n")
	p := filepath.Join(output, "team.md")
	os.WriteFile(p, authored, 0644)
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !bytes.Equal(b, authored) {
		t.Fatal("authored file changed")
	}
	os.WriteFile(filepath.Join(output, "system.md"), []byte("hand edit"), 0644)
	if _, err := Publish(input, output, c, false); err == nil {
		t.Fatal("accepted generated edit")
	}
	b, _ = os.ReadFile(filepath.Join(output, "system.md"))
	if string(b) != "hand edit" {
		t.Fatal("lost user edit")
	}
}
func TestCorruptionDoesNotInvalidateOKF(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	if _, err := Publish(input, output, configFixture("checkout"), false); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(output, "evidence/tests/checkout/primary/recording.json"), []byte("corrupt"), 0644)
	v := Validate(output)
	if v.BaseOKF != "valid" || v.Application != "invalid" {
		t.Fatalf("integrity conflated with OKF: %+v", v)
	}
}
func TestRetryOrderRequiresHarnessIdentity(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout", "checkout")
	c := configFixture("checkout")
	output := filepath.Join(base, "knowledge")
	if _, err := Publish(input, output, c, true); err == nil || !strings.Contains(err.Error(), "ordinals") {
		t.Fatalf("ambiguous retries: %v", err)
	}
	c.Tests[0].AttemptOrdinals = map[string]int{"checkouta": 1, "checkoutb": 0}
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(output, "evidence/tests/checkout/retry-1/attempt.json"))
	v, _ := Decode(b)
	if object(v)["attempt_id"] != "checkouta" {
		t.Fatal("used completion order instead of harness ordinal")
	}
}
func TestCanonicalPreciseAndLiteralStrings(t *testing.T) {
	v, err := Decode([]byte(`{"z":9007199254740991,"a":"<α> ","literal":"\\u2028"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Canonical(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `\u003c`) || !strings.Contains(string(b), "9007199254740991") || !strings.Contains(string(b), `\\u2028`) {
		t.Fatalf("bad canonical bytes: %s", b)
	}
	if _, err := Decode([]byte(`{"a":1,"a":2}`)); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := Decode([]byte(`{"a":"\ud800"}`)); err == nil {
		t.Fatal("invalid Unicode repaired silently")
	}
	y, err := YAML(Object{"n": json.Number("9007199254740993")})
	if err != nil || strings.Contains(string(y), `"9007199254740993"`) {
		t.Fatalf("YAML number became string: %s %v", y, err)
	}
}
