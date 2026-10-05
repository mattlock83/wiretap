package bundlereport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
)

func TestActiveExplorationViewsKeepInvocationsAndLocalLineage(t *testing.T) {
	root := viewerNavigationFixture(t)
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	var knowledge knowledgeIndex
	if err := json.Unmarshal([]byte(read("docs/knowledge.json")), &knowledge); err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	for _, attempt := range knowledge.Attempts {
		if attempt.Exploration == nil {
			continue
		}
		x := attempt.Exploration
		calls[x.InvocationID]++
		if x.SourceKind != "active_test" || x.SourceRunID != knowledge.RunID || x.SourceAttemptID != "authored" || x.SourceAttemptPath != "tests/authored.json" || len(x.SourceFlowIDs) != 1 || x.SourceFlowIDs[0] != "request" || x.SourceSHA256 != "" {
			t.Fatalf("incorrect active-test lineage: %+v", x)
		}
	}
	if calls["explore-a"] != 2 || calls["explore-b"] != 2 {
		t.Fatalf("invocations merged or omitted: %v", calls)
	}
	if strings.Contains(read("docs/knowledge.json"), `"source_sha256"`) {
		t.Fatal("active source acquired an invented file hash")
	}
	for _, path := range []string{"docs/run.md", "docs/tests/baseline-a.md", "docs/viewer.html"} {
		page := read(path)
		if !strings.Contains(page, "explore-a") {
			t.Errorf("%s lost invocation identity", path)
		}
		for _, obsolete := range []string{"source hash </p>", "SHA-256 ``", "does not capture downstream", "source bundle itself is not embedded"} {
			if strings.Contains(page, obsolete) {
				t.Errorf("%s has obsolete exploration claim %q", path, obsolete)
			}
		}
	}
	if !strings.Contains(read("docs/run.md"), "[authored](tests/authored.md)") {
		t.Fatal("run view does not link its local parent")
	}
	if !strings.Contains(read("docs/viewer.html"), `href="#attempt-`+shortHash("authored")+`">Parent authored test`) {
		t.Fatal("viewer does not link generated evidence to its parent")
	}
	if err := Validate(root); err != nil {
		t.Fatal(err)
	}
}

func TestActiveExplorationDoesNotInventPendingParentPath(t *testing.T) {
	x := &artifact.Exploration{Version: "wiretap-negative-1", SourceKind: "active_test", SourceRunID: "run", SourceAttemptID: "parent", InvocationID: "explore-a", SourceFlowIDs: []string{"flow-1"}, Mode: "baseline"}
	capture := &artifact.Artifact{TestCase: artifact.TestCase{ID: "baseline", Name: "test [baseline]", Status: "pass"}, Exploration: x}
	run := &bundle.Run{RunID: "run", RunState: "open", Attempts: []bundle.AttemptRef{{AttemptID: "baseline", FileRef: bundle.FileRef{Path: "tests/baseline.json"}}}}
	report := &coverage.Report{}
	knowledge := buildKnowledge(t.TempDir(), run, []*artifact.Artifact{capture}, report)
	if knowledge.Attempts[0].Exploration.SourceAttemptPath != "" {
		t.Fatal("uncommitted parent linked as existing evidence")
	}
	if !strings.Contains(runMarkdown(run, []*artifact.Artifact{capture}), "not yet committed") {
		t.Fatal("pending parent not explained")
	}
	run.Attempts = append(run.Attempts, bundle.AttemptRef{AttemptID: "parent", FileRef: bundle.FileRef{Path: "tests/parent.json"}})
	knowledge = buildKnowledge(t.TempDir(), run, []*artifact.Artifact{capture}, report)
	if knowledge.Attempts[0].Exploration.SourceAttemptPath != "tests/parent.json" {
		t.Fatal("committed parent did not become navigable")
	}
}
