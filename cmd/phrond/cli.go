package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/phrond/evermock/core"
)

type cliConfig struct {
	printCA                  bool
	standalone               bool
	standaloneTemplatesRaw   string
	standaloneScenarioHeader string
	standaloneStackSize      int
	standaloneTemplateFiles  []string
}

func parseCLI(cfg *core.Config) cliConfig {
	var cli cliConfig
	flag.BoolVar(&cli.printCA, "print-ca", false, "print proxy CA certificate and exit")
	flag.BoolVar(&cli.standalone, "standalone", false, "start standalone mode with template-backed mock responses")
	flag.StringVar(&cli.standaloneTemplatesRaw, "standalone-templates", "", "comma-separated template file or directory paths for standalone mode")
	flag.StringVar(&cli.standaloneScenarioHeader, "standalone-scenario-header", "X-Phrond-Scenario", "header name used to select scenario-specific standalone templates")
	flag.IntVar(&cli.standaloneStackSize, "standalone-stack-size", 100, "maximum number of standalone templates to load")
	flag.Parse()

	if v := os.Getenv("PHROND_ARTIFACT_DIR"); v != "" {
		cfg.Artifact.OutputDir = v
	}
	cli.standaloneTemplateFiles = splitCSV(cli.standaloneTemplatesRaw)

	return cli
}

func handleCLI(cli cliConfig, cfg core.Config) bool {
	if !cli.printCA {
		return false
	}
	fmt.Println("Proxy CA is stored under:")
	if cfg.Proxy.CABaseDir != "" {
		fmt.Println(cfg.Proxy.CABaseDir)
	} else {
		fmt.Println("<default cache dir>")
	}
	return true
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
