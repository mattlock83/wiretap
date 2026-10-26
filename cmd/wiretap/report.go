package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mattlock83/wiretap/internal/archiveoutput"
	"github.com/mattlock83/wiretap/internal/assumptions"
	"github.com/mattlock83/wiretap/internal/htmlreport"
	"github.com/mattlock83/wiretap/internal/runoutput"
	"github.com/mattlock83/wiretap/internal/runset"
)

func reportCommand(args []string) error {
	flags := flag.NewFlagSet("wiretap report", flag.ContinueOnError)
	var bundlePath string
	var runSetPath string
	flags.StringVar(&runSetPath, "run-set", "", "same-build run set manifest (JSON coverage)")
	var sutName string
	var outputPath string
	var outputDir string
	var format string
	var includeArtifacts bool
	var coverageJSON, arazzoPath string
	var specPaths checkValues
	flags.StringVar(&bundlePath, "bundle", "", "capture bundle directory")
	flags.StringVar(&coverageJSON, "coverage-json", "", "existing Wiretap coverage JSON input for an offline HTML report")
	flags.Var(&specPaths, "spec", "local OpenAPI source file to embed in an HTML report (repeatable; requires --coverage-json)")
	flags.StringVar(&arazzoPath, "arazzo", "", "local Arazzo JSON file to embed in an HTML report (requires --coverage-json)")
	flags.StringVar(&sutName, "sut-name", "", "SUT name in the report or Arazzo workflow document")
	flags.StringVar(&outputPath, "output", "", "output file path; additional export files are written alongside the primary file (default: stdout)")
	flags.StringVar(&outputDir, "output-dir", "", "Arazzo export output directory")
	flags.StringVar(&format, "format", "json", "output format: json, text, html, arazzo, all")
	flags.BoolVar(&includeArtifacts, "include-artifacts", false, "include artifact file paths in the report")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: wiretap report --bundle <archive> [options]\n       wiretap report --coverage-json <coverage.json> --format html [--spec <openapi.yaml>] [--arazzo <arazzo.json>]\n       wiretap report --run-set <manifest.json> [--output coverage.json]\n\nRead captured evidence without replaying traffic. JSON is the default.\n\nExamples:\n  wiretap report --bundle <archive> --format html --output coverage.html\n  wiretap report --bundle <archive> --format arazzo --output-dir arazzo\n\nOptions:")
		flags.PrintDefaults()
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return fmt.Errorf("%w: unexpected arguments: %v", errUsage, flags.Args())
	}
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "json", "text", "html", "arazzo", "all":
	default:
		return fmt.Errorf("%w: unsupported format: %s", errUsage, format)
	}
	if coverageJSON != "" || len(specPaths) > 0 || arazzoPath != "" {
		if coverageJSON == "" || format != "html" || bundlePath != "" || runSetPath != "" || outputDir != "" || sutName != "" || includeArtifacts {
			return fmt.Errorf("%w: --coverage-json, --spec and --arazzo require standalone --format html; do not combine them with --bundle, --run-set or archive options", errUsage)
		}
		return exportHTMLFiles(htmlreport.Files{Coverage: coverageJSON, Specs: specPaths, Arazzo: arazzoPath}, outputPath)
	}
	if runSetPath != "" {
		if bundlePath != "" || format != "json" || outputDir != "" || sutName != "" || includeArtifacts {
			return fmt.Errorf("%w: --run-set supports JSON coverage with optional --output; export Arazzo workflows from each original archive", errUsage)
		}
		set, err := runset.Load(context.Background(), runSetPath)
		if err != nil {
			return fmt.Errorf("%w: run set: %v", errUsage, err)
		}
		report, err := set.Coverage(context.Background())
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(report, "", "  ")
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
	if outputDir != "" && format != "arazzo" {
		return fmt.Errorf("%w: --output-dir is only for Arazzo exports", errUsage)
	}
	if outputDir != "" && outputPath != "" {
		return fmt.Errorf("%w: use either --output-dir or --output", errUsage)
	}
	if format == "arazzo" {
		return exportArazzo(bundlePath, sutName, outputPath, outputDir)
	}
	if format == "all" {
		if outputPath != "" || outputDir != "" || sutName != "" || includeArtifacts {
			return fmt.Errorf("%w: all writes standard outputs inside the capture archive; use --bundle only", errUsage)
		}
		result, exportErr := runoutput.Generate(context.Background(), bundlePath)
		if result != nil {
			if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
				return err
			}
		}
		return exportErr
	}
	snapshot, err := runoutput.Load(context.Background(), bundlePath, sutName, includeArtifacts)
	if err != nil {
		return err
	}
	data, err := snapshot.Coverage(context.Background(), format)
	if err != nil {
		return err
	}
	if outputPath == "" {
		_, err = os.Stdout.Write(data)
		return err
	}
	guard, err := archiveoutput.New(bundlePath)
	if err != nil {
		return err
	}
	outputPath, err = archiveOutputDestination(guard, outputPath)
	if err != nil {
		return err
	}
	return writeArchiveReport(outputPath, data)
}
