package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/phrond/evermock/internal/explore"
	"github.com/phrond/evermock/internal/project"
)

func exploreCommand(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("wiretap explore", flag.ContinueOnError)
	bundlePath := f.String("bundle", "", "source capture bundle (read-only)")
	attempt := f.String("attempt", "", "source attempt ID (required if selection is ambiguous)")
	test := f.String("test", "", "source test name")
	configPath := f.String("config", "", "project configuration containing external hook commands")
	target := f.String("target", "", "live provider http(s) origin (default: project sut.url)")
	output := f.String("output", "", "output parent directory (default: project output_dir or artifacts)")
	maxCases := f.Int("max-cases", 0, "maximum generated cases to execute (default: 100 or project setting)")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if *bundlePath == "" || len(f.Args()) > 0 || *maxCases < 0 {
		return fmt.Errorf("%w: --bundle is required; unexpected arguments and negative limits are not allowed", errUsage)
	}
	cfg, err := project.Load(*configPath)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	opts := explore.Options{Bundle: *bundlePath, Attempt: *attempt, Test: *test, Target: *target, Output: *output, Logs: os.Stderr}
	if cfg != nil {
		opts.Config = cfg.Explore
		if opts.Target == "" {
			opts.Target = cfg.SUT.URL
		}
		if opts.Output == "" {
			opts.Output = cfg.OutputDir
		}
	}
	if *maxCases > 0 {
		opts.Config.MaxCases = *maxCases
	}
	if opts.Target == "" {
		return fmt.Errorf("%w: --target or project sut.url is required", errUsage)
	}
	result, err := explore.Run(ctx, opts)
	if result.Bundle != "" {
		fmt.Println(result.Bundle)
		fmt.Fprintf(os.Stderr, "Rejection cases: %d generated, %d executed, %d passed, %d failed\n", result.Generated, result.Executed, result.Passed, result.Failed)
	}
	if err != nil {
		return err
	}
	if result.Failed > 0 {
		return fmt.Errorf("%d generated rejection cases failed", result.Failed)
	}
	return nil
}
