package main

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/mattlock83/wiretap/internal/coverage"
)

// Human-readable reports sample details; their verdicts and totals always use
// the complete result. Check and comparison JSON retain every source value.
const maxTerminalItems = 10
const maxTerminalDetails = 3
const maxTerminalComparisons = 5

func terminalLabel(value string) string {
	value = strings.Join(strings.FieldsFunc(value, unicode.IsControl), " ")
	return checkText(value, 240)
}

func terminalOmitted(out io.Writer, count int, label string) {
	if count > 0 {
		fmt.Fprintf(out, "  ... %d more %s; use --format json for complete details.\n", count, label)
	}
}

func writeComparisonText(out io.Writer, diff *coverage.ReportDiff) error {
	var text strings.Builder
	fmt.Fprintf(&text, "Coverage comparison: %s -> %s\nLost coverage: %d; gained: %d; targets added: %d; targets removed: %d; schema changes: %d\n", terminalLabel(diff.BaselineRunID), terminalLabel(diff.CurrentRunID), len(diff.LostCoverage), len(diff.GainedCoverage), len(diff.AddedTargets), len(diff.RemovedTargets), len(diff.SchemaChanges))
	for _, changes := range []struct {
		label string
		items []coverage.CoverageChange
	}{{"Lost", diff.LostCoverage}, {"Gained", diff.GainedCoverage}} {
		for _, change := range changes.items[:min(len(changes.items), maxTerminalItems)] {
			fmt.Fprintf(&text, "%s: %s %s / %s / %s: %s -> %s [%s]\n", changes.label, terminalLabel(change.After.Origin), terminalLabel(change.After.APIID), terminalLabel(change.After.Operation), terminalLabel(change.After.Family), terminalLabel(change.Before.State), terminalLabel(change.After.State), terminalLabel(change.After.TargetKey))
		}
		terminalOmitted(&text, len(changes.items)-maxTerminalItems, strings.ToLower(changes.label)+" coverage changes")
	}
	for _, inventory := range []struct {
		label string
		items []coverage.TargetSnapshot
	}{{"Added", diff.AddedTargets}, {"Removed", diff.RemovedTargets}} {
		for _, target := range inventory.items[:min(len(inventory.items), maxTerminalItems)] {
			fmt.Fprintf(&text, "%s target: %s %s / %s / %s: %s [%s @ %s, %s]\n", inventory.label, terminalLabel(target.Origin), terminalLabel(target.APIID), terminalLabel(target.Operation), terminalLabel(target.Family), terminalLabel(target.State), terminalLabel(target.TargetKey), terminalLabel(target.Revision), terminalLabel(target.RulesVersion))
		}
		terminalOmitted(&text, len(inventory.items)-maxTerminalItems, strings.ToLower(inventory.label)+" targets")
	}
	for _, change := range diff.SchemaChanges[:min(len(diff.SchemaChanges), maxTerminalItems)] {
		fmt.Fprintf(&text, "Schema changed: %s %s\n", terminalLabel(change.Origin), terminalLabel(change.APIID))
		for _, schemas := range []struct {
			label string
			items []coverage.SchemaSnapshot
		}{{"Baseline", change.Before}, {"Current", change.After}} {
			for _, schema := range schemas.items[:min(len(schemas.items), maxTerminalDetails)] {
				fmt.Fprintf(&text, "  %s: %s @ %s (%s)\n", schemas.label, terminalLabel(schema.SchemaID), terminalLabel(schema.Revision), terminalLabel(schema.RulesVersion))
			}
			terminalOmitted(&text, len(schemas.items)-maxTerminalDetails, strings.ToLower(schemas.label)+" schemas")
		}
	}
	terminalOmitted(&text, len(diff.SchemaChanges)-maxTerminalItems, "schema changes")
	for _, note := range diff.Notes[:min(len(diff.Notes), maxTerminalItems)] {
		fmt.Fprintln(&text, terminalLabel(note))
	}
	terminalOmitted(&text, len(diff.Notes)-maxTerminalItems, "notes")
	_, err := io.WriteString(out, text.String())
	return err
}

func writePolicyText(out io.Writer, result *coverage.PolicyResult) error {
	var text strings.Builder
	for _, finding := range result.Findings[:min(len(result.Findings), maxTerminalItems)] {
		fmt.Fprintf(&text, "Policy %s: %s %s %s %s: %s\n", terminalLabel(finding.Code), terminalLabel(finding.Origin), terminalLabel(finding.APIID), terminalLabel(finding.Operation), terminalLabel(finding.Family), terminalLabel(finding.Message))
	}
	if len(result.Findings) > maxTerminalItems {
		fmt.Fprintf(&text, "... %d more policy findings. Inspect the complete coverage report and policy selectors for the remaining failures.\n", len(result.Findings)-maxTerminalItems)
	}
	_, err := io.WriteString(out, text.String())
	return err
}
