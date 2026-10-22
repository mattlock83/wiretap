package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/phrond/wiretap/internal/coverage"
)

var errUsage = errors.New("usage")

func reportCommand(args []string) error {
	flags := flag.NewFlagSet("wiretap report", flag.ContinueOnError)
	var bundlePath string
	var sutName string
	var outputPath string
	var outputDir string
	var format string
	var includeArtifacts bool
	flags.StringVar(&bundlePath, "bundle", "", "capture bundle directory")
	flags.StringVar(&sutName, "sut-name", "", "SUT name in the report or SUT Postman collection name")
	flags.StringVar(&outputPath, "output", "", "output file path; additional Postman collections are written alongside the primary collection (default: stdout)")
	flags.StringVar(&outputDir, "output-dir", "", "Postman collection output directory")
	flags.StringVar(&format, "format", "json", "output format: json, yaml, postman")
	flags.BoolVar(&includeArtifacts, "include-artifacts", false, "include artifact file paths in the report")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if len(flags.Args()) != 0 {
		return fmt.Errorf("%w: unexpected arguments: %v", errUsage, flags.Args())
	}
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "json", "yaml", "postman":
	default:
		return fmt.Errorf("%w: unsupported format: %s", errUsage, format)
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
	if format == "postman" {
		return exportPostman(bundlePath, sutName, outputPath, outputDir)
	}
	report, err := coverage.Generate(context.Background(), bundlePath, sutName, includeArtifacts)
	if err != nil {
		return err
	}
	var out *os.File
	if outputPath == "" {
		out = os.Stdout
	} else {
		file, err := os.Create(outputPath)
		if err != nil {
			return err
		}
		defer file.Close()
		out = file
	}

	if err := coverage.WriteReport(out, report, format); err != nil {
		return err
	}
	return nil
}
