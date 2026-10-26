package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

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
