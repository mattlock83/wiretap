package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattlock83/wiretap/internal/assumptions"
)

func TestTerminalCheckSummarizesWithoutChangingCompleteResult(t *testing.T) {
	report := formattedCheckFixture()
	report.Counts = assumptions.Counts{Assumptions: 20, Verified: 7, Mismatched: 13}
	for _, assurance := range []string{"openapi_3_0_validation_rules", "openapi_3_0_provider_contract", "unavailable", "future-kind"} {
		report.Matches = append(report.Matches, assumptions.Match{ContractCheck: assurance})
	}
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
	for _, want := range []string{"FAIL:", "7 of 20 mock examples verified", "mismatched: 13", "Excluded provider exchanges: 20", "1 HTTP declaration and value checks", "1 HTTP provider-spec checks", "1 example-only matches", "1 matches with unknown assurance", "--format json"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "finding-") || strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "\u009b") || !utf8.Valid(out.Bytes()) || out.Len() > 1<<10 {
		t.Fatalf("terminal check is unsafe or unbounded: %d bytes", out.Len())
	}
	after, _ := json.Marshal(report)
	if !bytes.Equal(before, after) || !bytes.Contains(after, []byte("finding-19")) {
		t.Fatal("terminal presentation changed the complete JSON result")
	}
	report.ExcludedProviderEvidence = nil // Run-set summaries do not carry this count.
	out.Reset()
	if err := writeCheckText(&out, report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Excluded provider exchanges") {
		t.Fatal("unavailable excluded-provider count must not be reported as zero")
	}
}

func TestTerminalReportPropagatesWriterFailure(t *testing.T) {
	want := errors.New("write failed")
	if err := writeCheckText(terminalFailingWriter{want}, formattedCheckFixture()); !errors.Is(err, want) {
		t.Fatalf("lost writer error: %v", err)
	}
}

type terminalFailingWriter struct{ err error }

func (writer terminalFailingWriter) Write([]byte) (int, error) { return 0, writer.err }

func formattedCheckFixture() *assumptions.Report {
	return &assumptions.Report{Scope: "test fixtures", RulesVersion: assumptions.RulesVersion, Compatible: false,
		Consumer: assumptions.Source{ApplicationID: `<orders & "clients">`}, Provider: assumptions.Source{ApplicationID: "inventory"}, Dependency: "stock",
		Counts: assumptions.Counts{Assumptions: 2, Verified: 1, Mismatched: 1},
		Findings: []assumptions.Finding{{Kind: "mismatch", Code: "response_mismatch", Operation: "GET /orders", Message: "<script>alert('x')</script> | [link](https://example.com)\x01",
			Consumer: &assumptions.Evidence{EvidencePath: "tests/attempt.json#/capture/traffic/0"}, Differences: []string{"response.body.name"},
			Excerpts: []assumptions.Difference{{Path: "response.body.name", Expected: `"A&B"`, Actual: `"<B>"`}},
		}}}
}
