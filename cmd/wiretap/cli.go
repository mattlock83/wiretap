package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mattlock83/wiretap/internal/app"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/project"
	wiretaptls "github.com/mattlock83/wiretap/internal/tls"
)

var errUsage = errors.New("usage")

// parseCommandFlags leaves diagnostics to execute so flag failures are printed
// once. Help still uses the command's own usage text and output stream.
func parseCommandFlags(flags *flag.FlagSet, args []string) error {
	output, usage := flags.Output(), flags.Usage
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	err := flags.Parse(args)
	flags.SetOutput(output)
	flags.Usage = usage
	if errors.Is(err, flag.ErrHelp) {
		usage()
		return err
	}
	if err != nil {
		return fmt.Errorf("%w: %v\nRun %s --help for usage.", errUsage, err, flags.Name())
	}
	return nil
}

func execute(ctx context.Context, args []string) int {
	command := ""
	if len(args) > 0 && args[0] == "--version" {
		command, args = "version", args[1:]
	} else if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		command, args = args[0], args[1:]
	}
	var err error
	switch command {
	case "", "ca":
		err = runtimeCommand(ctx, command, args)
	case "version":
		err = versionCommand(args, os.Stdout)
	case "report":
		err = reportCommand(args)
	case "check":
		code, err := checkCommand(ctx, args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return code
	case "run-set":
		err = runSetCommand(ctx, args)
	case "doctor":
		err = doctorCommand(ctx, args)
	case "init":
		err = initCommand(args)
	default:
		err = fmt.Errorf("%w: unknown command %q; run wiretap --help for commands", errUsage, command)
	}
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintln(os.Stderr, err)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 1
	}
	if errors.Is(err, errUsage) || errors.Is(err, bundle.ErrInput) {
		return 2
	}
	return 1
}

func runtimeCommand(ctx context.Context, command string, args []string) error {
	name := "wiretap"
	if command != "" {
		name += " " + command
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	configPath := flags.String("config", "", "project configuration (default: discover .wiretap.yaml)")
	flags.Usage = func() {
		if command == "ca" {
			fmt.Fprintln(flags.Output(), "Usage: wiretap ca [--config path]\nPrint the capture CA certificate as PEM.")
		} else {
			fmt.Fprintln(flags.Output(), `Usage: wiretap [--config path]
Start the capture API; run tests with your existing test framework.

Commands:
  init       Create a project configuration
  doctor     Check local capture setup
  report     Generate coverage reports and Arazzo exports
  check      Check consumer/provider compatibility
  run-set    Group captures from one application build
  version    Print the Wiretap version
  ca         Print the capture CA certificate

Use wiretap <command> --help for options and examples.

Capture options:`)
		}
		flags.PrintDefaults()
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if len(flags.Args()) > 0 {
		return fmt.Errorf("%w: unexpected arguments: Wiretap hosts the HTTP API; run tests with your existing test framework", errUsage)
	}
	projectConfig, err := project.Load(*configPath)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	cfg, err := resolveRuntimeConfig(projectConfig)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if command == "ca" {
		manager := wiretaptls.NewManager(cfg.Proxy.CABaseDir)
		pem, err := manager.CACertPEM()
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(pem)
		return err
	}
	instance, err := app.New(app.Deps{Config: cfg, Project: projectConfig})
	if err != nil {
		return err
	}
	if err := instance.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
