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
				err = writeCheckText(&data, &assumptions.Report{Compatible: report.Compatible, Counts: report.Counts, Matches: report.Matches})
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
	assurance := map[string]int{}
	for _, match := range report.Matches {
		assurance[match.ContractCheck]++
	}
	declarations, provider, examples := assurance["openapi_3_0_validation_rules"], assurance["openapi_3_0_provider_contract"], assurance["unavailable"]
	excluded := ""
	if report.ExcludedProviderEvidence != nil {
		excluded = fmt.Sprintf("Excluded provider exchanges: %d\n", len(report.ExcludedProviderEvidence))
	}
	_, err := fmt.Fprintf(out, "%s: check — %d of %d mock examples verified\nUncovered: %d; mismatched: %d; unverified: %d\nAssurance: %d HTTP declaration and value checks; %d HTTP provider-spec checks; %d example-only matches (schemas unavailable); %d matches with unknown assurance.\n%sUse --format json for complete diagnostics and source evidence.\n",
		status, report.Counts.Verified, report.Counts.Assumptions, report.Counts.Uncovered, report.Counts.Mismatched, report.Counts.Unverified,
		declarations, provider, examples, len(report.Matches)-declarations-provider-examples, excluded)
	return err
}

func writeCheckOutput(path string, sources []assumptions.Source, manifests []string, data []byte) error {
	var inputs []protectedInput
	for _, input := range manifests {
		inputs = append(inputs, protectedInput{input, "run set"})
	}
	for _, source := range sources {
		root, err := filepath.EvalSymlinks(source.BundlePath)
		if err != nil {
			return err
		}
		inputs = append(inputs, protectedInput{root, "capture archive"})
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
			inputs = append(inputs, protectedInput{input, "Arazzo"})
		}
	}
	return writeProtectedReport(path, inputs, data)
}
