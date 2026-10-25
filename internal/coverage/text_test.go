package coverage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestTextSummaryKeepsUnknownsAndSeparateFamilyDenominators(t *testing.T) {
	report := &Report{SUTName: "Orders\n\x1b[31m", ArtifactCount: 2,
		Capture:   &CaptureSource{RunID: "run-1", State: "complete"},
		TestCases: []TestCaseCoverage{{Attempts: []artifact.TestCase{{ID: "a", Status: "pass"}, {ID: "b", Status: "fail"}}}},
		Provided:  SpecCoverageGroup{Specs: []SpecCoverage{{SchemaID: "orders", DenominatorKnown: true, Exercised: []OperationSamples{{Operation: "POST /orders"}}, NotCovered: []OperationSamples{{Operation: "GET /orders"}}}}},
		Dependent: SpecCoverageGroup{Specs: []SpecCoverage{{AssetID: "inventory", DenominatorKnown: false}}},
		Contracts: &ContractCoverage{Complete: false, Specs: []*ContractSpec{{APIID: "orders", Origin: "provided", TargetKind: "service", DenominatorKnown: true,
			Counts:     map[string]ContractCounts{"presence": {Covered: 2, Unknown: 1}, "enum": {Covered: 1, NotCovered: 2, Unsupported: 1}},
			Targets:    []*ContractTarget{{State: "not_covered", Operation: "POST /orders", Phase: "request", Condition: "CANCELLED", FieldPath: "$.state"}},
			Violations: []ContractViolation{{Reason: "invalid request"}},
		}}},
	}
	var out bytes.Buffer
	if err := WriteReport(&out, report, "text"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 (1 passed, 1 failed, 0 skipped)", "Capture is incomplete", "1/2 operations exercised", "Untested: GET /orders", "inventory: operation denominator unknown", "enum: 1 / 2 / 0 / 1 / 0", "presence: 2 / 0 / 1 / 0 / 0", "CANCELLED ($.state)", "Contract violations observed: 1", "provider agreement is not verified"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary omitted %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Fatal("terminal escape retained")
	}
}

func TestTextSummaryShowsEmptyRunAndWriterFailure(t *testing.T) {
	var out bytes.Buffer
	if err := WriteReport(&out, &Report{}, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No test attempts were recorded") {
		t.Fatal(out.String())
	}
	if !strings.Contains(out.String(), "Captured interactions: 0") || strings.Contains(out.String(), "WARNING:") {
		t.Fatal("empty run confused with tests that captured no traffic: " + out.String())
	}
	if err := WriteReport(failedSummaryWriter{}, &Report{}, "text"); err == nil {
		t.Fatal("writer failure lost")
	}
}

func TestTextSummaryWarnsWhenRecordedTestsCaptureNoTraffic(t *testing.T) {
	for name, statuses := range map[string][]string{"passing": {"pass", "pass"}, "failed": {"fail"}, "mixed": {"pass", "fail", "skip"}, "skipped": {"skip"}} {
		t.Run(name, func(t *testing.T) {
			var entries []artifactEntry
			for i, status := range statuses {
				entries = append(entries, artifactEntry{Doc: &artifact.Artifact{TestCase: artifact.TestCase{ID: fmt.Sprint(i), Name: "same parameterized test", Status: status}}})
			}
			report, err := generateFromEntries(entries, nil, "example", false, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := WriteReport(&out, report, "text"); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Captured interactions: 0 (0 HTTP, 0 gRPC, 0 other)", "WARNING:", "no network traffic was captured", "Wiretap's SUT URL", "proxy URLs", "test trace context"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q: %s", want, out.String())
				}
			}
			if strings.Contains(out.String(), "Tests passed, but") != (name == "passing") {
				t.Fatal("warning misrepresented test outcomes: " + out.String())
			}
		})
	}
}

func TestTextSummaryCountsCapturedTrafficWithoutDependingOnSchemas(t *testing.T) {
	report, err := generateFromEntries([]artifactEntry{
		{Doc: &artifact.Artifact{TestCase: artifact.TestCase{ID: "first", Name: "same name", Status: "pass"}, Traffic: []artifact.Flow{{ID: "http", Protocol: "http"}, {ID: "grpc", Protocol: "grpc"}}}},
		{Doc: &artifact.Artifact{TestCase: artifact.TestCase{ID: "second", Name: "same name", Status: "fail"}, Traffic: []artifact.Flow{{ID: "other", Protocol: "websocket"}}}},
	}, nil, "example", false, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Schema samples repeat traffic and must not contribute to capture totals.
	report.Provided.Specs = []SpecCoverage{{Exercised: []OperationSamples{{Samples: report.TestCases[0].Traffic}}}}
	report.Dependent = report.Provided
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var restored Report
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	for _, input := range []*Report{report, &restored} {
		var out bytes.Buffer
		if err := WriteReport(&out, input, "text"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "Captured interactions: 3 (1 HTTP, 1 gRPC, 1 other)") || strings.Contains(out.String(), "no network traffic was captured") {
			t.Fatal("traffic omitted, duplicated, or confused with coverage: " + out.String())
		}
	}
}

func TestTextSummaryDoesNotInferZeroTrafficWhenAttemptDetailsAreUnavailable(t *testing.T) {
	for _, report := range []*Report{
		{ArtifactCount: 2},
		{ArtifactCount: 2, TestCases: []TestCaseCoverage{{Attempts: []artifact.TestCase{{ID: "one", Status: "pass"}}}}},
		{ArtifactCount: 1, TestCases: []TestCaseCoverage{{Attempts: []artifact.TestCase{{Status: "pass"}}}}},
	} {
		var out bytes.Buffer
		if err := WriteReport(&out, report, "text"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "Captured interactions: unavailable") || strings.Contains(out.String(), "no network traffic was captured") {
			t.Fatal("missing details misrepresented as zero traffic: " + out.String())
		}
	}
}

type failedSummaryWriter struct{}

func (failedSummaryWriter) Write([]byte) (int, error) { return 0, errors.New("cannot write") }
