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
	"strings"

	"github.com/mattlock83/wiretap/internal/archiveoutput"
	"github.com/mattlock83/wiretap/internal/assumptions"
	"github.com/mattlock83/wiretap/internal/coverage"
	"github.com/mattlock83/wiretap/internal/postman"
	"github.com/mattlock83/wiretap/internal/runoutput"
	"github.com/mattlock83/wiretap/internal/runset"
	"github.com/mattlock83/wiretap/internal/schemajson"
)

var errCheckIncompatible = errors.New("policy or baseline check failed")

func reportCommand(command string, args []string) error {
	flags := flag.NewFlagSet("wiretap "+command, flag.ContinueOnError)
	var bundlePath string
	var runSetPath string
	var policyPath string
	flags.StringVar(&policyPath, "policy", "", "optional coverage policy JSON for one archive; failures return exit 1")
	flags.StringVar(&runSetPath, "run-set", "", "same-build run set manifest (JSON coverage)")
	var sutName string
	var outputPath string
	var outputDir string
	var format string
	var includeArtifacts bool
	var postmanConfig string
	var omitIncomplete bool
	flags.StringVar(&bundlePath, "bundle", "", "capture bundle directory")
	flags.StringVar(&sutName, "sut-name", "", "SUT name in the report or SUT Postman collection name")
	flags.StringVar(&outputPath, "output", "", "output file path; additional Postman collections are written alongside the primary collection (default: stdout)")
	flags.StringVar(&outputDir, "output-dir", "", "Postman collection output directory")
	flags.StringVar(&format, "format", "json", "output format: json, yaml, text, postman, all")
	flags.StringVar(&postmanConfig, "postman-config", "", "Postman export options JSON (gRPC endpoints and source proto packages)")
	flags.BoolVar(&omitIncomplete, "postman-omit-incomplete", false, "omit unrepresentable interactions and report each omission (Postman only)")
	flags.BoolVar(&includeArtifacts, "include-artifacts", false, "include artifact file paths in the report")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: wiretap %s --bundle <archive> [options]\n       wiretap %s --run-set <manifest.json> [--output coverage.json]\n\nRead a verified capture without replaying traffic. JSON is the default.\n\nExamples:\n  wiretap report --bundle <archive> --format text\n  wiretap export --bundle <archive> --format postman --output-dir postman\n  wiretap report --bundle <archive> --policy coverage-policy.json\n\nOptions:\n", command, command)
		flags.PrintDefaults()
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return fmt.Errorf("%w: unexpected arguments: %v", errUsage, flags.Args())
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if policyPath != "" && (runSetPath != "" || format == "all" || format == "postman") {
		return fmt.Errorf("%w: --policy requires one --bundle and json, yaml or text coverage", errUsage)
	}
	if policyPath != "" && outputPath != "" && sameCheckFile(policyPath, outputPath) {
		return fmt.Errorf("%w: output must not replace the coverage policy", errUsage)
	}
	switch format {
	case "json", "yaml", "text", "postman", "all":
	default:
		return fmt.Errorf("%w: unsupported format: %s", errUsage, format)
	}
	if runSetPath != "" {
		if bundlePath != "" || format != "json" || outputDir != "" || postmanConfig != "" || omitIncomplete || sutName != "" || includeArtifacts {
			return fmt.Errorf("%w: --run-set supports JSON coverage with optional --output; export executable Postman collections from each original archive", errUsage)
		}
		set, err := runset.Load(context.Background(), runSetPath)
		if err != nil {
			return fmt.Errorf("%w: run set: %v", errUsage, err)
		}
		data, err := json.MarshalIndent(set.Coverage(), "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if outputPath == "" {
			_, err = os.Stdout.Write(data)
			return err
		}
		sources := []assumptions.Source{}
		for _, archive := range set.Archives {
			sources = append(sources, assumptions.Source{BundlePath: archive.Path})
		}
		return writeCheckOutput(outputPath, sources, []string{runSetPath}, data)
	}
	if bundlePath == "" {
		return fmt.Errorf("%w: --bundle is required", errUsage)
	}
	if outputDir != "" && format != "postman" {
		return fmt.Errorf("%w: --output-dir is only for Postman exports", errUsage)
	}
	if outputDir != "" && outputPath != "" {
		return fmt.Errorf("%w: use either --output-dir or --output", errUsage)
	}
	if (postmanConfig != "" || omitIncomplete) && format != "postman" && format != "all" {
		return fmt.Errorf("%w: Postman options require --format postman or all", errUsage)
	}
	options := postman.Options{}
	if format == "postman" || format == "all" {
		if postmanConfig != "" {
			var err error
			options, err = postman.ReadOptions(postmanConfig)
			if err != nil {
				return fmt.Errorf("%w: Postman config: %v", errUsage, err)
			}
		}
		options.OmitIncomplete = options.OmitIncomplete || omitIncomplete
	}
	if format == "postman" {
		return exportPostmanWithOptions(bundlePath, sutName, outputPath, outputDir, options)
	}
	if format == "all" {
		if outputPath != "" || outputDir != "" || sutName != "" || includeArtifacts {
			return fmt.Errorf("%w: all writes standard outputs inside the capture archive; use --bundle only", errUsage)
		}
		config := runoutput.Defaults()
		config.PostmanOptions = &options
		result, exportErr := runoutput.Generate(context.Background(), bundlePath, config)
		if result != nil {
			if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
				return err
			}
		}
		return exportErr
	}
	report, err := coverage.Generate(context.Background(), bundlePath, sutName, includeArtifacts)
	if err != nil {
		return err
	}
	var verdict error
	if policyPath != "" {
		policy, err := readCoveragePolicy(policyPath)
		if err != nil {
			return fmt.Errorf("%w: coverage policy: %v", errUsage, err)
		}
		result, err := coverage.EvaluatePolicy(report, policy)
		if err != nil {
			return fmt.Errorf("%w: coverage policy: %v", errUsage, err)
		}
		if err := writePolicyText(os.Stderr, result); err != nil {
			return err
		}
		if !result.Passed {
			verdict = errCheckIncompatible
		}
	}
	if outputPath == "" {
		return errors.Join(coverage.WriteReport(os.Stdout, report, format), verdict)
	}
	guard, err := archiveoutput.New(bundlePath)
	if err != nil {
		return err
	}
	outputPath, err = archiveOutputDestination(guard, outputPath)
	if err != nil {
		return err
	}
	var data bytes.Buffer
	if err := coverage.WriteReport(&data, report, format); err != nil {
		return err
	}
	return errors.Join(writeArchiveReport(outputPath, data.Bytes()), verdict)
}

func readCoveragePolicy(path string) (coverage.Policy, error) {
	var policy coverage.Policy
	info, err := os.Stat(path)
	if err != nil {
		return policy, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return policy, errors.New("policy must be a regular JSON file of at most 1 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return policy, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return policy, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return policy, errors.New("policy must be a regular JSON file of at most 1 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return policy, err
	}
	if len(data) > 1<<20 {
		return policy, errors.New("policy exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return policy, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return policy, errors.New("policy must contain one JSON document")
	}
	if _, err := schemajson.Decode(data); err != nil {
		return policy, err
	}
	return policy, nil
}
