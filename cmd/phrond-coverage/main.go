package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/phrond/evermock/internal/coverage"
)

func main() {
	var artifactsDir string
	var sutName string
	var outputPath string
	var format string
	var includeArtifacts bool

	flag.StringVar(&artifactsDir, "artifacts", "artifacts", "directory containing artifact files")
	flag.StringVar(&sutName, "sut-name", "", "SUT name to display in the report")
	flag.StringVar(&outputPath, "output", "", "output file path (default: stdout)")
	flag.StringVar(&format, "format", "postman", "output format: openapi, postman, workflow, html, json (yaml is legacy)")
	flag.BoolVar(&includeArtifacts, "include-artifacts", false, "include artifact file paths in the report")
	flag.Parse()

	report, err := coverage.Generate(context.Background(), artifactsDir, sutName, includeArtifacts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
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
