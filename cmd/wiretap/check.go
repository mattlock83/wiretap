package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattlock83/wiretap/internal/assumptions"
)

type checkValues []string

func (values *checkValues) String() string { return strings.Join(*values, ", ") }
func (values *checkValues) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("value must not be empty")
	}
	*values = append(*values, value)
	return nil
}

func checkCommand(ctx context.Context, args []string) (int, error) {
	flags := flag.NewFlagSet("wiretap check", flag.ContinueOnError)
	options := assumptions.Options{}
	var ignored checkValues
	var format, output, consumerSet, providerSet string
	flags.StringVar(&consumerSet, "consumer-set", "", "consumer run set manifest (use with --provider-set)")
	flags.StringVar(&providerSet, "provider-set", "", "provider run set manifest (use with --consumer-set)")
	flags.StringVar(&options.Consumer, "consumer", "", "consumer run directory or bundle.json")
	flags.StringVar(&options.Provider, "provider", "", "provider run directory or bundle.json")
	flags.StringVar(&options.Dependency, "dependency", "", "consumer's local mock ID (inferred when unambiguous)")
	flags.StringVar(&options.ConsumerArazzo, "consumer-arazzo", "", "consumer Arazzo file or directory for explicit pairs (default: <consumer>/exports/arazzo/arazzo.json)")
	flags.StringVar(&options.ProviderArazzo, "provider-arazzo", "", "provider Arazzo file or directory for explicit pairs (default: <provider>/exports/arazzo/arazzo.json)")
	flags.Var(&ignored, "ignore-header", "ignore another captured HTTP header; declared HTTP header rules still apply; repeatable")
	flags.StringVar(&format, "format", "text", "report format: text or json")
	flags.StringVar(&output, "output", "", "write report outside input archives, export directories and run set manifests (default: stdout)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: wiretap check --consumer <capture> --provider <capture> [options]\n       wiretap check --consumer-set <manifest> --provider-set <manifest> [options]\n\nCheck one consumer mock boundary against provider evidence using JSON/REST Arazzo workflows.\nRun sets combine evidence from suites in the same build. No traffic is replayed.")
		flags.PrintDefaults()
	}
	if err := parseCommandFlags(flags, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if len(flags.Args()) != 0 {
		return 2, fmt.Errorf("%w: unexpected arguments: %v", errUsage, flags.Args())
	}
	var arazzoOverrides bool
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "consumer-arazzo" || value.Name == "provider-arazzo" {
			arazzoOverrides = true
		}
	})
	sets := consumerSet != "" || providerSet != ""
	if sets && (consumerSet == "" || providerSet == "" || options.Consumer != "" || options.Provider != "" || arazzoOverrides) {
		return 2, fmt.Errorf("%w: use both --consumer-set and --provider-set without individual archives or input overrides", errUsage)
	}
	if !sets && (options.Consumer == "" || options.Provider == "") {
		return 2, fmt.Errorf("%w: supply --consumer and --provider, or --consumer-set and --provider-set", errUsage)
	}
	if format != "text" && format != "json" {
		return 2, fmt.Errorf("%w: --format must be text or json", errUsage)
	}
	options.IgnoreHeaders = ignored
	var data bytes.Buffer
	var result any
	var compatible bool
	var sources []assumptions.Source
	var manifests []string
	var err error
	if sets {
		var report *assumptions.SetReport
		report, err = assumptions.CheckRunSets(ctx, consumerSet, providerSet, options.Dependency, ignored)
		if err == nil {
			result, compatible = report, report.Compatible
			sources = append(append(sources, report.Consumers...), report.Providers...)
			manifests = []string{consumerSet, providerSet}
			if format == "text" {
				err = writeCheckText(&data, &assumptions.Report{Compatible: report.Compatible, RulesVersion: report.RulesVersion, Dependency: report.Dependency, Binding: "same_build_run_sets", Consumer: assumptions.Source{BundlePath: consumerSet, RunID: "run set"}, Provider: assumptions.Source{BundlePath: providerSet, RunID: "run set"}, Counts: report.Counts, Matches: report.Matches, Findings: report.Findings})
			}
		}
	} else {
		var report *assumptions.Report
		report, err = assumptions.Check(ctx, options)
		if err == nil {
			result, compatible = report, report.Compatible
			sources = []assumptions.Source{report.Consumer, report.Provider}
			if format == "text" {
				err = writeCheckText(&data, report)
			}
		}
	}
	if err == nil && format == "json" {
		encoder := json.NewEncoder(&data)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(result)
	}
	if err != nil {
		if ctx.Err() != nil {
			return 1, ctx.Err()
		}
		if errors.Is(err, assumptions.ErrInput) {
			return 2, err
		}
		return 1, err
	}
	if output == "" {
		_, err = os.Stdout.Write(data.Bytes())
	} else {
		err = writeCheckOutput(output, sources, manifests, data.Bytes())
	}
	if err != nil {
		if errors.Is(err, errUsage) {
			return 2, err
		}
		return 1, err
	}
	if !compatible {
		return 1, nil
	}
	return 0, nil
}

func writeCheckText(out io.Writer, report *assumptions.Report) error {
	status := "FAIL"
	if report.Compatible {
		status = "PASS"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s: check — %d of %d mock examples verified\n", status, report.Counts.Verified, report.Counts.Assumptions)
	fmt.Fprintf(&text, "Consumer: %s (%s)\nProvider: %s (%s)\nMock dependency: %s; binding: %s\n", terminalLabel(report.Consumer.BundlePath), terminalLabel(report.Consumer.RunID), terminalLabel(report.Provider.BundlePath), terminalLabel(report.Provider.RunID), terminalLabel(report.Dependency), terminalLabel(report.Binding))
	fmt.Fprintf(&text, "Uncovered: %d; mismatched: %d; unverified: %d\n", report.Counts.Uncovered, report.Counts.Mismatched, report.Counts.Unverified)
	var assurance checkAssurance
	assurance.add(report.Matches)
	fmt.Fprintln(&text, assurance)
	for _, finding := range report.Findings[:min(len(report.Findings), maxTerminalItems)] {
		fmt.Fprintf(&text, "\n[%s] %s: %s", terminalLabel(finding.Kind), terminalLabel(finding.Code), terminalLabel(finding.Operation))
		if finding.Operation != "" {
			text.WriteString(": ")
		}
		fmt.Fprintf(&text, "%s\n", terminalLabel(finding.Message))
		if finding.Consumer != nil {
			fmt.Fprintf(&text, "  Consumer test %q, flow %s: %s\n", terminalLabel(finding.Consumer.TestName), terminalLabel(finding.Consumer.FlowID), terminalLabel(finding.Consumer.EvidencePath))
		}
		for _, ref := range finding.Provider[:min(len(finding.Provider), maxTerminalDetails)] {
			fmt.Fprintf(&text, "  Provider test %q, flow %s: %s\n", terminalLabel(ref.TestName), terminalLabel(ref.FlowID), terminalLabel(ref.EvidencePath))
		}
		terminalOmitted(&text, len(finding.Provider)-maxTerminalDetails, "provider candidates")
		for _, path := range finding.Differences[:min(len(finding.Differences), maxTerminalDetails)] {
			fmt.Fprintf(&text, "  Different: %s\n", terminalLabel(path))
		}
		terminalOmitted(&text, len(finding.Differences)-maxTerminalDetails, "differences")
		for _, excerpt := range finding.Excerpts[:min(len(finding.Excerpts), maxTerminalDetails)] {
			fmt.Fprintf(&text, "  %s: expected %s; actual %s (first provider candidate)\n", terminalLabel(excerpt.Path), terminalLabel(excerpt.Expected), terminalLabel(excerpt.Actual))
		}
		terminalOmitted(&text, len(finding.Excerpts)-maxTerminalDetails, "value excerpts")
	}
	terminalOmitted(&text, len(report.Findings)-maxTerminalItems, "findings")
	if len(report.ExcludedProviderEvidence) > 0 {
		fmt.Fprintf(&text, "\nExcluded %d provider exchanges that cannot establish coverage.\n", len(report.ExcludedProviderEvidence))
		for _, excluded := range report.ExcludedProviderEvidence[:min(len(report.ExcludedProviderEvidence), maxTerminalDetails)] {
			fmt.Fprintf(&text, "  Test %q, flow %s: %s\n", terminalLabel(excluded.Evidence.TestName), terminalLabel(excluded.Evidence.FlowID), terminalLabel(excluded.Reason))
		}
		terminalOmitted(&text, len(report.ExcludedProviderEvidence)-maxTerminalDetails, "excluded provider exchanges")
	}
	fmt.Fprintf(&text, "\nRules: %s; exact retained examples and exercised contract rules after endpoint/header normalization.\n", terminalLabel(report.RulesVersion))
	text.WriteString("Use --format json for complete diagnostics.\n")
	_, err := io.WriteString(out, text.String())
	return err
}

func writeCheckOutput(path string, sources []assumptions.Source, manifests []string, data []byte) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	for _, input := range manifests {
		if err := protectCheckInput(absolute, input, "run set"); err != nil {
			return err
		}
	}
	for _, source := range sources {
		root, err := filepath.EvalSymlinks(source.BundlePath)
		if err != nil {
			return err
		}
		if withinCheckPath(root, absolute) {
			return fmt.Errorf("%w: --output must be outside input capture archives", errUsage)
		}
		arazzoInputs := append([]string{}, source.ArazzoFiles...)
		if source.Arazzo != "" {
			arazzoInputs = append(arazzoInputs, source.Arazzo)
			info, statErr := os.Stat(source.Arazzo)
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
			if info == nil || !info.IsDir() {
				// Early run-level failures can precede companion verification.
				// Keep the export tree protected even when no file inventory was
				// produced; companions share the workflow's containing directory.
				arazzoInputs = append(arazzoInputs, filepath.Dir(source.Arazzo))
				if resolved, resolveErr := filepath.EvalSymlinks(source.Arazzo); resolveErr == nil {
					arazzoInputs = append(arazzoInputs, filepath.Dir(resolved))
				} else if !errors.Is(resolveErr, os.ErrNotExist) {
					return resolveErr
				}
			}
		}
		for _, input := range arazzoInputs {
			if err := protectCheckInput(absolute, input, "Arazzo"); err != nil {
				return err
			}
		}
	}
	if info, err := os.Lstat(absolute); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: --output must be a regular file", errUsage)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeArchiveReport(absolute, data)
}

func protectCheckInput(output, input, kind string) error {
	inputAbs, err := filepath.Abs(input)
	if err != nil {
		return err
	}
	if output == inputAbs || sameCheckFile(output, inputAbs) {
		return fmt.Errorf("%w: --output must not replace %s inputs", errUsage, kind)
	}
	resolved, err := filepath.EvalSymlinks(input)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if output == resolved || info.IsDir() && withinCheckPath(resolved, output) {
		return fmt.Errorf("%w: --output must not replace %s inputs", errUsage, kind)
	}
	return nil
}

func withinCheckPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	// EvalSymlinks need not canonicalize casing. Match existing ancestors by
	// file identity too, so a casing alias cannot overwrite a source archive.
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		if sameCheckFile(root, current) {
			return true
		}
		if filepath.Dir(current) == current {
			return false
		}
	}
}

func sameCheckFile(left, right string) bool {
	a, err := os.Stat(left)
	if err != nil {
		return false
	}
	b, err := os.Stat(right)
	return err == nil && os.SameFile(a, b)
}
