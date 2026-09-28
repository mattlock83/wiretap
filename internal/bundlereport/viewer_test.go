package bundlereport

import (
	"bytes"
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
