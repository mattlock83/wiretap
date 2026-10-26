package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattlock83/wiretap/internal/project"
	"gopkg.in/yaml.v3"
)

func initCommand(args []string) error {
	flags := flag.NewFlagSet("wiretap init", flag.ContinueOnError)
	sutURL := flags.String("sut-url", "", "application's existing HTTP base URL")
	schema := flags.String("schema", "", "OpenAPI file path, relative to the generated configuration")
	application := flags.String("application", "", "stable application ID")
	output := flags.String("output", ".wiretap.yaml", "new project configuration path; - prints YAML to stdout")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: wiretap init --sut-url <url> --schema <file> --application <id> [--output path|-]\nCreate an HTTP project configuration without changing your tests or starting servers.")
		flags.PrintDefaults()
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sutURL) == "" || strings.TrimSpace(*schema) == "" || strings.TrimSpace(*application) == "" || *output == "" {
		return fmt.Errorf("%w: init requires --sut-url, --schema and --application; --output is a new path or -", errUsage)
	}
	parsed, err := url.Parse(*sutURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Fragment != "" {
		return fmt.Errorf("%w: --sut-url must be an absolute http or https URL without a fragment", errUsage)
	}
	// Keep the generated file deliberately small. Dependencies and source
	// metadata use the ordinary project format after initialization.
	configuration := struct {
		Version     int `yaml:"version"`
		Application struct {
			ID string `yaml:"id"`
		} `yaml:"application"`
		OutputDir string `yaml:"output_dir"`
		SUT       struct {
			ID     string `yaml:"id"`
			URL    string `yaml:"url"`
			Kind   string `yaml:"kind"`
			Schema string `yaml:"schema"`
		} `yaml:"sut"`
	}{Version: 1, OutputDir: "artifacts"}
	configuration.Application.ID = strings.TrimSpace(*application)
	configuration.SUT.ID = "sut"
	configuration.SUT.URL = *sutURL
	configuration.SUT.Kind = "service"
	configuration.SUT.Schema = *schema
	data, err := yaml.Marshal(configuration)
	if err != nil {
		return err
	}
	path := *output
	if path == "-" {
		path = ".wiretap.yaml"
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := project.Decode(data, path); err != nil {
		return fmt.Errorf("%w: generated configuration: %v", errUsage, err)
	}
	if *output == "-" {
		_, err := os.Stdout.Write(data)
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("refusing to overwrite %s; edit it directly or choose another --output", path)
	}
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		_ = os.Remove(path)
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "Created %s\nRun wiretap doctor --config %s to check the local setup.\n", path, *output)
	return err
}
