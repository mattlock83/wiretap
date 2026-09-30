package bundlereport

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
	"github.com/phrond/evermock/internal/coverage"
)

func TestViewerIsOfflineRebuildableAndUsesBoundedEvidencePreviews(t *testing.T) {
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	largeBody := strings.Repeat("body-", 1000) + "END-OF-BODY"
	tc := artifact.TestCase{ID: "attempt-1", Name: `<script>alert("bad")</script>`, StartedAt: time.Now().UTC(), Status: "pass", TraceID: "test-trace-123"}
	if _, err := writer.Register(tc, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(&artifact.Artifact{Version: 1, TestCase: tc,
		CaptureDiagnostics: []string{"trace parent was uncertain"},
		SchemaValidation:   &artifact.SchemaValidation{Status: "fail", Errors: []artifact.SchemaValidationError{{Message: "response omitted required field"}}},
		Traffic: []artifact.Flow{{
			ID: "flow-1", Protocol: "http", Channel: "test_to_sut", TraceID: "test-trace-123", TestCorrelation: "trace", TraceContextSource: "received",
			Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://sut.test/items", Body: &artifact.Body{Text: largeBody}}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 201}},
		}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(writer.Path()); err != nil {
		t.Fatal(err)
	}
	viewerPath := filepath.Join(writer.Path(), "viewer", "index.html")
	html, err := os.ReadFile(viewerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"../evidence/run.json", "../evidence/attempts/attempt-1.json", "../payloads/", "preview limited to 4 KiB", "&lt;script&gt;", "data-pane=\"tests\"", "trace parent was uncertain", "response omitted required field", "test-trace-123", "received"} {
		if !bytes.Contains(html, []byte(expected)) {
			t.Fatalf("viewer missing %q", expected)
		}
	}
	if bytes.Contains(html, []byte(largeBody)) || bytes.Contains(html, []byte(`<script>alert("bad")</script>`)) {
		t.Fatal("viewer embedded full body or unescaped test name")
	}
	if err := Validate(writer.Path()); err != nil {
		t.Fatal(err)
	}
	broken := bytes.Replace(html, []byte(`href="../evidence/run.json"`), []byte(`href="../missing.json"`), 1)
	if err := os.WriteFile(viewerPath, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(writer.Path()); err == nil || !strings.Contains(err.Error(), "viewer link") {
		t.Fatalf("missing viewer link was not diagnosed: %v", err)
	}
	if err := os.WriteFile(viewerPath, html, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := coverage.Generate(t.Context(), writer.Path(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(viewerPath); err != nil {
		t.Fatal(err)
	}
	if _, err := coverage.Generate(t.Context(), writer.Path(), "", false); err != nil {
		t.Fatalf("missing viewer changed evidence calculation: %v", err)
	}
	if _, err := Rebuild(writer.Path()); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := os.ReadFile(viewerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(html, rebuilt) {
		t.Fatal("viewer did not rebuild deterministically from evidence")
	}
	after, err := coverage.Generate(t.Context(), writer.Path(), "", false)
	if err != nil || before.ArtifactCount != after.ArtifactCount {
		t.Fatalf("coverage changed after viewer rebuild: %v", err)
	}
}

func TestViewerShowsInterruptedPendingAttempt(t *testing.T) {
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Register(artifact.TestCase{ID: "pending-1", Name: "still running", StartedAt: time.Now().UTC()}, nil); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finalize("interrupted"); err != nil {
		t.Fatal(err)
	}
	if _, err := Rebuild(writer.Path()); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(writer.Path(), "viewer", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page, []byte("interrupted")) || !bytes.Contains(page, []byte("still running")) {
		t.Fatal("viewer hides interrupted pending attempt")
	}
}

func TestViewerDistinguishesProviderEvidenceMockAssumptionsAndSchemaChecks(t *testing.T) {
	root := t.TempDir()
	run := &bundle.Run{
		RunID: "run-1", APIID: "payments", SUTTargetKind: "service", RunState: "complete",
		DependencyKinds: map[string]string{"accounts": "mock"},
		Attempts:        []bundle.AttemptRef{{AttemptID: "attempt-1", FileRef: bundle.FileRef{Path: "evidence/attempts/attempt-1.json"}}},
	}
	tc := artifact.TestCase{ID: "attempt-1", Name: "withdraw funds", Status: "pass"}
	capture := &artifact.Artifact{
		TestCase: tc, SchemaValidation: &artifact.SchemaValidation{Status: "fail"},
		Traffic: []artifact.Flow{
			{ID: "inbound", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://payments/withdraw"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 201}}},
			{ID: "outbound", Channel: "sut_to_dependency", AssetID: "accounts", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://accounts/balance"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}},
		},
	}
	sample := coverage.FlowSample{TestCaseID: tc.ID, TestCase: tc.Name, TestStatus: "pass", FlowID: "inbound", Request: coverage.InteractionMessage{Method: "POST", URL: "http://payments/withdraw"}, Response: coverage.InteractionMessage{Status: 201}}
	report := &coverage.Report{
		Contracts: &coverage.ContractCoverage{Complete: true},
		Provided:  coverage.SpecCoverageGroup{Specs: []coverage.SpecCoverage{{SchemaID: "payments", DenominatorKnown: true, Exercised: []coverage.OperationSamples{{Operation: "POST /withdraw", Samples: []coverage.FlowSample{sample}}}}}},
		Dependent: coverage.SpecCoverageGroup{Specs: []coverage.SpecCoverage{{SchemaID: "accounts", AssetID: "accounts"}}},
	}
	if err := writeViewer(root, run, []*artifact.Artifact{capture}, report, nil); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(root, "viewer", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"<h1>payments</h1>", "Provider observation", "Mock assumption",
		"Tests → payments", "payments → accounts", "2. GET /balance",
		"Test outcome: pass · attempt schema validation: fail",
		"Agreement with the real provider", "Missing evidence means unknown agreement",
		"../system.md", "../knowledge.json", "../verification.md",
	} {
		if !bytes.Contains(page, []byte(expected)) {
			t.Errorf("viewer missing %q", expected)
		}
	}
}

func TestViewerEvidenceDoesNotPresentAllDependenciesAsMocks(t *testing.T) {
	for _, tc := range []struct {
		kind, targetKind, label, explanation string
	}{
		{"provided", "service", "Provider observation", "recorded test environment"},
		{"dependency", "service", "Dependency observation", "other versions or environments"},
		{"provided", "mock", "Mock assumption", "real provider"},
		{"dependency", "mock", "Mock assumption", "real provider"},
		{"dependency", "unknown", "Unclassified observation", "Classify it"},
		{"dependency", "", "Unclassified observation", "Classify it"},
	} {
		t.Run(tc.kind+"/"+tc.targetKind, func(t *testing.T) {
			label, explanation := viewerEvidence(tc.kind, tc.targetKind)
			if label != tc.label || !strings.Contains(explanation, tc.explanation) {
				t.Fatalf("got (%q, %q), want %q with %q", label, explanation, tc.label, tc.explanation)
			}
		})
	}
}

func TestViewerSequenceUsesRequestStartsAndExplainsMissingTimestamps(t *testing.T) {
	started := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	for _, missingTimestamp := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing_timestamp=%t", missingTimestamp), func(t *testing.T) {
			root := t.TempDir()
			run := &bundle.Run{RunID: "run-1", APIID: "payments", SUTTargetKind: "service", DependencyKinds: map[string]string{"accounts": "mock"}, Attempts: []bundle.AttemptRef{{AttemptID: "attempt-1", FileRef: bundle.FileRef{Path: "evidence/attempts/attempt-1.json"}}}}
			capture := &artifact.Artifact{TestCase: artifact.TestCase{ID: "attempt-1"}, Traffic: []artifact.Flow{
				{ID: "flow-1", StartedAt: started.Add(time.Second), Channel: "sut_to_dependency", AssetID: "accounts", TestCorrelation: "active_session", ParentCorrelation: "timing", ParentFlowID: "flow-2", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://accounts/balance"}}},
				{ID: "flow-2", StartedAt: started, Channel: "test_to_sut", TestCorrelation: "trace", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://payments/withdraw"}}},
			}}
			if missingTimestamp {
				capture.Traffic[0].StartedAt = time.Time{}
			}
			if err := writeViewer(root, run, []*artifact.Artifact{capture}, &coverage.Report{Contracts: &coverage.ContractCoverage{}}, nil); err != nil {
				t.Fatal(err)
			}
			page, err := os.ReadFile(filepath.Join(root, "viewer", "index.html"))
			if err != nil {
				t.Fatal(err)
			}
			first, second, explanation := "1. POST /withdraw", "2. GET /balance", "Ordered by request start time; calls may overlap."
			if missingTimestamp {
				first, second, explanation = "1. GET /balance", "2. POST /withdraw", "Capture order is shown because some request timestamps are missing"
			}
			text := string(page)
			if start, end := strings.Index(text, first), strings.Index(text, second); start < 0 || end < 0 || start >= end {
				t.Fatalf("expected %q before %q", first, second)
			}
			for _, expected := range []string{explanation, "W3C trace", "single active test session (fallback)", "request timing overlap (inferred)"} {
				if !strings.Contains(text, expected) {
					t.Errorf("viewer missing %q", expected)
				}
			}
			if capture.Traffic[0].ID != "flow-1" {
				t.Fatal("rendering changed canonical capture order")
			}
		})
	}
}
