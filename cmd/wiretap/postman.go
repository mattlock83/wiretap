package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattlock83/wiretap/internal/archiveoutput"
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
	type outputFile struct {
		path, staged, relative string
		data                   []byte
		mode                   os.FileMode
		exists                 bool
	}
	files := make([]outputFile, len(exports))
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
			files = append(files, outputFile{path: filepath.Join(base, filepath.FromSlash(companion.Path)), relative: companion.Path, data: companion.Data})
		}
	}
	if len(result.Diagnostics) > 0 {
		data, err := json.MarshalIndent(result.Diagnostics, "", "  ")
		if err != nil {
			return err
		}
		files = append(files, outputFile{path: filepath.Join(base, "wiretap-postman-diagnostics.json"), relative: "wiretap-postman-diagnostics.json", data: append(data, '\n')})
	}
	guard, err := archiveoutput.New(bundlePath)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for i := range files {
		file := &files[i]
		file.path, err = archiveOutputDestination(guard, file.path)
		if err != nil {
			return err
		}
		key := strings.ToLower(filepath.Clean(file.path))
		if seen[key] {
			return fmt.Errorf("%w: Postman output filename collision: %s", errUsage, file.path)
		}
		seen[key] = true
		if err := postmanOutputParents(base, file.relative); err != nil {
			return err
		}
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
		output.staged = filepath.Join(stagingDir, filepath.FromSlash(output.relative))
		if err := os.MkdirAll(filepath.Dir(output.staged), 0755); err != nil {
			return err
		}
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
		if err := os.MkdirAll(filepath.Dir(file.path), 0755); err != nil {
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

func postmanOutputParents(base, relative string) error {
	current := base
	dirs := append([]string{""}, strings.Split(filepath.ToSlash(filepath.Dir(relative)), "/")...)
	for _, part := range dirs {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("Postman destination %q is not a regular directory", current)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
