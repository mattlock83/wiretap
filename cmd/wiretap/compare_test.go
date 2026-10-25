package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/coverage"
)

func comparisonFixture(t *testing.T, directory, name, state string) string {
	t.Helper()
	report := coverage.Report{ReportVersion: coverage.ReportVersion, Contracts: &coverage.ContractCoverage{
		RulesVersion: coverage.ContractRulesVersion, RunID: name, Complete: true,
		Specs: []*coverage.ContractSpec{{RulesVersion: coverage.ContractRulesVersion, APIID: "orders", Origin: "provided", SchemaID: "orders", Revision: "revision", DenominatorKnown: true,
			Targets: []*coverage.ContractTarget{{Key: "target", Family: "operation", Operation: "GET /orders", State: state}}}},
	}}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCompareCommandWritesLossBeforeReturningCIVerdict(t *testing.T) {
	root := t.TempDir()
	baseline := comparisonFixture(t, root, "baseline", "covered")
	current := comparisonFixture(t, root, "current", "not_covered")
	output := filepath.Join(root, "diff.json")
	err := compareCommand(t.Context(), []string{"--baseline", baseline, "--current", current, "--format", "json", "--output", output})
	if !errors.Is(err, errCheckIncompatible) {
		t.Fatalf("lost coverage did not fail CI: %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var diff coverage.ReportDiff
	if err := json.Unmarshal(data, &diff); err != nil || len(diff.LostCoverage) != 1 || diff.LostCoverage[0].After.Operation != "GET /orders" {
		t.Fatalf("missing structured loss diagnostics: %+v, %v", diff, err)
	}
}

func TestCompareCommandProtectsReportInputsAndCaptureAncestors(t *testing.T) {
	root := t.TempDir()
	baseline := comparisonFixture(t, root, "baseline", "covered")
	current := comparisonFixture(t, root, "current", "covered")
	for _, kind := range []string{"direct", "hardlink", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			output := baseline
			if kind != "direct" {
				output = filepath.Join(t.TempDir(), "alias.json")
				link := os.Link
				if kind == "symlink" {
					link = os.Symlink
				}
				if err := link(baseline, output); err != nil {
					t.Skip(err)
				}
			}
			before, _ := os.ReadFile(baseline)
			if err := compareCommand(t.Context(), []string{"--baseline", baseline, "--current", current, "--output", output}); !errors.Is(err, errUsage) {
				t.Fatalf("accepted input alias: %v", err)
			}
			after, _ := os.ReadFile(baseline)
			if string(after) != string(before) {
				t.Fatal("comparison changed input evidence")
			}
		})
	}
	archive := t.TempDir()
	exports := filepath.Join(archive, "exports")
	if err := os.Mkdir(exports, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(archive, "bundle.json")
	if err := os.WriteFile(manifest, []byte("source archive"), 0600); err != nil {
		t.Fatal(err)
	}
	input := comparisonFixture(t, exports, "coverage", "covered")
	if err := compareCommand(t.Context(), []string{"--baseline", input, "--current", current, "--output", manifest}); !errors.Is(err, errUsage) {
		t.Fatalf("accepted output in source capture archive: %v", err)
	}
	if data, err := os.ReadFile(manifest); err != nil || string(data) != "source archive" {
		t.Fatal("comparison changed its source archive")
	}
}

func TestCompareCommandAllowsAdditiveFieldsButRejectsUnknownRulesAndBudgets(t *testing.T) {
	root := t.TempDir()
	baseline := comparisonFixture(t, root, "baseline", "covered")
	current := comparisonFixture(t, root, "current", "covered")
	data, _ := os.ReadFile(current)
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["future_metadata"] = map[string]any{"note": "additive envelope field"}
	data, _ = json.Marshal(object)
	if err := os.WriteFile(current, data, 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "diff.json")
	args := []string{"--baseline", baseline, "--current", current, "--output", output}
	if err := compareCommand(t.Context(), args); err != nil {
		t.Fatalf("rejected additive report fields: %v", err)
	}
	before, _ := os.ReadFile(output)
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILE_BYTES", "1")
	if err := compareCommand(t.Context(), args); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "byte budget") {
		t.Fatalf("unbounded report loading: %v", err)
	}
	after, _ := os.ReadFile(output)
	if string(before) != string(after) {
		t.Fatal("invalid comparison changed an existing output")
	}
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILE_BYTES", "67108864")
	object["contract_coverage"].(map[string]any)["rules_version"] = "future-rules"
	data, _ = json.Marshal(object)
	if err := os.WriteFile(current, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := compareCommand(t.Context(), args); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "rules_version") {
		t.Fatalf("compared unknown measurement rules: %v", err)
	}
}
