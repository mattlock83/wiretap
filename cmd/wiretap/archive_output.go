package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattlock83/wiretap/internal/archiveoutput"
)

func archiveOutputDestination(guard *archiveoutput.Guard, path string) (string, error) {
	destination, err := guard.Destination(path)
	if errors.Is(err, archiveoutput.ErrDestination) {
		return "", fmt.Errorf("%w: %v", errUsage, err)
	}
	return destination, err
}

func writeArchiveReport(path string, data []byte) error {
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Ordinary creation permissions respect the umask. Preserve the exact mode
	// only when replacing an existing report.
	directory, err := os.MkdirTemp(filepath.Dir(path), ".wiretap-report-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	staged := filepath.Join(directory, "report")
	file, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	if info != nil {
		if !info.Mode().IsRegular() {
			_ = file.Close()
			return fmt.Errorf("output %q is not a regular file", path)
		}
		if err := file.Chmod(info.Mode().Perm()); err != nil {
			_ = file.Close()
			return err
		}
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(staged, path)
}

// archiveExportFile is one generated document or its portable source companion.
type archiveExportFile struct {
	path, staged, relative string
	data                   []byte
	mode                   os.FileMode
	exists                 bool
}

// writeArchiveExportFiles verifies and stages the complete set before replacing
// any existing exports. All encoding must finish before calling this helper.
func writeArchiveExportFiles(bundlePath, base, label string, makeBase bool, files []archiveExportFile) error {
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
			return fmt.Errorf("%w: %s output filename collision: %s", errUsage, label, file.path)
		}
		seen[key] = true
		if err := archiveExportParents(base, file.relative, label); err != nil {
			return err
		}
	}
	// Check every target before replacing any existing export. A directory or
	// symlink at a later filename must not leave a half-updated export set.
	for i := range files {
		file := &files[i]
		info, err := os.Lstat(file.path)
		if err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("%s output %q is not a regular file", label, file.path)
		}
		if err == nil {
			file.mode = info.Mode().Perm()
			file.exists = true
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if makeBase {
		if err := os.MkdirAll(base, 0755); err != nil {
			return err
		}
	}
	// Stage all files in their destination directory so write failures
	// occur before any existing export is replaced.
	stagingDir, err := os.MkdirTemp(filepath.Dir(files[0].path), ".wiretap-"+strings.ToLower(label)+"-*")
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

func archiveExportParents(base, relative, label string) error {
	current := base
	dirs := append([]string{""}, strings.Split(filepath.ToSlash(filepath.Dir(relative)), "/")...)
	for _, part := range dirs {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("%s destination %q is not a regular directory", label, current)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
