package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
