package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/phrond/wiretap/internal/bundlereport"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/knowledge"
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
	flags.StringVar(&outputPath, "output", "", "output file path; Postman mock collections are written alongside the SUT file (default: stdout)")
	flags.StringVar(&outputDir, "output-dir", "", "Postman collection output directory")
	flags.StringVar(&format, "format", "okf", "output format: okf, json, yaml, postman")
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
	case "okf", "json", "yaml", "postman":
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
	// Publications select observations rather than describing one synthetic run.
	// Export verified records without inventing aggregate coverage across sources.
	if b, err := os.ReadFile(filepath.Join(bundlePath, "bundle.json")); err == nil {
		if v, err := knowledge.Decode(b); err == nil {
			if m, ok := v.(map[string]any); ok && m["profile"] != nil {
				records, err := knowledge.Read(bundlePath)
				if err != nil {
					return err
				}
				if format == "okf" {
					if outputPath != "" {
						return fmt.Errorf("%w: --output is only for file exports", errUsage)
					}
					p, err := filepath.Abs(filepath.Join(bundlePath, "index.md"))
					if err != nil {
						return err
					}
					fmt.Println(p)
					return nil
				}
				var data []byte
				if format == "json" {
					data, err = knowledge.Canonical(records)
				} else {
					data, err = knowledge.YAML(records)
				}
				if err != nil {
					return err
				}
				if outputPath != "" {
					return os.WriteFile(outputPath, data, 0644)
				}
				_, err = os.Stdout.Write(data)
				return err
			}
		}
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
