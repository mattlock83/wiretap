package coverage

import (
	"bytes"
	"errors"
	"strings"
	"testing"

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
	if err := WriteReport(failedSummaryWriter{}, &Report{}, "text"); err == nil {
		t.Fatal("writer failure lost")
	}
}

type failedSummaryWriter struct{}

func (failedSummaryWriter) Write([]byte) (int, error) { return 0, errors.New("cannot write") }
