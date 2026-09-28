package main

import (
	"flag"
	"fmt"

	"github.com/phrond/evermock/internal/core"
)

type cliConfig struct {
	printCA bool
}

func parseCLI(cfg *core.Config) cliConfig {
	var cli cliConfig
	flag.BoolVar(&cli.printCA, "print-ca", false, "print proxy CA certificate and exit")
	flag.Parse()

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
