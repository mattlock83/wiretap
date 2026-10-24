package coverage

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Text is a bounded reading aid over the same report, not another measurement
// profile. Keep condition families separate: their denominators differ.
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
		for _, spec := range group.specs {
			label := spec.SchemaID
			if spec.AssetID != "" {
				label = spec.AssetID
			}
			if !spec.DenominatorKnown {
				fmt.Fprintf(&out, "  %s: operation denominator unknown\n", textLabel(label))
				continue
			}
			fmt.Fprintf(&out, "  %s: %d/%d operations exercised\n", textLabel(label), len(spec.Exercised), len(spec.Exercised)+len(spec.NotCovered))
			for i, missing := range spec.NotCovered {
				if i == 5 {
					fmt.Fprintf(&out, "    ... %d more untested operations in the JSON report\n", len(spec.NotCovered)-i)
					break
				}
				fmt.Fprintf(&out, "    Untested: %s\n", textLabel(missing.Operation))
			}
		}
	}
	if report.Contracts != nil {
		out.WriteString("\nContract conditions (covered / gaps / unknown / unsupported / excluded)\n")
		for _, spec := range report.Contracts.Specs {
			fmt.Fprintf(&out, "  %s (%s, %s)\n", textLabel(spec.APIID), textLabel(spec.Origin), textLabel(spec.TargetKind))
			if !spec.DenominatorKnown {
				out.WriteString("    Contract denominator unknown.\n")
			}
			families := make([]string, 0, len(spec.Counts))
			for family := range spec.Counts {
				if strings.HasPrefix(family, "phase:") {
					continue // These overlap the same targets already counted by family.
				}
				families = append(families, family)
			}
			sort.Strings(families)
			for _, family := range families {
				count := spec.Counts[family]
				label := strings.ReplaceAll(strings.TrimPrefix(family, "family:"), "_", " ")
				fmt.Fprintf(&out, "    %s: %d / %d / %d / %d / %d\n", textLabel(label), count.Covered, count.NotCovered, count.Unknown, count.Unsupported, count.Excluded)
			}
			shown := 0
			for _, target := range spec.Targets {
				if target.State != "not_covered" && target.State != "unknown" {
					continue
				}
				if shown == 3 {
					out.WriteString("    More condition details in the JSON report.\n")
					break
				}
				location := target.FieldPath
				if location == "" {
					// Prefer the property name over an implementation-shaped schema
					// pointer. Full pointers and target identities remain in JSON.
					if _, property, found := strings.Cut(target.Location, "/properties/"); found {
						location = strings.Split(property, "/")[0]
						location = strings.ReplaceAll(strings.ReplaceAll(location, "~1", "/"), "~0", "~")
					} else {
						location = strings.ReplaceAll(target.Family, "_", " ")
					}
				}
				state := "Gap"
				if target.State == "unknown" {
					state = "Unknown"
				}
				fmt.Fprintf(&out, "    %s: %s %s %s (%s)\n", state, textLabel(target.Operation), textLabel(target.Phase), textLabel(target.Condition), textLabel(location))
				shown++
			}
			if len(spec.Violations) > 0 {
				fmt.Fprintf(&out, "    Contract violations observed: %d; framework test outcomes are reported separately.\n", len(spec.Violations))
			}
		}
	}
	if len(report.Unmeasured) > 0 {
		fmt.Fprintf(&out, "\nUnmeasured interactions: %d\n", len(report.Unmeasured))
	}
	for i, note := range report.Notes {
		if i == 5 {
			out.WriteString("More notes in the JSON report.\n")
			break
		}
		fmt.Fprintf(&out, "Note: %s\n", textLabel(note))
	}
	out.WriteString("\nExercised operations show observed traffic. Contract coverage requires qualifying evidence.\nMock replies are test assumptions; provider agreement is not verified.\n")
	_, err := io.WriteString(w, out.String())
	return err
}

// Captured labels can contain newlines or terminal control bytes. A summary
// displays them as one bounded line; the machine report retains the source.
func textLabel(value string) string {
	value = strings.Join(strings.FieldsFunc(value, func(r rune) bool { return r < 32 || r == 127 }), " ")
	runes := []rune(value)
	if len(runes) > 180 {
		return string(runes[:177]) + "..."
	}
	return value
}
