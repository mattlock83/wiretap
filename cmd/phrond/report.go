package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/phrond/evermock/internal/bundlereport"
	"github.com/phrond/evermock/internal/coverage"
)

var errUsage = errors.New("usage")

func reportCommand(args []string) error {
	flags := flag.NewFlagSet("phrond report", flag.ContinueOnError)
	var bundlePath string
	var sutName string
	var outputPath string
	var format string
	var includeArtifacts bool
	flags.StringVar(&bundlePath, "bundle", "", "capture bundle directory")
	flags.StringVar(&sutName, "sut-name", "", "SUT name to display in the report")
	flags.StringVar(&outputPath, "output", "", "output file path (default: stdout)")
	flags.StringVar(&format, "format", "okf", "output format: okf, openapi, workflow, json, yaml")
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
	case "okf", "openapi", "workflow", "json", "yaml":
	default:
		return fmt.Errorf("%w: unsupported format: %s", errUsage, format)
	}
	if bundlePath == "" {
		return fmt.Errorf("%w: --bundle is required", errUsage)
	}
	if format == "okf" {
		if outputPath != "" {
			return fmt.Errorf("%w: --output is only for file exports", errUsage)
		}
		if _, err := bundlereport.Rebuild(bundlePath); err != nil {
			return err
		}
		path, err := filepath.Abs(filepath.Join(bundlePath, "index.md"))
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
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
