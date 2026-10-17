package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/phrond/wiretap/internal/postman"
)

func exportPostman(bundlePath, sutName, outputPath, outputDir string) error {
	exports, err := postman.Generate(bundlePath, sutName)
	if err != nil {
		return err
	}
	if outputPath == "" && outputDir == "" && len(exports) > 1 {
		return fmt.Errorf("%w: multiple Postman collections require --output-dir <directory> or --output <SUT file>", errUsage)
	}
	if outputPath != "" {
		for _, export := range exports[1:] {
			if strings.EqualFold(filepath.Base(outputPath), export.Filename) {
				return fmt.Errorf("%w: --output filename conflicts with mock collection %q; use --output-dir or choose another filename", errUsage, export.Filename)
			}
		}
	}
	// Finish every generation and encoding step before creating output files.
	type outputFile struct {
		path, staged string
		data         []byte
		mode         os.FileMode
		exists       bool
	}
	files := make([]outputFile, len(exports))
	for i, export := range exports {
		var encoded bytes.Buffer
		if err := postman.Write(&encoded, export.Collection); err != nil {
			return err
		}
		file := &files[i]
		file.data = encoded.Bytes()
		if outputDir != "" {
			file.path = filepath.Join(outputDir, export.Filename)
		} else if outputPath != "" {
			file.path = filepath.Join(filepath.Dir(outputPath), export.Filename)
			if i == 0 {
				file.path = outputPath
			}
		}
	}
	if outputPath == "" && outputDir == "" {
		_, err := os.Stdout.Write(files[0].data)
		return err
	}
	// Check all targets before replacing any existing collection. A directory or
	// symlink at a later mock filename must not leave a half-updated export set.
	for i := range files {
		file := &files[i]
		info, err := os.Lstat(file.path)
		if err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("Postman output %q is not a regular file", file.path)
		}
		if err == nil {
			file.mode = info.Mode().Perm()
			file.exists = true
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if outputDir != "" {
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return err
		}
	}
	// Stage all collections in their destination directory so write failures
	// occur before any existing export is replaced.
	stagingDir, err := os.MkdirTemp(filepath.Dir(files[0].path), ".wiretap-postman-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingDir)
	for i := range files {
		output := &files[i]
		output.staged = filepath.Join(stagingDir, filepath.Base(output.path))
		// Ordinary creation permissions respect the process umask. Only an
		// existing destination requires an explicit chmod to preserve its mode.
		file, err := os.OpenFile(output.staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		if output.exists {
			if err := file.Chmod(output.mode); err != nil {
				_ = file.Close()
				return err
			}
		}
		_, writeErr := file.Write(output.data)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return err
		}
	}
	for _, file := range files {
		if err := os.Rename(file.staged, file.path); err != nil {
			return err
		}
	}
	return nil
}
