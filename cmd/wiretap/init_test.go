package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/project"
)

func TestInitCreatesLoadableConfigWithoutOtherFiles(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	data, err := setupCommandOutput(t, func() error {
		return initCommand([]string{"--sut-url", "http://127.0.0.1:8080/api", "--schema", "schemas/orders.yaml", "--application", "orders"})
	})
	if err != nil {
		t.Fatalf("init: %v\n%s", err, data)
	}
	configured, err := project.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if configured == nil || configured.Application.ID != "orders" || configured.SUT.URL != "http://127.0.0.1:8080/api" || configured.SUT.Schema != filepath.Join(root, "schemas", "orders.yaml") || !configured.Outputs.Arazzo || len(configured.Outputs.Coverage) != 2 {
		t.Fatalf("unexpected generated configuration: %+v", configured)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".wiretap.yaml" {
		t.Fatalf("init created more than the configuration: %v %v", entries, err)
	}
}

func TestInitStdoutIsOnlyYAML(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	data, err := setupCommandOutput(t, func() error {
		return initCommand([]string{"--sut-url", "https://example.test/api", "--schema", "api.yaml", "--application", "orders: example", "--output", "-"})
	})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := project.Decode(data, filepath.Join(root, ".wiretap.yaml"))
	if err != nil || configured.Application.ID != "orders: example" {
		t.Fatalf("stdout did not contain only loadable YAML: %v\n%s", err, data)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("stdout mode wrote files: %v %v", entries, err)
	}
}

func TestInitRefusesExistingFilesAndSymlinks(t *testing.T) {
	for _, alias := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "symlink"}[alias], func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "existing.yaml")
			if err := os.WriteFile(source, []byte("keep existing"), 0600); err != nil {
				t.Fatal(err)
			}
			output := source
			if alias {
				output = filepath.Join(root, "alias.yaml")
				if err := os.Symlink(source, output); err != nil {
					t.Skip(err)
				}
			}
			err := initCommand([]string{"--sut-url", "http://example.test", "--schema", "api.yaml", "--application", "orders", "--output", output})
			data, readErr := os.ReadFile(source)
			if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") || readErr != nil || string(data) != "keep existing" {
				t.Fatalf("existing configuration changed: %v, %v, %s", err, readErr, data)
			}
		})
	}
}

func TestSetupCommandArguments(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--sut-url", "not-a-url", "--schema", "api.yaml", "--application", "orders"},
		{"--sut-url", "http://example.test", "--schema", "api.yaml"},
		{"--unknown"},
	} {
		if err := initCommand(args); !errors.Is(err, errUsage) {
			t.Fatalf("init %q accepted invalid options: %v", args, err)
		}
	}
	if err := initCommand([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("init help: %v", err)
	}
	if err := doctorCommand(t.Context(), []string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("doctor help: %v", err)
	}
	if err := doctorCommand(t.Context(), []string{"--format", "xml"}); !errors.Is(err, errUsage) {
		t.Fatalf("doctor accepted invalid format: %v", err)
	}
}
