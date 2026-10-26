package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattlock83/wiretap/internal/archiveoutput"
)

func archiveOutputError(err error) error {
	if errors.Is(err, archiveoutput.ErrDestination) {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	return err
}

func archiveOutputDestination(guard *archiveoutput.Guard, path string) (string, error) {
	destination, err := guard.Destination(path)
	return destination, archiveOutputError(err)
}

func writeArchiveReport(path string, data []byte) error {
	writer := archiveoutput.Writer{Directory: filepath.Dir(path)}
	return writer.Write(context.Background(), []archiveoutput.File{{Name: filepath.Base(path), Data: data}})
}

// Inputs may be files or whole trees; generated output must not replace either.
type protectedInput struct{ path, kind string }

func writeProtectedReport(output string, inputs []protectedInput, data []byte) error {
	absolute, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	for _, input := range inputs {
		if err := protectReportInput(absolute, input.path, input.kind); err != nil {
			return err
		}
	}
	if info, err := os.Lstat(absolute); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%w: --output must be a regular file", errUsage)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeArchiveReport(absolute, data)
}

func protectReportInput(output, input, kind string) error {
	inputAbs, err := filepath.Abs(input)
	if err != nil {
		return err
	}
	if output == inputAbs || sameCheckFile(output, inputAbs) {
		return fmt.Errorf("%w: --output must not replace %s inputs", errUsage, kind)
	}
	resolved, err := filepath.EvalSymlinks(input)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if output == resolved || info.IsDir() && withinCheckPath(resolved, output) {
		return fmt.Errorf("%w: --output must not replace %s inputs", errUsage, kind)
	}
	return nil
}

func withinCheckPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	// EvalSymlinks need not canonicalize casing. Match existing ancestors by
	// file identity too, so a casing alias cannot overwrite a source archive.
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		if sameCheckFile(root, current) {
			return true
		}
		if filepath.Dir(current) == current {
			return false
		}
	}
}

func sameCheckFile(left, right string) bool {
	a, err := os.Stat(left)
	if err != nil {
		return false
	}
	b, err := os.Stat(right)
	return err == nil && os.SameFile(a, b)
}
