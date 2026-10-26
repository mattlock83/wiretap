package main

import (
	"bytes"
	"fmt"
	"os"

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
	paths := append([]string{files.Coverage}, files.Specs...)
	if files.Arazzo != "" {
		paths = append(paths, files.Arazzo)
	}
	inputsToProtect := make([]protectedInput, 0, len(paths))
	for _, path := range paths {
		inputsToProtect = append(inputsToProtect, protectedInput{path, "HTML source"})
	}
	return writeProtectedReport(output, inputsToProtect, data.Bytes())
}
