package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattlock83/wiretap/internal/arazzo"
)

func exportArazzo(bundlePath, sutName, outputPath, outputDir string) error {
	export, err := arazzo.Generate(bundlePath, sutName)
	if err != nil {
		return err
	}
	if export == nil || export.Document == nil {
		return errors.New("Arazzo export requires captured JSON/REST traffic")
	}
	if outputPath == "" && outputDir == "" && len(export.Files) > 0 {
		return fmt.Errorf("%w: OpenAPI companions require --output-dir <directory> or --output <arazzo.json>", errUsage)
	}
	var encoded bytes.Buffer
	if err := arazzo.Write(&encoded, export.Document); err != nil {
		return err
	}
	if outputPath == "" && outputDir == "" {
		_, err := os.Stdout.Write(encoded.Bytes())
		return err
	}
	base := outputDir
	relative := "arazzo.json"
	if outputPath != "" {
		base = filepath.Dir(outputPath)
		relative = filepath.Base(outputPath)
	} else {
		outputPath = filepath.Join(base, relative)
	}
	files := []archiveExportFile{{path: outputPath, relative: relative, data: encoded.Bytes()}}
	for _, companion := range export.Files {
		files = append(files, archiveExportFile{path: filepath.Join(base, filepath.FromSlash(companion.Path)), relative: companion.Path, data: companion.Data})
	}
	return writeArchiveExportFiles(bundlePath, base, "Arazzo", outputDir != "", files)
}
