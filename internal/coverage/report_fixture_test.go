package coverage

import (
	"context"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestGenerateReadsOnlyBundleInventory(t *testing.T) {
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	capture := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "tc-1", Name: "hello", StartedAt: time.Now().UTC(), Status: "pass"}}
	if _, err := writer.RegisterPrepared(capture.TestCase, nil); err != nil {
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

func TestGenerateIndexesUnnamedAndEmptyAttemptsOnce(t *testing.T) {
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	flow := func(id string) artifact.Flow {
		return artifact.Flow{ID: id, Protocol: "http", Channel: "test_to_sut", Request: &artifact.HttpMessage{Method: "GET", URL: "http://sut/items"}, Response: &artifact.HttpMessage{Status: 204}}
	}
	captures := []*artifact.Artifact{
		{TestCase: artifact.TestCase{ID: "a-empty", Name: " grouped ", Status: "fail"}, CaptureDiagnostics: []string{"capture ended before traffic"}},
		{TestCase: artifact.TestCase{ID: "b-traffic", Name: "grouped", Status: "pass"}, Traffic: []artifact.Flow{flow("one"), flow("two")}},
		{TestCase: artifact.TestCase{ID: "c-skipped", Name: "grouped", Status: "skip"}},
		{TestCase: artifact.TestCase{ID: "unnamed", Name: " \t", Status: "pass"}, Traffic: []artifact.Flow{flow("unnamed-flow")}},
		{TestCase: artifact.TestCase{ID: "empty-unnamed", Status: "fail"}},
	}
	for _, capture := range captures {
		capture.Version = 1
		capture.TestCase.StartedAt = time.Now().UTC()
		if _, err := writer.RegisterPrepared(capture.TestCase, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Commit(capture); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Generate(context.Background(), writer.Path(), "test", false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var attempts [][]string
	var outcomes [][]string
	var traffic []int
	for _, testcase := range report.TestCases {
		names = append(names, testcase.Name)
		var ids, statuses []string
		for _, attempt := range testcase.Attempts {
			ids, statuses = append(ids, attempt.ID), append(statuses, attempt.Status)
		}
		attempts, outcomes = append(attempts, ids), append(outcomes, statuses)
		traffic = append(traffic, len(testcase.Traffic))
		for _, sample := range testcase.Traffic {
			if sample.TestCase != testcase.Name {
				t.Fatalf("traffic is grouped under a different name: %+v", testcase)
			}
		}
	}
	if report.ArtifactCount != 5 || !reflect.DeepEqual(names, []string{"empty-unnamed", "grouped", "unnamed"}) ||
		!reflect.DeepEqual(attempts, [][]string{{"empty-unnamed"}, {"b-traffic", "a-empty", "c-skipped"}, {"unnamed"}}) ||
		!reflect.DeepEqual(outcomes, [][]string{{"fail"}, {"pass", "fail", "skip"}, {"pass"}}) ||
		!reflect.DeepEqual(traffic, []int{0, 2, 1}) {
		t.Fatalf("attempts were duplicated, omitted or reordered: names=%v attempts=%v outcomes=%v traffic=%v", names, attempts, outcomes, traffic)
	}
	if !reflect.DeepEqual(report.Notes, []string{"a-empty: capture ended before traffic"}) {
		t.Fatalf("empty-attempt diagnostics lost: %v", report.Notes)
	}
}
