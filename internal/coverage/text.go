package coverage

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

const maxTextSpecs = 10

// Text summarizes outcomes and identified evidence. Detailed operations,
// condition families and findings remain in the JSON and HTML reports.
func writeTextReport(w io.Writer, report *Report) error {
	var out strings.Builder
	fmt.Fprintf(&out, "Wiretap coverage: %s\n", textLabel(report.SUTName))
	if report.Capture != nil {
		fmt.Fprintf(&out, "Run: %s (%s)\n", textLabel(report.Capture.RunID), textLabel(report.Capture.State))
	}
	outcomes := map[string]int{}
	seen := map[string]bool{}
	for _, test := range report.TestCases {
		for _, attempt := range test.Attempts {
			if seen[attempt.ID] {
				continue
			}
			seen[attempt.ID] = true
			outcomes[attempt.Status]++
		}
	}
	fmt.Fprintf(&out, "Attempts: %d (%d passed, %d failed, %d skipped)\n", report.ArtifactCount, outcomes["pass"], outcomes["fail"], outcomes["skip"])
	if report.ArtifactCount == 0 {
		out.WriteString("No test attempts were recorded; declared operations below remain untested.\n")
	}
	if traffic, known := capturedTraffic(report); known {
		fmt.Fprintf(&out, "Captured interactions: %d (%d HTTP, %d other)\n", traffic.http+traffic.other, traffic.http, traffic.other)
		if report.ArtifactCount > 0 && traffic.http+traffic.other == 0 {
			if outcomes["pass"] == report.ArtifactCount {
				out.WriteString("WARNING: Tests passed, but no network traffic was captured.\n")
			} else {
				out.WriteString("WARNING: Test attempts were recorded, but no network traffic was captured.\n")
			}
			out.WriteString("Check that clients use Wiretap's SUT URL, dependency calls use its proxy URLs, and requests carry the test trace context.\n")
		}
	} else {
		out.WriteString("Captured interactions: unavailable (attempt details are incomplete).\n")
	}
	if report.Contracts != nil && !report.Contracts.Complete {
		out.WriteString("Capture is incomplete; the available evidence cannot establish final run coverage.\n")
	}
	for _, group := range []struct {
		name  string
		specs []SpecCoverage
	}{{"Provided APIs", report.Provided.Specs}, {"Dependency APIs", report.Dependent.Specs}} {
		fmt.Fprintf(&out, "\n%s\n", group.name)
		if len(group.specs) == 0 {
			out.WriteString("  No registered contracts.\n")
		}
		for i, spec := range group.specs {
			if i == maxTextSpecs {
				fmt.Fprintf(&out, "  ... %d more contracts; use --format json for complete details.\n", len(group.specs)-i)
				break
			}
			label := spec.SchemaID
			if spec.AssetID != "" {
				label = spec.AssetID
			}
			if !spec.DenominatorKnown {
				fmt.Fprintf(&out, "  %s: operation denominator unknown\n", textLabel(label))
				continue
			}
			fmt.Fprintf(&out, "  %s: %d/%d operations exercised; %d untested\n", textLabel(label), len(spec.Exercised), len(spec.Exercised)+len(spec.NotCovered), len(spec.NotCovered))
		}
	}
	if report.Contracts != nil {
		var total ContractCounts
		unknownDenominators, violations := 0, 0
		for _, spec := range report.Contracts.Specs {
			if !spec.DenominatorKnown {
				unknownDenominators++
			}
			violations += len(spec.Violations)
			for family, count := range spec.Counts {
				if strings.HasPrefix(family, "phase:") {
					continue // These overlap the same targets already counted by family.
				}
				total.Covered += count.Covered
				total.NotCovered += count.NotCovered
				total.Unknown += count.Unknown
				total.Unsupported += count.Unsupported
				total.Excluded += count.Excluded
			}
		}
		fmt.Fprintf(&out, "\nIdentified contract conditions: %d covered, %d gaps, %d unknown, %d unsupported, %d excluded\n", total.Covered, total.NotCovered, total.Unknown, total.Unsupported, total.Excluded)
		if unknownDenominators > 0 {
			fmt.Fprintf(&out, "Contracts with unknown denominator: %d; totals describe identified conditions only.\n", unknownDenominators)
		}
		fmt.Fprintf(&out, "Contract violations observed: %d; framework outcomes are reported separately.\n", violations)
	}
	if len(report.Notes) > 0 {
		fmt.Fprintf(&out, "Notes: %d; see the complete report.\n", len(report.Notes))
	}
	out.WriteString("\nExercised operations show observed traffic. Contract coverage requires qualifying evidence.\nMock replies are test assumptions; provider agreement is not verified.\nUse --format json or --format html for complete details.\n")
	_, err := io.WriteString(w, out.String())
	return err
}

// Traffic is recorded once per attempt here, including exchanges that have no
// matching schema. Operation/condition samples can repeat or omit exchanges,
// so those samples cannot establish a captured-interaction count.
func capturedTraffic(report *Report) (counts struct{ http, other int }, known bool) {
	attempts := map[string]bool{}
	for _, test := range report.TestCases {
		for _, attempt := range test.Attempts {
			if attempt.ID == "" {
				return counts, false
			}
			attempts[attempt.ID] = true
		}
		for _, flow := range test.Traffic {
			switch flow.Protocol {
			case "http":
				counts.http++
			default:
				counts.other++
			}
		}
	}
	return counts, len(attempts) == report.ArtifactCount
}

// Captured labels can contain newlines or terminal control bytes. A summary
// displays them as one bounded line; the machine report retains the source.
func textLabel(value string) string {
	value = strings.Join(strings.FieldsFunc(value, unicode.IsControl), " ")
	runes := []rune(value)
	if len(runes) > 180 {
		return string(runes[:177]) + "..."
	}
	return value
}
