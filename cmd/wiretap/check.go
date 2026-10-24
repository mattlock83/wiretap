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
	var consumerCollections, providerCollections, ignored checkValues
	var format, output, artifacts string
	flags.StringVar(&options.Consumer, "consumer", "", "consumer run directory or bundle.json")
	flags.StringVar(&options.Provider, "provider", "", "provider run directory or bundle.json")
	flags.StringVar(&artifacts, "artifacts", "", "local shared artifact root, grouped by application ID; discovers peers for the supplied run")
	flags.StringVar(&options.Dependency, "dependency", "", "consumer's local mock ID (all mocks in consumer discovery; inferred for explicit pairs)")
	flags.Var(&consumerCollections, "consumer-collection", "consumer Postman file or directory for explicit pairs; repeatable (default: <consumer>/exports/postman)")
	flags.Var(&providerCollections, "provider-collection", "provider Postman file or directory for explicit pairs; repeatable (default: <provider>/exports/postman)")
	flags.Var(&ignored, "ignore-header", "ignore another captured HTTP header or gRPC metadata field; declared HTTP header rules still apply; repeatable")
	flags.StringVar(&format, "format", "text", "report format: text or json")
	flags.StringVar(&output, "output", "", "write report outside input archives and artifact root (default: stdout)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: wiretap check consumer --consumer <capture> --artifacts <directory> [options]\n       wiretap check provider --provider <capture> --artifacts <directory> [options]\n       wiretap check <consumer|provider> --consumer <capture> --provider <capture> [options]\n\nConsumer checks discover mapped providers; provider checks discover mapped consumers.\nThe artifact root contains application folders with complete runs and exports/postman.\nSelections must be unambiguous; historical snapshots are never merged.\nExplicit pairs check one mock boundary. No traffic is replayed.")
		flags.PrintDefaults()
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		flags.Usage()
		return 0, nil
	}
	if len(args) == 0 || args[0] != "consumer" && args[0] != "provider" {
		return 2, fmt.Errorf("%w: use wiretap check consumer or wiretap check provider", errUsage)
	}
	options.Mode = args[0]
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, fmt.Errorf("%w: %v", errUsage, err)
	}
	if len(flags.Args()) != 0 {
		return 2, fmt.Errorf("%w: unexpected arguments: %v", errUsage, flags.Args())
	}
	if artifacts == "" && (options.Consumer == "" || options.Provider == "") {
		return 2, fmt.Errorf("%w: supply --consumer and --provider, or the local --%s capture with --artifacts", errUsage, options.Mode)
	}
	if artifacts != "" && (len(consumerCollections) > 0 || len(providerCollections) > 0) {
		return 2, fmt.Errorf("%w: --artifacts uses each run's exports/postman; collection overrides require an explicit consumer/provider pair", errUsage)
	}
	if format != "text" && format != "json" {
		return 2, fmt.Errorf("%w: --format must be text or json", errUsage)
	}
	options.ConsumerCollections = consumerCollections
	options.ProviderCollections = providerCollections
	options.IgnoreHeaders = ignored
	var data bytes.Buffer
	var result any
	var compatible bool
	var sources []assumptions.Source
	var roots []string
	var err error
	if artifacts != "" {
		var report *assumptions.StoreReport
		report, err = assumptions.CheckStore(ctx, assumptions.StoreOptions{
			Mode: options.Mode, Consumer: options.Consumer, Provider: options.Provider,
			Dependency: options.Dependency, Artifacts: artifacts,
		}, options.IgnoreHeaders)
		if err == nil {
			result, compatible = report, report.Compatible
			roots = []string{report.ArtifactsPath}
			for _, comparison := range report.Comparisons {
				sources = append(sources, comparison.Consumer, comparison.Provider)
			}
			if format == "text" {
				err = writeStoreCheckText(&data, report)
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
		if errors.Is(err, assumptions.ErrInput) {
			return 2, err
		}
		return 1, err
	}
	if output == "" {
		_, err = os.Stdout.Write(data.Bytes())
	} else {
		err = writeCheckOutput(output, sources, roots, data.Bytes())
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

func writeStoreCheckText(out io.Writer, report *assumptions.StoreReport) error {
	status := "FAIL"
	if report.Compatible {
		status = "PASS"
	}
	if _, err := fmt.Fprintf(out, "%s: check %s — %d comparisons; %d of %d mock examples verified\nArtifacts: %s\n", status, report.Mode, len(report.Comparisons), report.Counts.Verified, report.Counts.Assumptions, report.ArtifactsPath); err != nil {
		return err
	}
	for _, comparison := range report.Comparisons {
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
		if err := writeCheckText(out, comparison); err != nil {
			return err
		}
	}
	return nil
}

func writeCheckText(out io.Writer, report *assumptions.Report) error {
	status := "FAIL"
	if report.Compatible {
		status = "PASS"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s: check %s — %d of %d mock examples verified\n", status, report.Mode, report.Counts.Verified, report.Counts.Assumptions)
	fmt.Fprintf(&text, "Consumer: %s (%s)\nProvider: %s (%s)\nMock dependency: %s; binding: %s\n", report.Consumer.BundlePath, report.Consumer.RunID, report.Provider.BundlePath, report.Provider.RunID, report.Dependency, report.Binding)
	fmt.Fprintf(&text, "Uncovered: %d; mismatched: %d; unverified: %d\n", report.Counts.Uncovered, report.Counts.Mismatched, report.Counts.Unverified)
	contracts := map[string]int{}
	for _, match := range report.Matches {
		contracts[match.ContractCheck]++
	}
	fmt.Fprintf(&text, "Contracts: %d HTTP declaration and value checks; %d HTTP provider-spec checks; %d protobuf declaration and wire checks; %d example-only matches (schemas unavailable)\n", contracts["openapi_3_0_validation_rules"], contracts["openapi_3_0_provider_contract"], contracts["protobuf_rules_and_observed_wire"], contracts["unavailable"])
	for _, finding := range report.Findings {
		fmt.Fprintf(&text, "\n[%s] %s: %s", finding.Kind, finding.Code, finding.Operation)
		if finding.Operation != "" {
			text.WriteString(": ")
		}
		fmt.Fprintf(&text, "%s\n", finding.Message)
		if finding.Consumer != nil {
			fmt.Fprintf(&text, "  Consumer test %q, flow %s: %s\n", finding.Consumer.TestName, finding.Consumer.FlowID, finding.Consumer.EvidencePath)
		}
		for _, ref := range finding.Provider {
			fmt.Fprintf(&text, "  Provider test %q, flow %s: %s\n", ref.TestName, ref.FlowID, ref.EvidencePath)
		}
		for _, path := range finding.Differences {
			fmt.Fprintf(&text, "  Different: %s\n", path)
		}
	}
	if len(report.ExcludedProviderEvidence) > 0 {
		fmt.Fprintf(&text, "\nExcluded %d provider exchanges that cannot establish coverage.\n", len(report.ExcludedProviderEvidence))
		for _, excluded := range report.ExcludedProviderEvidence {
			fmt.Fprintf(&text, "  Test %q, flow %s: %s\n", excluded.Evidence.TestName, excluded.Evidence.FlowID, excluded.Reason)
		}
	}
	fmt.Fprintf(&text, "\nPolicy: %s; exact retained examples and exercised contract rules after endpoint/header normalization.\n", report.RulesVersion)
	_, err := io.WriteString(out, text.String())
	return err
}

func writeCheckOutput(path string, sources []assumptions.Source, roots []string, data []byte) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	for _, input := range roots {
		root, err := filepath.EvalSymlinks(input)
		if err != nil {
			return err
		}
		if absolute == root || sameCheckFile(root, absolute) || withinCheckPath(root, absolute) {
			return fmt.Errorf("%w: --output must be outside the shared artifact directory", errUsage)
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
		for _, input := range source.Collections {
			inputAbs, err := filepath.Abs(input)
			if err != nil {
				return err
			}
			if absolute == inputAbs || sameCheckFile(absolute, inputAbs) {
				return fmt.Errorf("%w: --output must not replace Postman inputs", errUsage)
			}
			resolved, err := filepath.EvalSymlinks(input)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return err
			}
			info, err := os.Stat(resolved)
			if err != nil {
				return err
			}
			if absolute == resolved || info.IsDir() && withinCheckPath(resolved, absolute) {
				return fmt.Errorf("%w: --output must not replace Postman inputs", errUsage)
			}
		}
	}
	mode := os.FileMode(0o644)
	if info, err := os.Lstat(absolute); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: --output must be a regular file", errUsage)
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(parent, ".wiretap-check-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), absolute)
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
