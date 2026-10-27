package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattlock83/wiretap/internal/runset"
)

func runSetCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("wiretap run-set", flag.ContinueOnError)
	var paths checkValues
	flags.Var(&paths, "bundle", "archive to include; repeat once per package or shard")
	output := flags.String("output", "", "new run set JSON manifest")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: wiretap run-set --bundle <archive> --bundle <archive> --output <manifest.json>\nGroup captures from the same application build without changing the originals.\n\nOptions:")
		flags.PrintDefaults()
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" || len(paths) == 0 {
		return fmt.Errorf("%w: run-set requires --bundle <archive> (repeatable) and --output <manifest.json>", errUsage)
	}
	manifest, err := runset.Create(ctx, paths, *output)
	if err != nil {
		if errors.Is(err, runset.ErrOutputPath) {
			return err
		}
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	// Keep the manifest outside all input archives, including path aliases.
	inputs := make([]protectedInput, 0, len(paths))
	for _, path := range paths {
		if filepath.Base(path) == "bundle.json" {
			path = filepath.Dir(path)
		}
		root, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		inputs = append(inputs, protectedInput{root, "capture archive"})
	}
	if err := writeProtectedReport(*output, inputs, append(data, '\n')); err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, *output)
	return err
}
