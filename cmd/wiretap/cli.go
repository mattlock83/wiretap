package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/mattlock83/wiretap/internal/app"
	"github.com/mattlock83/wiretap/internal/core"
	"github.com/mattlock83/wiretap/internal/project"
	wiretaptls "github.com/mattlock83/wiretap/internal/tls"
)

func execute(ctx context.Context, args []string) int {
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		if err := versionCommand(args[1:], os.Stdout); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		return 0
	}
	command := ""
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		command = args[0]
		args = args[1:]
	}
	if command == "report" || command == "export" {
		err := reportCommand(args)
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintln(os.Stderr, err)
			if errors.Is(err, errUsage) {
				return 2
			}
			return 1
		}
		return 0
	}
	if command == "check" {
		code, err := checkCommand(ctx, args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return code
	}
	if command != "" && command != "ca" {
		fmt.Fprintf(os.Stderr, "unknown command %q; use wiretap, report, export, check, version, or ca\n", command)
		return 2
	}
	flags := flag.NewFlagSet("wiretap "+command, flag.ContinueOnError)
	configPath := flags.String("config", "", "project configuration (default: discover .wiretap.yaml)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: wiretap [--config path]\n  wiretap ca [--config path]\n  wiretap version [--json]\n  wiretap report --bundle <directory> [--format json|yaml|text|all]\n  wiretap export --bundle <directory> --format json --output coverage.json\n  wiretap export --bundle <directory> --format postman --output-dir postman\n  wiretap check consumer --consumer <capture> --artifacts <directory>\n  wiretap check provider --provider <capture> --artifacts <directory>\n  wiretap check <consumer|provider> --consumer <capture> --provider <capture> [--dependency <id>]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(flags.Args()) > 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments: Wiretap hosts the HTTP API; run tests with your existing test framework")
		return 2
	}
	cfg := core.DefaultConfig()
	projectConfig, err := project.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if projectConfig != nil {
		if projectConfig.HTTPListen != "" {
			cfg.HTTP.ListenAddr = projectConfig.HTTPListen
		}
		if projectConfig.ProxyListen != "" {
			cfg.Proxy.ListenAddr = projectConfig.ProxyListen
		}
		cfg.Artifact.OutputDir = projectConfig.OutputDir
		cfg.Proxy.CABaseDir = projectConfig.CADir
	}
	if err := applyEnv(&cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if command == "ca" {
		manager := wiretaptls.NewManager(cfg.Proxy.CABaseDir)
		pem, err := manager.CACertPEM()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_, err = os.Stdout.Write(pem)
		if err != nil {
			return 1
		}
		return 0
	}
	instance, err := app.New(app.Deps{Config: cfg, Project: projectConfig})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := instance.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
