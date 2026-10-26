package main

import (
	"github.com/mattlock83/wiretap/internal/core"
	"github.com/mattlock83/wiretap/internal/project"
)

// resolveRuntimeConfig gives startup, CA access and diagnostics the same
// precedence: defaults, project configuration, then process environment.
func resolveRuntimeConfig(project *project.Config) (core.Config, error) {
	config := core.DefaultConfig()
	if project != nil {
		if project.HTTPListen != "" {
			config.HTTP.ListenAddr = project.HTTPListen
		}
		if project.ProxyListen != "" {
			config.Proxy.ListenAddr = project.ProxyListen
		}
		config.Artifact.OutputDir = project.OutputDir
		config.Proxy.CABaseDir = project.CADir
	}
	if err := applyEnv(&config); err != nil {
		return config, err
	}
	return config, config.Validate()
}
