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
	var format, output string
	flags.StringVar(&options.Consumer, "consumer", "", "consumer run directory or bundle.json")
	flags.StringVar(&options.Provider, "provider", "", "provider run directory or bundle.json")
	flags.StringVar(&options.Dependency, "dependency", "", "consumer's local mock dependency ID (inferred when unambiguous)")
	flags.Var(&consumerCollections, "consumer-collection", "consumer Postman collection file or directory; repeatable (default: <consumer>/exports/postman)")
	flags.Var(&providerCollections, "provider-collection", "provider Postman collection file or directory; repeatable (default: <provider>/exports/postman)")
	flags.Var(&ignored, "ignore-header", "ignore another captured HTTP header or gRPC metadata field; declared HTTP header rules still apply; repeatable")
	flags.StringVar(&format, "format", "text", "report format: text or json")
	flags.StringVar(&output, "output", "", "write report outside input archives (default: stdout)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: wiretap check consumer --consumer <capture> --provider <capture> [options]\n       wiretap check provider --provider <capture> --consumer <capture> [options]\n\nVerify mock request/response examples and exercised contract rules against complete passing provider tests.\nBoth viewpoints enforce the same consumer-to-provider obligation. No traffic is replayed.")
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
	if options.Consumer == "" || options.Provider == "" {
		return 2, fmt.Errorf("%w: --consumer and --provider are required", errUsage)
	}
	if format != "text" && format != "json" {
		return 2, fmt.Errorf("%w: --format must be text or json", errUsage)
	}
	options.ConsumerCollections = consumerCollections
	options.ProviderCollections = providerCollections
	options.IgnoreHeaders = ignored
	report, err := assumptions.Check(ctx, options)
	if err != nil {
		if errors.Is(err, assumptions.ErrInput) {
			return 2, err
		}
		return 1, err
	}
	var data bytes.Buffer
	if format == "json" {
		encoder := json.NewEncoder(&data)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(report)
	} else {
		err = writeCheckText(&data, report)
	}
	if err != nil {
		return 1, err
	}
	if output == "" {
		_, err = os.Stdout.Write(data.Bytes())
	} else {
		err = writeCheckOutput(output, report, data.Bytes())
	}
	if err != nil {
		if errors.Is(err, errUsage) {
			return 2, err
		}
		return 1, err
	}
	if !report.Compatible {
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
	fmt.Fprintf(&text, "%s: check %s — %d of %d mock examples verified\n", status, report.Mode, report.Counts.Verified, report.Counts.Assumptions)
	fmt.Fprintf(&text, "Consumer: %s (%s)\nProvider: %s (%s)\nMock dependency: %s; binding: %s\n", report.Consumer.BundlePath, report.Consumer.RunID, report.Provider.BundlePath, report.Provider.RunID, report.Dependency, report.Binding)
	fmt.Fprintf(&text, "Uncovered: %d; mismatched: %d; unverified: %d\n", report.Counts.Uncovered, report.Counts.Mismatched, report.Counts.Unverified)
	contracts := map[string]int{}
	for _, match := range report.Matches {
		contracts[match.ContractCheck]++
	}
	fmt.Fprintf(&text, "Contracts: %d HTTP validation-rule checks; %d protobuf declaration and wire checks; %d example-only matches (schemas unavailable)\n", contracts["openapi_3_0_validation_rules"], contracts["protobuf_rules_and_observed_wire"], contracts["unavailable"])
	for _, finding := range report.Findings {
		fmt.Fprintf(&text, "\n[%s] %s", finding.Kind, finding.Operation)
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

func writeCheckOutput(path string, report *assumptions.Report, data []byte) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	for _, source := range []assumptions.Source{report.Consumer, report.Provider} {
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
