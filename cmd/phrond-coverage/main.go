package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/phrond/evermock/internal/coverage"
)

func main() {
	var artifactsDir string
	var sutName string
	var outputPath string
	var format string
	var consumerName string
	var includeArtifacts bool
	providerNames := providerNameFlags{}

	flag.StringVar(&artifactsDir, "artifacts", "artifacts", "directory containing artifact files")
	flag.StringVar(&sutName, "sut-name", "", "SUT name to display in the report")
	flag.StringVar(&outputPath, "output", "", "output file path, or output directory for Pact (default: stdout)")
	flag.StringVar(&format, "format", "postman", "output format: openapi, postman, workflow, pact, html, json (yaml is legacy)")
	flag.StringVar(&consumerName, "consumer-name", "", "stable Pact consumer participant name")
	flag.Var(&providerNames, "provider-name", "Pact provider override in asset-id=participant-name form; repeat as needed")
	flag.BoolVar(&includeArtifacts, "include-artifacts", false, "include artifact file paths in the report")
	flag.Parse()

	report, err := coverage.Generate(context.Background(), artifactsDir, sutName, includeArtifacts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if strings.EqualFold(strings.TrimSpace(format), "pact") {
		directory := outputPath
		if strings.TrimSpace(directory) == "" {
			directory = filepath.Join(artifactsDir, "pacts")
		}
		export, paths, err := coverage.WritePactDirectory(report, directory, coverage.PactOptions{
			ConsumerName: consumerName, ProviderNames: providerNames,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if export == nil || len(paths) == 0 {
			fmt.Fprintln(os.Stderr, "error: no passing captured dependency interactions are available for Pact export")
			os.Exit(1)
		}
		for _, path := range paths {
			fmt.Fprintln(os.Stdout, path)
		}
		return
	}

	var out *os.File
	if outputPath == "" {
		out = os.Stdout
	} else {
		file, err := os.Create(outputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer file.Close()
		out = file
	}

	if err := coverage.WriteReport(out, report, format); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type providerNameFlags map[string]string

func (values *providerNameFlags) String() string {
	if values == nil || len(*values) == 0 {
		return ""
	}
	entries := make([]string, 0, len(*values))
	for assetID, name := range *values {
		entries = append(entries, assetID+"="+name)
	}
	sort.Strings(entries)
	return strings.Join(entries, ", ")
}

func (values *providerNameFlags) Set(value string) error {
	assetID, name, found := strings.Cut(value, "=")
	assetID, name = strings.TrimSpace(assetID), strings.TrimSpace(name)
	if !found || assetID == "" || name == "" {
		return fmt.Errorf("provider name must use asset-id=participant-name")
	}
	if *values == nil {
		*values = providerNameFlags{}
	}
	(*values)[assetID] = name
	return nil
}
