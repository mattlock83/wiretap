package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRejectsTestOrchestration(t *testing.T) {
	for _, args := range [][]string{{"explore"}, {"run", "--", "npm", "test"}, {"serve"}, {"publish"}, {"check"}, {"validate"}, {"--", "npm", "test"}, {"ca", "unexpected"}} {
		if code := execute(context.Background(), args); code != 2 {
			t.Fatalf("execute(%q) = %d, want usage error", args, code)
		}
	}
}

func TestCLIHelp(t *testing.T) {
	for _, command := range []string{"", "ca", "report", "check", "run-set", "version"} {
		args := []string{"--help"}
		if command != "" {
			args = append([]string{command}, args...)
		}
		code, out, diagnostic := executeCapturingStreams(t, args)
		if code != 0 || !strings.Contains(out+diagnostic, "Usage: wiretap "+command) {
			t.Fatalf("execute(%q) = %d, stdout %q, stderr %q", args, code, out, diagnostic)
		}
	}
}

func TestCLIFlagErrorsArePrintedOnceOnStderr(t *testing.T) {
	for _, prefix := range [][]string{nil, {"ca"}, {"report"}, {"check"}, {"run-set"}, {"version"}, {"--version"}} {
		args := append(append([]string{}, prefix...), "--not-a-flag")
		code, out, diagnostic := executeCapturingStreams(t, args)
		if code != 2 || out != "" || strings.Count(diagnostic, "flag provided but not defined: -not-a-flag") != 1 || strings.Contains(diagnostic, "Usage:") || !strings.Contains(diagnostic, "--help for usage") {
			t.Fatalf("execute(%q) = %d, stdout %q, stderr %q", args, code, out, diagnostic)
		}
	}
	for _, args := range [][]string{{"--config"}, {"report", "--bundle"}, {"check", "--consumer"}, {"run-set", "--bundle"}, {"version", "--json=bad"}} {
		code, out, diagnostic := executeCapturingStreams(t, args)
		if code != 2 || out != "" || strings.Count(diagnostic, "usage:") != 1 || strings.Contains(diagnostic, "Usage:") {
			t.Fatalf("execute(%q) = %d, stdout %q, stderr %q", args, code, out, diagnostic)
		}
	}
}

func TestCLIMissingInputsUseExitTwo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for _, args := range [][]string{
		{"report"}, {"run-set"},
		{"--config", missing}, {"ca", "--config", missing},
		{"report", "--bundle", missing},
		{"report", "--bundle", missing, "--format", "postman"},
		{"report", "--bundle", missing, "--format", "all"},
		{"report", "--run-set", missing},
		{"run-set", "--bundle", missing, "--output", filepath.Join(t.TempDir(), "set.json")},
		{"check", "--consumer", missing, "--provider", missing},
	} {
		code, out, diagnostic := executeCapturingStreams(t, args)
		if code != 2 || out != "" || diagnostic == "" {
			t.Fatalf("execute(%q) = %d, stdout %q, stderr %q", args, code, out, diagnostic)
		}
	}
}

func TestCLIOutputFailuresRemainExitOne(t *testing.T) {
	archive := httpBundleFixture(t)
	output := filepath.Join(t.TempDir(), "absent", "report.json")
	code, out, diagnostic := executeCapturingStreams(t, []string{"report", "--bundle", archive, "--output", output})
	if code != 1 || out != "" || diagnostic == "" {
		t.Fatalf("output failure = %d, stdout %q, stderr %q", code, out, diagnostic)
	}
}

func TestCLICancellationIsAnOperationalFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, args := range [][]string{
		{"run-set", "--bundle", "capture", "--output", "set.json"},
		{"check", "--consumer-set", "consumers.json", "--provider-set", "providers.json"},
	} {
		if code := execute(ctx, args); code != 1 {
			t.Fatalf("cancelled execute(%q) = %d, want 1", args, code)
		}
	}
}

func executeCapturingStreams(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	files := make([]*os.File, 2)
	for i := range files {
		file, err := os.CreateTemp(t.TempDir(), "cli-output-*")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		files[i] = file
	}
	out, diagnostic := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = files[0], files[1]
	defer func() { os.Stdout, os.Stderr = out, diagnostic }()
	code := execute(t.Context(), args)
	read := func(file *os.File) string {
		data, err := os.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	return code, read(files[0]), read(files[1])
}

func TestCLIRejectsRemovedExportFormats(t *testing.T) {

	for _, format := range []string{"workflow", "openapi", "okf", "postman", "yaml"} {
		if code := execute(context.Background(), []string{"report", "--bundle", t.TempDir(), "--format", format}); code != 2 {
			t.Fatalf("%s accepted removed %s export format: exit %d", "report", format, code)
		}
	}

}

func TestCLIRejectsRemovedCommandsAndFlags(t *testing.T) {
	for _, args := range [][]string{
		{"export", "--help"}, {"compare", "--help"},
		{"init", "--help"}, {"doctor", "--help"},
		{"report", "--bundle", "capture", "--policy", "policy.json"},
		{"check", "consumer", "--consumer", "a", "--provider", "b"},
		{"check", "provider", "--consumer", "a", "--provider", "b"},
		{"check", "--consumer", "a", "--artifacts", "store"},
		{"check", "--consumer", "a", "--provider", "b", "--format", "markdown"},
		{"check", "--consumer", "a", "--provider", "b", "--format", "junit"},
	} {
		if code, output, _ := executeCapturingStreams(t, args); code != 2 || output != "" {
			t.Fatalf("removed feature %v: exit %d, stdout %q", args, code, output)
		}
	}
}
