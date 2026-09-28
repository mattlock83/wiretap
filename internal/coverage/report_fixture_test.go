package coverage

import (
	"context"
	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateReadsOnlyBundleInventory(t *testing.T) {
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	capture := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "tc-1", Name: "hello", StartedAt: time.Now().UTC(), Status: "pass"}}
	if _, err := writer.Register(capture.TestCase, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(capture); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(writer.Path(), "unrelated.yaml"), []byte("unrelated: true"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := Generate(context.Background(), writer.Path(), "hello", true)
	if err != nil {
		t.Fatal(err)
	}
	if report.ArtifactCount != 1 || len(report.Artifacts) != 1 || filepath.Base(report.Artifacts[0]) != "tc-1.json" {
		t.Fatalf("unexpected evidence: %#v", report)
	}
	loose := t.TempDir()
	if err := os.WriteFile(filepath.Join(loose, "tc-1.json"), []byte(`{"version":1,"testcase":{"id":"tc-1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(context.Background(), loose, "", false); err == nil {
		t.Fatal("accepted a loose capture without a bundle")
	}
}
