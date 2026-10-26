package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattlock83/wiretap/internal/htmlreport"
)

// The standalone renderer reads only explicit local inputs; it never treats
// workflow URLs or OpenAPI references as instructions to open more files.
func exportHTMLFiles(files htmlreport.Files, output string) error {
	inputs, err := htmlreport.Load(files)
	if err != nil {
		return fmt.Errorf("%w: HTML inputs: %v", errUsage, err)
	}
	var data bytes.Buffer
	if err := htmlreport.Write(&data, inputs); err != nil {
		return err
	}
	if output == "" {
		_, err = os.Stdout.Write(data.Bytes())
		return err
	}
	absolute, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	paths := append([]string{files.Coverage}, files.Specs...)
	if files.Arazzo != "" {
		paths = append(paths, files.Arazzo)
	}
	for _, path := range paths {
		if err := protectCheckInput(absolute, path, "HTML source"); err != nil {
			return err
		}
	}
	if info, err := os.Lstat(absolute); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%w: --output must be a regular file", errUsage)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeArchiveReport(absolute, data.Bytes())
}
