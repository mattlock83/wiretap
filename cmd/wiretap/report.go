package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"encoding/json"
	"github.com/mattlock83/wiretap/internal/coverage"
	"github.com/mattlock83/wiretap/internal/postman"
	"github.com/mattlock83/wiretap/internal/runoutput"
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
	case "json", "yaml", "text", "postman", "all":
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
