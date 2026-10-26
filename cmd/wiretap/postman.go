package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattlock83/wiretap/internal/postman"
)

func exportPostman(bundlePath, sutName, outputPath, outputDir string) error {
	return exportPostmanWithOptions(bundlePath, sutName, outputPath, outputDir, postman.Options{})
}

func exportPostmanWithOptions(bundlePath, sutName, outputPath, outputDir string, options postman.Options) error {
	result, err := postman.GenerateAllWithOptions(bundlePath, sutName, options)
	if err != nil {
		return err
	}
	exports := result.Exports
	for _, diagnostic := range result.Diagnostics {
		fmt.Fprintf(os.Stderr, "Postman omitted test %q flow %q (%s): %s\n", diagnostic.AttemptID, diagnostic.FlowID, diagnostic.Protocol, diagnostic.Message)
	}
	if outputPath == "" && outputDir == "" {
		for _, export := range exports {
			if len(export.Files) > 0 {
				return fmt.Errorf("%w: source proto companions require --output-dir or --output", errUsage)
			}
		}
	}
	if outputPath == "" && outputDir == "" && len(exports) > 1 {
		return fmt.Errorf("%w: multiple Postman collections require --output-dir <directory> or --output <primary file>", errUsage)
	}
	if outputPath != "" {
		for _, export := range exports[1:] {
			if strings.EqualFold(filepath.Base(outputPath), export.Filename) {
				return fmt.Errorf("%w: --output filename conflicts with additional collection %q; use --output-dir or choose another filename", errUsage, export.Filename)
			}
		}
	}
	// Finish every generation and encoding step before creating output files.
	files := make([]archiveExportFile, len(exports))
	for i, export := range exports {
		var encoded bytes.Buffer
		if err := postman.WriteExport(&encoded, export); err != nil {
			return err
		}
		file := &files[i]
		file.data = encoded.Bytes()
		file.relative = export.Filename
		if outputDir != "" {
			file.path = filepath.Join(outputDir, export.Filename)
		} else if outputPath != "" {
			file.path = filepath.Join(filepath.Dir(outputPath), export.Filename)
			if i == 0 {
				file.path = outputPath
				file.relative = filepath.Base(outputPath)
			}
		}
	}
	if outputPath == "" && outputDir == "" {
		_, err := os.Stdout.Write(files[0].data)
		return err
	}
	base := outputDir
	if base == "" {
		base = filepath.Dir(outputPath)
	}
	for _, export := range exports {
		for _, companion := range export.Files {
			files = append(files, archiveExportFile{path: filepath.Join(base, filepath.FromSlash(companion.Path)), relative: companion.Path, data: companion.Data})
		}
	}
	if len(result.Diagnostics) > 0 {
		data, err := json.MarshalIndent(result.Diagnostics, "", "  ")
		if err != nil {
			return err
		}
		files = append(files, archiveExportFile{path: filepath.Join(base, "wiretap-postman-diagnostics.json"), relative: "wiretap-postman-diagnostics.json", data: append(data, '\n')})
	}
	return writeArchiveExportFiles(bundlePath, base, "Postman", outputDir != "", files)
}
