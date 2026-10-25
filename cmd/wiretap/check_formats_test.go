package main

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/assumptions"
)

func formattedCheckFixture() *assumptions.Report {
	return &assumptions.Report{Mode: "consumer", Scope: "test fixtures", RulesVersion: assumptions.RulesVersion, Compatible: false,
		Consumer: assumptions.Source{ApplicationID: `<orders & "clients">`}, Provider: assumptions.Source{ApplicationID: "inventory"}, Dependency: "stock",
		Counts: assumptions.Counts{Assumptions: 2, Verified: 1, Mismatched: 1},
		Findings: []assumptions.Finding{{Kind: "mismatch", Code: "response_mismatch", Operation: "GET /orders", Message: "<script>alert('x')</script> | [link](https://example.com)\x01",
			Consumer: &assumptions.Evidence{EvidencePath: "tests/attempt.json#/capture/traffic/0"}, Differences: []string{"response.body.name"},
			Excerpts: []assumptions.Difference{{Path: "response.body.name", Expected: `"A&B"`, Actual: `"<B>"`}},
		}}}
}

func TestCheckFormatsEscapeDiagnosticsAndPreserveOriginalVerdict(t *testing.T) {
	report := formattedCheckFixture()
	var markdown bytes.Buffer
	if err := writeCheckFormat(&markdown, report, "markdown"); err != nil {
		t.Fatal(err)
	}
	text := markdown.String()
	if !strings.HasPrefix(text, "# FAIL:") || strings.Contains(text, "<script>") || strings.Contains(text, "[link]") || strings.Contains(text, "\x01") || !strings.Contains(text, "&#124;") || !strings.Contains(text, "first provider candidate only") {
		t.Fatalf("unsafe or misleading Markdown: %s", text)
	}
	var output bytes.Buffer
	if err := writeCheckFormat(&output, report, "junit"); err != nil {
		t.Fatal(err)
	}
	var suite junitCheckSuite
	if err := xml.Unmarshal(output.Bytes(), &suite); err != nil {
		t.Fatalf("invalid JUnit XML: %v\n%s", err, output.String())
	}
	if suite.Tests != 1 || suite.Failures != 1 || suite.Cases[0].Failure == nil || !strings.Contains(suite.Cases[0].Failure.Details, `<script>`) || !strings.Contains(suite.Cases[0].Failure.Details, `"A&B"`) {
		t.Fatalf("XML changed the original diagnostics or verdict: %+v", suite)
	}
}

func TestCheckFormatsSupportStoreAndRunSetReportsWithoutDroppingFailure(t *testing.T) {
	passing := &assumptions.Report{Compatible: true}
	store := &assumptions.StoreReport{Compatible: false, Mode: "provider", Comparisons: make([]*assumptions.Report, maxCheckComparisons+1)}
	for index := range store.Comparisons {
		store.Comparisons[index] = passing
	}
	store.Comparisons[maxCheckComparisons] = formattedCheckFixture()
	for _, report := range []any{store, &assumptions.SetReport{Compatible: false, Mode: "consumer", Findings: formattedCheckFixture().Findings}} {
		var output bytes.Buffer
		if err := writeCheckFormat(&output, report, "junit"); err != nil {
			t.Fatal(err)
		}
		var suite junitCheckSuite
		if err := xml.Unmarshal(output.Bytes(), &suite); err != nil || suite.Failures == 0 {
			t.Fatalf("presentation hid failed original verdict: %+v %v", suite, err)
		}
	}
}

func TestCheckFormatsDescribeAssuranceForEveryReportScope(t *testing.T) {
	matches := []assumptions.Match{
		{ContractCheck: "openapi_3_0_validation_rules"},
		{ContractCheck: "openapi_3_0_validation_rules"},
		{ContractCheck: "openapi_3_0_provider_contract"},
		{ContractCheck: "protobuf_rules_and_observed_wire"},
		{ContractCheck: "unavailable"},
		{ContractCheck: "unavailable"},
		{ContractCheck: "unrecognized_contract_check"},
		{},
	}
	counts := assumptions.Counts{Assumptions: len(matches), Verified: len(matches)}
	pair := &assumptions.Report{Compatible: true, Counts: counts, Matches: matches}
	store := &assumptions.StoreReport{Compatible: true, Counts: counts, Comparisons: []*assumptions.Report{
		{Compatible: true, Matches: matches[:3]},
		{Compatible: true, Matches: matches[3:]},
	}}
	set := &assumptions.SetReport{Compatible: true, Counts: counts, Matches: matches}
	want := "Assurance: 2 HTTP declaration and value checks; 1 HTTP provider-spec checks; 1 protobuf declaration and wire checks; 2 example-only matches (schemas unavailable); 2 matches with unknown assurance."
	for name, report := range map[string]any{"pair": pair, "store": store, "run set": set, "pair value": *pair, "store value": *store, "run set value": *set} {
		t.Run(name, func(t *testing.T) {
			var markdown, junit bytes.Buffer
			if err := writeCheckFormat(&markdown, report, "markdown"); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(markdown.String(), "# PASS:") || !strings.Contains(markdown.String(), want) {
				t.Fatalf("Markdown omitted aggregate assurance: %s", markdown.String())
			}
			if err := writeCheckFormat(&junit, report, "junit"); err != nil {
				t.Fatal(err)
			}
			var suite junitCheckSuite
			if err := xml.Unmarshal(junit.Bytes(), &suite); err != nil {
				t.Fatal(err)
			}
			if suite.Failures != 0 || !strings.Contains(suite.SystemOut, want) {
				t.Fatalf("JUnit omitted aggregate assurance or changed verdict: %+v", suite)
			}
			for _, comparison := range suite.Cases {
				if !strings.HasPrefix(comparison.SystemOut, "Assurance:") {
					t.Fatalf("passing comparison omitted assurance: %+v", comparison)
				}
			}
		})
	}
	for name, write := range map[string]func(*bytes.Buffer) error{
		"pair text":  func(out *bytes.Buffer) error { return writeCheckText(out, pair) },
		"store text": func(out *bytes.Buffer) error { return writeStoreCheckText(out, store) },
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := write(&out); err != nil || !strings.Contains(out.String(), want) {
				t.Fatalf("text omitted aggregate assurance: %v\n%s", err, out.String())
			}
		})
	}
}

func TestCheckFormatsRetainAssuranceAfterComparisonTruncation(t *testing.T) {
	store := &assumptions.StoreReport{Compatible: true, Counts: assumptions.Counts{Assumptions: maxCheckComparisons + 1, Verified: maxCheckComparisons + 1}}
	for i := 0; i < maxCheckComparisons; i++ {
		store.Comparisons = append(store.Comparisons, &assumptions.Report{Compatible: true, Matches: []assumptions.Match{{ContractCheck: "unavailable"}}})
	}
	store.Comparisons = append(store.Comparisons, &assumptions.Report{Compatible: true, Matches: []assumptions.Match{{ContractCheck: "protobuf_rules_and_observed_wire"}}})
	for _, format := range []string{"markdown", "junit"} {
		var out bytes.Buffer
		if err := writeCheckFormat(&out, store, format); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"1 protobuf declaration and wire checks", "256 example-only matches (schemas unavailable)", "truncated"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s lost full-result assurance %q", format, want)
			}
		}
	}
}

func TestCheckFormatsShowZeroAssuranceWithoutInventingMatches(t *testing.T) {
	for _, report := range []any{&assumptions.Report{}, &assumptions.StoreReport{}, &assumptions.SetReport{}} {
		for _, format := range []string{"markdown", "junit"} {
			var out bytes.Buffer
			if err := writeCheckFormat(&out, report, format); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"0 HTTP declaration and value checks", "0 HTTP provider-spec checks", "0 protobuf declaration and wire checks", "0 example-only matches", "0 matches with unknown assurance"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("%T %s omitted %q", report, format, want)
				}
			}
		}
	}
}

func TestCheckFormatsBoundLongUnicodeDiagnostics(t *testing.T) {
	report := formattedCheckFixture()
	report.Findings[0].Message = strings.Repeat("☃<&>\n", 20000)
	for _, format := range []string{"markdown", "junit"} {
		var output bytes.Buffer
		if err := writeCheckFormat(&output, report, format); err != nil {
			t.Fatal(err)
		}
		if output.Len() > maxCheckDetails || !strings.Contains(output.String(), "truncated") {
			t.Fatalf("%s did not bound diagnostics: %d bytes", format, output.Len())
		}
	}
	if err := writeCheckFormat(&bytes.Buffer{}, (*assumptions.Report)(nil), "junit"); err == nil {
		t.Fatal("accepted nil report")
	}
}
