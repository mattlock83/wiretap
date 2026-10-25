package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattlock83/wiretap/internal/assumptions"
	"github.com/mattlock83/wiretap/internal/coverage"
)

func TestTerminalCheckSamplesDetailsWithoutChangingCompleteResult(t *testing.T) {
	report := formattedCheckFixture()
	report.Counts = assumptions.Counts{Assumptions: 20, Verified: 7, Mismatched: 13}
	for i := 0; i < 20; i++ {
		finding := assumptions.Finding{Kind: "mismatch", Code: fmt.Sprintf("finding-%02d", i), Message: strings.Repeat("☃", 1000) + "\n\x1b[31m\u009b"}
		for j := 0; j < 8; j++ {
			finding.Provider = append(finding.Provider, assumptions.Evidence{TestName: "provider", EvidencePath: fmt.Sprintf("evidence-%d", j)})
			finding.Differences = append(finding.Differences, fmt.Sprintf("$.field-%d", j))
			finding.Excerpts = append(finding.Excerpts, assumptions.Difference{Path: fmt.Sprintf("$.field-%d", j), Expected: "a", Actual: "b"})
		}
		report.Findings = append(report.Findings, finding)
		report.ExcludedProviderEvidence = append(report.ExcludedProviderEvidence, assumptions.ExcludedEvidence{Reason: fmt.Sprintf("excluded-%d", i)})
	}
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeCheckText(&out, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FAIL:", "7 of 20 mock examples verified", "mismatched: 13", "11 more findings", "5 more provider candidates", "5 more differences", "5 more value excerpts", "17 more excluded provider exchanges", "--format json"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "finding-19") || strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "\u009b") || !utf8.Valid(out.Bytes()) || out.Len() > 25<<10 {
		t.Fatalf("terminal check is unsafe or unbounded: %d bytes", out.Len())
	}
	after, _ := json.Marshal(report)
	if !bytes.Equal(before, after) || !bytes.Contains(after, []byte("finding-19")) {
		t.Fatal("terminal presentation changed the complete JSON result")
	}
}

func TestTerminalStoreCheckPrioritizesFailuresAndKeepsAggregateAssurance(t *testing.T) {
	store := &assumptions.StoreReport{Mode: "consumer", Counts: assumptions.Counts{Assumptions: 11, Verified: 10, Mismatched: 1}}
	for i := 0; i < 10; i++ {
		store.Comparisons = append(store.Comparisons, &assumptions.Report{Compatible: true, Dependency: fmt.Sprintf("passing-%d", i), Matches: []assumptions.Match{{ContractCheck: "unavailable"}}})
	}
	failed := formattedCheckFixture()
	failed.Dependency = "failed-last"
	store.Comparisons = append(store.Comparisons, failed)
	var out bytes.Buffer
	if err := writeStoreCheckText(&out, store); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FAIL: check consumer — 11 comparisons", "10 example-only matches", "failed-last", "6 more comparisons"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
	if strings.Index(out.String(), "failed-last") > strings.Index(out.String(), "passing-0") || strings.Contains(out.String(), "passing-9") || store.Comparisons[0].Dependency != "passing-0" {
		t.Fatal("failure prioritization hid the failure or changed source order")
	}
}

func TestTerminalComparisonBoundsEveryCollectionAndRetainsTotals(t *testing.T) {
	diff := &coverage.ReportDiff{BaselineRunID: "before\n\x1b[31m", CurrentRunID: "after"}
	for i := 0; i < 15; i++ {
		target := coverage.TargetSnapshot{APIID: fmt.Sprintf("api-%02d", i), Operation: strings.Repeat("☃", 1000), TargetKey: fmt.Sprintf("target-%02d", i)}
		change := coverage.CoverageChange{Before: target, After: target}
		diff.LostCoverage = append(diff.LostCoverage, change)
		diff.GainedCoverage = append(diff.GainedCoverage, change)
		diff.AddedTargets = append(diff.AddedTargets, target)
		diff.RemovedTargets = append(diff.RemovedTargets, target)
		schemas := []coverage.SchemaSnapshot{{SchemaID: "a"}, {SchemaID: "b"}, {SchemaID: "c"}, {SchemaID: "hidden-schema"}}
		diff.SchemaChanges = append(diff.SchemaChanges, coverage.SchemaChange{APIID: target.APIID, Before: schemas, After: schemas})
		diff.Notes = append(diff.Notes, fmt.Sprintf("note-%02d", i))
	}
	before, _ := json.Marshal(diff)
	var out bytes.Buffer
	if err := writeComparisonText(&out, diff); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Lost coverage: 15; gained: 15; targets added: 15; targets removed: 15; schema changes: 15", "5 more lost coverage changes", "5 more gained coverage changes", "5 more added targets", "5 more removed targets", "5 more schema changes", "1 more baseline schemas", "1 more current schemas", "5 more notes", "--format json"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out.String(), "target-14") || strings.Contains(out.String(), "hidden-schema") || strings.Contains(out.String(), "\x1b") || out.Len() > 40<<10 || !utf8.Valid(out.Bytes()) {
		t.Fatalf("terminal comparison is unsafe or unbounded: %d bytes", out.Len())
	}
	after, _ := json.Marshal(diff)
	if !bytes.Equal(before, after) || !bytes.Contains(after, []byte("target-14")) {
		t.Fatal("terminal presentation changed the complete JSON diff")
	}
}

func TestTerminalPolicyBoundsFindingsWithoutChangingVerdict(t *testing.T) {
	result := &coverage.PolicyResult{Passed: false}
	for i := 0; i < 15; i++ {
		result.Findings = append(result.Findings, coverage.PolicyFinding{Code: fmt.Sprintf("code-%02d", i), Message: strings.Repeat("x", 1000) + "\n\x1b[31m"})
	}
	var out bytes.Buffer
	if err := writePolicyText(&out, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "5 more policy findings") || strings.Contains(out.String(), "code-14") || strings.Contains(out.String(), "\x1b") || out.Len() > 4<<10 || result.Passed || len(result.Findings) != 15 {
		t.Fatal("policy text changed the result or failed to bound diagnostics")
	}
}

func TestTerminalReportsPropagateWriterFailure(t *testing.T) {
	want := errors.New("write failed")
	for name, write := range map[string]func(io.Writer) error{
		"check":   func(out io.Writer) error { return writeCheckText(out, formattedCheckFixture()) },
		"store":   func(out io.Writer) error { return writeStoreCheckText(out, &assumptions.StoreReport{}) },
		"compare": func(out io.Writer) error { return writeComparisonText(out, &coverage.ReportDiff{}) },
		"policy": func(out io.Writer) error {
			return writePolicyText(out, &coverage.PolicyResult{Findings: []coverage.PolicyFinding{{Code: "failed"}}})
		},
	} {
		if err := write(terminalFailingWriter{want}); !errors.Is(err, want) {
			t.Errorf("%s lost writer error: %v", name, err)
		}
	}
}

type terminalFailingWriter struct{ err error }

func (writer terminalFailingWriter) Write([]byte) (int, error) { return 0, writer.err }
