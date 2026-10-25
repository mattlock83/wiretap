package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
)

func compareCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("wiretap compare", flag.ContinueOnError)
	baseline := flags.String("baseline", "", "baseline coverage JSON report")
	current := flags.String("current", "", "current coverage JSON report")
	format := flags.String("format", "text", "comparison format: text or json")
	output := flags.String("output", "", "report file outside the two inputs (default: stdout)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: wiretap compare --baseline <coverage.json> --current <coverage.json> [options]\nCompare measured coverage. Lost coverage returns exit 1; schema changes are descriptive.\n\nOptions:")
		flags.PrintDefaults()
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *baseline == "" || *current == "" || *format != "text" && *format != "json" {
		return fmt.Errorf("%w: use compare --baseline coverage.json --current coverage.json [--format text|json] [--output file]", errUsage)
	}
	limits, err := bundle.ConfiguredLoadLimits()
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if limits.MaxFileReads < 2 {
		return fmt.Errorf("%w: %w: comparing reports requires two file reads", errUsage, bundle.ErrLoadLimit)
	}
	var used int64
	before, oldPath, err := readComparisonReport(ctx, *baseline, limits, &used)
	if err != nil {
		return fmt.Errorf("%w: baseline: %w", errUsage, err)
	}
	after, newPath, err := readComparisonReport(ctx, *current, limits, &used)
	if err != nil {
		return fmt.Errorf("%w: current: %w", errUsage, err)
	}
	diff, err := coverage.CompareReports(before, after)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	var data bytes.Buffer
	if *format == "json" {
		encoder := json.NewEncoder(&data)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(diff); err != nil {
			return err
		}
	} else {
		if err := writeComparisonText(&data, diff); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if *output == "" {
		_, err = os.Stdout.Write(data.Bytes())
	} else {
		err = writeComparisonOutput(*output, []string{oldPath, newPath}, data.Bytes())
	}
	if err != nil {
		return err
	}
	if len(diff.LostCoverage) > 0 {
		return fmt.Errorf("%w: %d previously covered contract targets lost coverage", errCheckIncompatible, len(diff.LostCoverage))
	}
	return nil
}

func readComparisonReport(ctx context.Context, path string, limits bundle.LoadLimits, used *int64) (*coverage.Report, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, "", err
	}
	limit := min(limits.MaxFileBytes, limits.MaxTotalBytes-*used)
	if !info.Mode().IsRegular() {
		return nil, "", errors.New("coverage report must be a regular file")
	}
	if info.Size() > limit {
		return nil, "", fmt.Errorf("%w: coverage report exceeds file or remaining total byte budget", bundle.ErrLoadLimit)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", errors.New("coverage report is no longer a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(raw)) > limit {
		return nil, "", fmt.Errorf("%w: coverage report grew beyond its byte budget", bundle.ErrLoadLimit)
	}
	*used += int64(len(raw))
	var report coverage.Report
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&report); err != nil {
		return nil, "", err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, "", errors.New("coverage report contains trailing JSON or invalid data")
	}
	return &report, absolute, nil
}

func writeComparisonOutput(path string, inputs []string, data []byte) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	for _, input := range inputs {
		if absolute == input || sameCheckFile(absolute, input) {
			return fmt.Errorf("%w: comparison output must not replace either input report", errUsage)
		}
		// Standard reports live under an archive's exports directory. Avoid
		// writing anywhere in that original archive, including its manifest.
		// Externally saved reports do not encode the original archive location.
		for current := filepath.Dir(input); ; current = filepath.Dir(current) {
			if info, err := os.Stat(filepath.Join(current, "bundle.json")); err == nil && info.Mode().IsRegular() && withinCheckPath(current, absolute) {
				return fmt.Errorf("%w: comparison output must be outside input capture archives", errUsage)
			}
			if filepath.Dir(current) == current {
				break
			}
		}
	}
	if info, err := os.Lstat(absolute); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%w: comparison output must be a regular file", errUsage)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeArchiveReport(absolute, data)
}
