package bundlereport

import (
	"fmt"
	"sort"
	"strings"

	"github.com/phrond/evermock/internal/coverage"
)

func contractCoverageMarkdown(report *coverage.ContractCoverage) string {
	var out strings.Builder
	out.WriteString("# Contract condition coverage\n\n[Machine-readable catalogue](contract-coverage.json) · [Operation coverage](coverage.md)\n\n")
	fmt.Fprintf(&out, "Rules: `%s`. Run complete: **%t**. Scope: %s.\n\n", report.RulesVersion, report.Complete, escape(report.Scope))
	out.WriteString("Covered means a valid message in a passing, complete attempt. Observed conditions from failed attempts remain evidence, but earn no passing credit. Unsupported rules and unresolved feasibility make the denominator partial. No percentage of all value combinations or compatibility verdict is implied.\n\n")
	if !report.Complete {
		out.WriteString("**This snapshot cannot support a final run assurance claim.**\n\n")
	}
	for _, spec := range report.Specs {
		fmt.Fprintf(&out, "## %s — %s (%s)\n\nSchema: %s. Contract revision: `%s`. Condition denominator known: **%t**.\n\n", escape(spec.APIID), escape(spec.Origin), escape(spec.TargetKind), escape(spec.SchemaID), spec.Revision, spec.DenominatorKnown)
		for _, note := range spec.Notes {
			out.WriteString(escape(note) + "\n\n")
		}
		out.WriteString("| Scope | Covered | Not covered | Unknown | Unsupported | Excluded |\n| --- | ---: | ---: | ---: | ---: | ---: |\n")
		groups := make([]string, 0, len(spec.Counts))
		for k := range spec.Counts {
			groups = append(groups, k)
		}
		sort.Strings(groups)
		for _, group := range groups {
			c := spec.Counts[group]
			fmt.Fprintf(&out, "| %s | %d | %d | %d | %d | %d |\n", escape(group), c.Covered, c.NotCovered, c.Unknown, c.Unsupported, c.Excluded)
		}
		out.WriteString("\n")
		for _, target := range spec.Targets {
			fmt.Fprintf(&out, "### %s · %s · %s\n\nKey: `%s`\n\nOperation key: `%s`\n\n%s — **%s**. Schema location: %s. Feasibility: %s.\n\n", escape(target.Operation), escape(target.Phase), escape(target.Condition), target.Key, target.OperationKey, escape(target.Family), target.State, escape(target.Location), target.Feasibility)
			if target.Reason != "" {
				out.WriteString(escape(target.Reason) + "\n\n")
			}
			for _, e := range target.Evidence {
				fmt.Fprintf(&out, "- [%s](%s): %s; attempt %s, flow %s, %s %s; test outcome %s, message validation %s, capture incomplete %t. %s %s\n", escape(e.TestName), e.Path, e.Result, escape(e.AttemptID), escape(e.FlowID), e.Phase, escape(e.InstanceLocation), escape(e.Outcome), escape(e.Validation), e.CaptureIncomplete, escape(e.TransportError), escape(e.Reason))
			}
			out.WriteString("\n")
		}
		if len(spec.Violations) > 0 {
			out.WriteString("### Violations\n\n")
		}
		for _, v := range spec.Violations {
			fmt.Fprintf(&out, "- [%s](%s), flow %s, %s: %s\n", escape(v.Evidence.TestName), v.Evidence.Path, escape(v.Evidence.FlowID), v.Evidence.Phase, escape(v.Reason))
		}
		fmt.Fprintf(&out, "\n%d request/response condition combinations retained in the JSON catalogue, with exact array-item locations. Independent observations are never assembled into a synthetic exchange.\n\n", len(spec.Combinations))
	}
	return out.String()
}
