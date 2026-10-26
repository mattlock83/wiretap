package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattlock83/wiretap/internal/archiveoutput"
	"github.com/mattlock83/wiretap/internal/runoutput"
)

func exportArazzo(bundlePath, sutName, outputPath, outputDir string) error {
	snapshot, err := runoutput.Load(context.Background(), bundlePath, sutName, false)
	if err != nil {
		return err
	}
	export, err := snapshot.Arazzo()
	if err != nil {
		return err
	}
	if export == nil || export.Document == nil {
		return errors.New("Arazzo export requires captured JSON/REST traffic")
	}
	if outputPath == "" && outputDir == "" && len(export.Files) > 0 {
		return fmt.Errorf("%w: OpenAPI companions require --output-dir <directory> or --output <arazzo.json>", errUsage)
	}
	if outputPath == "" && outputDir == "" {
		files, err := runoutput.ArazzoFiles(export, "arazzo.json")
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(files[0].Data)
		return err
	}
	base := outputDir
	relative := "arazzo.json"
	if outputPath != "" {
		base = filepath.Dir(outputPath)
		relative = filepath.Base(outputPath)
	}
	files, err := runoutput.ArazzoFiles(export, relative)
	if err != nil {
		return err
	}
	guard, err := archiveoutput.New(bundlePath)
	if err != nil {
		return err
	}
	writer := archiveoutput.Writer{Guard: guard, Directory: base, CreateDir: outputDir != ""}
	return archiveOutputError(writer.Write(context.Background(), files))
}
