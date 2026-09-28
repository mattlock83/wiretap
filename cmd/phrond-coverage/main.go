package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
	"github.com/phrond/evermock/internal/bundlereport"
	"github.com/phrond/evermock/internal/coverage"
)

func main() {
	var artifactsDir string
	var bundlePath string
	var sutName string
	var outputPath string
	var format string
	var includeArtifacts bool

	flag.StringVar(&artifactsDir, "artifacts", "", "explicit read-only legacy artifact directory")
	flag.StringVar(&bundlePath, "bundle", "", "capture bundle directory")
	flag.StringVar(&sutName, "sut-name", "", "SUT name to display in the report")
	flag.StringVar(&outputPath, "output", "", "output file path (default: stdout)")
	flag.StringVar(&format, "format", "okf", "output format: okf, openapi, workflow, html, json")
	flag.BoolVar(&includeArtifacts, "include-artifacts", false, "include artifact file paths in the report")
	flag.Parse()
	if format == "postman" || format == "collection" {
		fmt.Fprintln(os.Stderr, "error: Postman export has been removed; use the OKF bundle or --format workflow")
		os.Exit(2)
	}
	if bundlePath != "" && artifactsDir != "" {
		fmt.Fprintln(os.Stderr, "error: --bundle and --artifacts are mutually exclusive")
		os.Exit(2)
	}
	if bundlePath == "" && artifactsDir == "" {
		fmt.Fprintln(os.Stderr, "error: --bundle is required (use --artifacts only for legacy reads)")
		os.Exit(2)
	}
	if format == "okf" {
		var path string
		var err error
		if artifactsDir != "" {
			if outputPath == "" {
				fmt.Fprintln(os.Stderr, "error: --output bundle directory is required for legacy import")
				os.Exit(2)
			}
			path, err = importLegacy(artifactsDir, outputPath)
		} else {
			if outputPath != "" {
				fmt.Fprintln(os.Stderr, "error: --output is only for file exports or legacy import")
				os.Exit(2)
			}
			_, err = bundlereport.Rebuild(bundlePath)
			path, _ = filepath.Abs(filepath.Join(bundlePath, "index.md"))
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(path)
		return
	}
	input := bundlePath
	if input == "" {
		input = artifactsDir
	} else if _, _, err := bundle.Load(input); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	report, err := coverage.Generate(context.Background(), input, sutName, includeArtifacts)
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

func importLegacy(source, destination string) (string, error) {
	captures, err := coverage.ReadLegacy(source)
	if err != nil {
		return "", err
	}
	w, err := bundle.NewAt(destination)
	if err != nil {
		return "", err
	}
	if err := w.Configure(bundle.Identity{TestSource: map[string]any{"legacy_import": true, "capture_provenance": "unverified"}}); err != nil {
		return "", err
	}
	for index, art := range captures {
		originalID := art.TestCase.ID
		art.TestCase.ID = fmt.Sprintf("legacy-%d", index+1)
		if art.Meta == nil {
			art.Meta = map[string]any{}
		}
		art.Meta["legacy_original_testcase_id"] = originalID
		art.Meta["legacy_import"] = true
		var available []int
		for i := range art.Schemas {
			ref := &art.Schemas[i]
			if strings.EqualFold(ref.Source.Type, "url") {
				art.CaptureDiagnostics = append(art.CaptureDiagnostics, fmt.Sprintf("schema %q had a URL source; original capture-time schema is unavailable", ref.ID))
				ref.Source.Type, ref.Source.Path, ref.Source.URL, ref.Source.Content, ref.Source.SHA256 = "unavailable", "", "", "", ""
				continue
			}
			if strings.EqualFold(ref.Source.Type, "file") {
				data, readErr := os.ReadFile(ref.Source.Path)
				sum := sha256.Sum256(data)
				if readErr != nil || ref.Source.SHA256 == "" || !strings.EqualFold(ref.Source.SHA256, hex.EncodeToString(sum[:])) {
					art.CaptureDiagnostics = append(art.CaptureDiagnostics, fmt.Sprintf("schema %q capture-time source cannot be verified at import", ref.ID))
					ref.Source.Type, ref.Source.Path, ref.Source.URL, ref.Source.Content, ref.Source.SHA256 = "unavailable", "", "", "", ""
					continue
				}
			}
			available = append(available, i)
		}
		refs := make([]artifact.SchemaRef, 0, len(available))
		for _, i := range available {
			refs = append(refs, art.Schemas[i])
		}
		frozen, err := w.Register(art.TestCase, refs)
		if err != nil {
			return "", err
		}
		for i, position := range available {
			art.Schemas[position] = frozen[i]
		}
		if _, err := w.Commit(art); err != nil {
			return "", err
		}
	}
	if err := w.Finalize("complete"); err != nil {
		return "", err
	}
	if _, err := bundlereport.Rebuild(w.Path()); err != nil {
		return "", err
	}
	return w.IndexPath(), nil
}
