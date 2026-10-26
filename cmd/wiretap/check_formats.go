package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/mattlock83/wiretap/internal/assumptions"
)

const maxCheckComparisons = 256
const maxCheckDetails = 64 << 10
const maxCheckOutput = 8 << 20

type checkPresentation struct {
	mode, scope, rules string
	compatible         bool
	counts             assumptions.Counts
	assurance          checkAssurance
	comparisons        []checkComparison
	truncated          bool
}

type checkComparison struct {
	name       string
	compatible bool
	details    string
	assurance  checkAssurance
}

// Count the assurance of each matched example, independently of presentation
// limits. An unfamiliar kind must not silently acquire contract assurance.
type checkAssurance struct {
	httpValidation, httpProvider, exampleOnly, unknown int
}

func (counts *checkAssurance) add(matches []assumptions.Match) {
	for _, match := range matches {
		switch match.ContractCheck {
		case "openapi_3_0_validation_rules":
			counts.httpValidation++
		case "openapi_3_0_provider_contract":
			counts.httpProvider++
		case "unavailable":
			counts.exampleOnly++
		default:
			counts.unknown++
		}
	}
}

func (counts checkAssurance) String() string {
	return fmt.Sprintf("Assurance: %d HTTP declaration and value checks; %d HTTP provider-spec checks; %d example-only matches (schemas unavailable); %d matches with unknown assurance.", counts.httpValidation, counts.httpProvider, counts.exampleOnly, counts.unknown)
}

// writeCheckFormat only presents a completed check. It never filters evidence,
// changes the original verdict, or treats a truncated presentation as passing.
func writeCheckFormat(out io.Writer, result any, format string) error {
	view, err := presentCheck(result)
	if err != nil {
		return err
	}
	var data []byte
	switch format {
	case "markdown", "md":
		var buffer bytes.Buffer
		verdict := "FAIL"
		if view.compatible {
			verdict = "PASS"
		}
		fmt.Fprintf(&buffer, "# %s: check %s\n\nScope: %s. Rules: %s.\n\n", verdict, markdownCheckText(view.mode), markdownCheckText(view.scope), markdownCheckText(view.rules))
		fmt.Fprintf(&buffer, "| Assumptions | Verified | Uncovered | Mismatched | Unverified |\n| ---: | ---: | ---: | ---: | ---: |\n| %d | %d | %d | %d | %d |\n", view.counts.Assumptions, view.counts.Verified, view.counts.Uncovered, view.counts.Mismatched, view.counts.Unverified)
		fmt.Fprintf(&buffer, "\n%s\n", view.assurance)
		for _, comparison := range view.comparisons {
			status := "FAIL"
			if comparison.compatible {
				status = "PASS"
			}
			fmt.Fprintf(&buffer, "\n## %s: %s\n\n", status, markdownCheckText(comparison.name))
			fmt.Fprintf(&buffer, "%s\n\n", comparison.assurance)
			for _, line := range strings.Split(comparison.details, "\n") {
				if line != "" {
					fmt.Fprintf(&buffer, "> %s\n>\n", markdownCheckText(line))
				}
			}
			if buffer.Len() > maxCheckOutput {
				return errors.New("formatted check report exceeds 8 MiB")
			}
		}
		if view.truncated {
			fmt.Fprintln(&buffer, "\nPresentation truncated. The verdict and counts above retain the full check result; use JSON for complete diagnostics.")
		}
		data = buffer.Bytes()
	case "junit", "junit-xml", "xml":
		suite := junitCheckSuite{Name: "Wiretap assumption check", Properties: []junitCheckProperty{{"rules_version", checkText(view.rules, 4096)}, {"scope", checkText(view.scope, 4096)}}}
		visibleFailure := false
		for _, comparison := range view.comparisons {
			item := junitCheckCase{Name: checkText(comparison.name, 4096), Classname: "wiretap.assumptions", SystemOut: comparison.assurance.String()}
			if !comparison.compatible {
				visibleFailure = true
				item.Failure = &junitCheckFailure{Message: "Consumer/provider assumption check failed", Type: "WiretapAssumptionCheck", Details: comparison.details}
				suite.Failures++
			}
			suite.Cases = append(suite.Cases, item)
		}
		// A presentation limit (or later presentation-only filter) must never
		// hide the failed aggregate verdict from a CI JUnit importer.
		if len(suite.Cases) == 0 || !view.compatible && !visibleFailure {
			item := junitCheckCase{Name: "Overall check verdict", Classname: "wiretap.assumptions"}
			if !view.compatible {
				item.Failure = &junitCheckFailure{Message: "Overall check verdict is incompatible", Type: "WiretapAssumptionCheck", Details: "The complete check failed. Use the JSON report for all findings."}
				suite.Failures++
			}
			suite.Cases = append(suite.Cases, item)
		}
		suite.Tests = len(suite.Cases)
		suite.SystemOut = fmt.Sprintf("Original verdict: %t. Assumptions: %d; verified: %d; uncovered: %d; mismatched: %d; unverified: %d. %s Presentation truncated: %t. These are assumption checks, not framework test counts or compatibility proof.", view.compatible, view.counts.Assumptions, view.counts.Verified, view.counts.Uncovered, view.counts.Mismatched, view.counts.Unverified, view.assurance, view.truncated)
		encoded, err := xml.MarshalIndent(suite, "", "  ")
		if err != nil {
			return err
		}
		data = append([]byte(xml.Header), encoded...)
		data = append(data, '\n')
	default:
		return fmt.Errorf("unsupported check presentation format %q", format)
	}
	if len(data) > maxCheckOutput {
		return errors.New("formatted check report exceeds 8 MiB")
	}
	_, err = out.Write(data)
	return err
}

func presentCheck(result any) (*checkPresentation, error) {
	view := &checkPresentation{}
	appendComparison := func(name string, compatible bool, findings []assumptions.Finding, matches []assumptions.Match) {
		view.assurance.add(matches)
		if len(view.comparisons) == maxCheckComparisons {
			view.truncated = true
			return
		}
		details, truncated := checkFindingDetails(findings)
		view.truncated = view.truncated || truncated
		comparison := checkComparison{name: name, compatible: compatible, details: details}
		comparison.assurance.add(matches)
		view.comparisons = append(view.comparisons, comparison)
	}
	switch report := result.(type) {
	case assumptions.Report:
		return presentCheck(&report)
	case assumptions.StoreReport:
		return presentCheck(&report)
	case assumptions.SetReport:
		return presentCheck(&report)
	case *assumptions.Report:
		if report == nil {
			return nil, errors.New("check report is nil")
		}
		view.mode, view.scope, view.rules = report.Mode, report.Scope, report.RulesVersion
		view.compatible, view.counts = report.Compatible, report.Counts
		appendComparison(checkComparisonName(report), report.Compatible, report.Findings, report.Matches)
	case *assumptions.StoreReport:
		if report == nil {
			return nil, errors.New("check report is nil")
		}
		view.mode, view.scope, view.rules = report.Mode, report.Scope, report.RulesVersion
		view.compatible, view.counts = report.Compatible, report.Counts
		for _, comparison := range report.Comparisons {
			if comparison == nil {
				return nil, errors.New("check comparison is nil")
			}
			appendComparison(checkComparisonName(comparison), comparison.Compatible, comparison.Findings, comparison.Matches)
		}
	case *assumptions.SetReport:
		if report == nil {
			return nil, errors.New("check report is nil")
		}
		view.mode, view.scope, view.rules = report.Mode, report.Scope, report.RulesVersion
		view.compatible, view.counts = report.Compatible, report.Counts
		appendComparison("Run-set dependency "+report.Dependency, report.Compatible, report.Findings, report.Matches)
	default:
		return nil, fmt.Errorf("unsupported check report type %T", result)
	}
	return view, nil
}

func checkComparisonName(report *assumptions.Report) string {
	consumer, provider := report.Consumer.ApplicationID, report.Provider.ApplicationID
	if consumer == "" {
		consumer = report.Consumer.RunID
	}
	if provider == "" {
		provider = report.Provider.RunID
	}
	return checkText(consumer, 1024) + " -> " + checkText(provider, 1024) + " / " + checkText(report.Dependency, 1024)
}

func checkFindingDetails(findings []assumptions.Finding) (string, bool) {
	var details strings.Builder
	truncated := false
	add := func(value string) {
		line := checkText(value, 4096) + "\n"
		if details.Len()+len(line) > maxCheckDetails-64 {
			truncated = true
			return
		}
		details.WriteString(line)
	}
	for i, finding := range findings {
		if i == 1000 || truncated {
			truncated = true
			break
		}
		add("[" + checkText(finding.Kind, 256) + "] " + checkText(finding.Code, 256) + ": " + checkText(finding.Operation, 1024) + ": " + checkText(finding.Message, 4096))
		if finding.Consumer != nil {
			add("Consumer evidence: " + checkText(finding.Consumer.EvidencePath, 4096))
		}
		for index, evidence := range finding.Provider {
			if index == 20 {
				truncated = true
				break
			}
			add("Provider candidate evidence: " + checkText(evidence.EvidencePath, 4096))
		}
		for index, path := range finding.Differences {
			if index == 20 {
				truncated = true
				break
			}
			add("Different: " + checkText(path, 4096))
		}
		if len(finding.Excerpts) > 0 {
			add("Excerpts show the first provider candidate only; values are bounded diagnostics.")
		}
		for index, excerpt := range finding.Excerpts {
			if index == 20 {
				truncated = true
				break
			}
			add(checkText(excerpt.Path, 1024) + ": expected " + checkText(excerpt.Expected, 1024) + "; actual " + checkText(excerpt.Actual, 1024))
		}
	}
	if truncated {
		details.WriteString("[Diagnostic presentation truncated; original verdict preserved.]\n")
	}
	return details.String(), truncated
}

func checkText(value string, limit int) string {
	count := 0
	for index := range value {
		if count == limit {
			value = value[:index] + "… [truncated]"
			break
		}
		count++
	}
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r != 0xfffe && r != 0xffff {
			return r
		}
		return '�'
	}, value)
}

func markdownCheckText(value string) string {
	value = html.EscapeString(checkText(value, 4096))
	return strings.NewReplacer("|", "&#124;", "`", "&#96;", "*", "&#42;", "_", "&#95;", "[", "&#91;", "]", "&#93;", "#", "&#35;", "\\", "&#92;", "\n", " ", "\r", " ").Replace(value)
}

type junitCheckSuite struct {
	XMLName    xml.Name             `xml:"testsuite"`
	Name       string               `xml:"name,attr"`
	Tests      int                  `xml:"tests,attr"`
	Failures   int                  `xml:"failures,attr"`
	Properties []junitCheckProperty `xml:"properties>property"`
	Cases      []junitCheckCase     `xml:"testcase"`
	SystemOut  string               `xml:"system-out"`
}

type junitCheckProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCheckCase struct {
	Name      string             `xml:"name,attr"`
	Classname string             `xml:"classname,attr"`
	Failure   *junitCheckFailure `xml:"failure,omitempty"`
	SystemOut string             `xml:"system-out,omitempty"`
}

type junitCheckFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Details string `xml:",chardata"`
}
