package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/coverage"
)

func htmlFileInputs(t *testing.T) (string, string, string) {
	t.Helper()
	root := httpBundleFixture(t)
	dir := t.TempDir()
	report, err := coverage.Generate(t.Context(), root, "Example API", false)
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err := coverage.WriteReport(&data, report, "json"); err != nil {
		t.Fatal(err)
	}
	coveragePath := filepath.Join(dir, "coverage.json")
	specPath := filepath.Join(dir, "openapi.yaml")
	arazzoPath := filepath.Join(dir, "arazzo.json")
	if err := os.WriteFile(coveragePath, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(specPath, []byte("openapi: 3.0.3\ninfo: {title: Example API, version: '1'}\npaths: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	export, err := arazzo.Generate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	data.Reset()
	if err := arazzo.Write(&data, export.Document); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(arazzoPath, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return coveragePath, specPath, arazzoPath
}

func TestHTMLReportFromArchiveAndStandaloneFiles(t *testing.T) {
	archive := httpBundleFixture(t)
	manifest, err := os.ReadFile(filepath.Join(archive, "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"report", "export"} {
		code, data := executeCapturingStdout(t, []string{command, "--bundle", archive, "--format", "html"})
		if code != 0 || !strings.Contains(string(data), `data-wiretap-report="coverage"`) || !strings.Contains(string(data), "reads items") || !strings.Contains(string(data), "Contract coverage") {
			t.Fatalf("archive HTML failed: exit %d: %s", code, data)
		}
	}
	if after, err := os.ReadFile(filepath.Join(archive, "bundle.json")); err != nil || !bytes.Equal(manifest, after) {
		t.Fatal("HTML report mutated its archive")
	}
	coveragePath, specPath, arazzoPath := htmlFileInputs(t)
	output := filepath.Join(t.TempDir(), "report.html")
	args := []string{"report", "--coverage-json", coveragePath, "--spec", specPath, "--arazzo", arazzoPath, "--format", "html", "--output", output}
	if code := execute(context.Background(), args); code != 0 {
		t.Fatalf("standalone HTML failed: exit %d", code)
	}
	data, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(data), "Example API") || !strings.Contains(string(data), "openapi.yaml") || !strings.Contains(string(data), "Arazzo") || !strings.Contains(string(data), "missing item") {
		t.Fatalf("standalone HTML omitted sources or test evidence: %v", err)
	}
}

func TestStandaloneHTMLProtectsEveryInputAndAliases(t *testing.T) {
	coveragePath, specPath, arazzoPath := htmlFileInputs(t)
	for _, source := range []string{coveragePath, specPath, arazzoPath} {
		before, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, alias := range []string{"direct", "hardlink", "symlink"} {
			t.Run(filepath.Base(source)+"/"+alias, func(t *testing.T) {
				output := source
				if alias != "direct" {
					output = filepath.Join(t.TempDir(), "report.html")
					link := os.Link
					if alias == "symlink" {
						link = os.Symlink
					}
					if err := link(source, output); err != nil {
						t.Skip(err)
					}
				}
				args := []string{"report", "--coverage-json", coveragePath, "--spec", specPath, "--arazzo", arazzoPath, "--format", "html", "--output", output}
				if code := execute(t.Context(), args); code != 2 {
					t.Fatalf("input overwrite was not rejected: exit %d", code)
				}
				if after, err := os.ReadFile(source); err != nil || !bytes.Equal(before, after) {
					t.Fatal("HTML output changed its source")
				}
			})
		}
	}
}

func TestStandaloneHTMLRejectsMixedModesAndPreservesOldOutput(t *testing.T) {
	coveragePath, specPath, _ := htmlFileInputs(t)
	for _, args := range [][]string{
		{"report", "--coverage-json", coveragePath},
		{"report", "--spec", specPath, "--format", "html"},
		{"report", "--coverage-json", coveragePath, "--bundle", httpBundleFixture(t), "--format", "html"},
		{"report", "--coverage-json", coveragePath, "--run-set", "set.json", "--format", "html"},
	} {
		if code, _ := executeCapturingStdout(t, args); code != 2 {
			t.Fatalf("mixed inputs accepted: exit %d: %v", code, args)
		}
	}
	output := filepath.Join(t.TempDir(), "report.html")
	if err := os.WriteFile(output, []byte("keep old report"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coveragePath, []byte(`{"report_version":"1.0","report_version":"1.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := execute(t.Context(), []string{"report", "--coverage-json", coveragePath, "--format", "html", "--output", output}); code != 2 {
		t.Fatalf("malformed coverage input accepted: exit %d", code)
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "keep old report" {
		t.Fatal("failed HTML regeneration replaced old report")
	}
}
