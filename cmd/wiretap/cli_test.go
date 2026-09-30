package main

import (
	"context"
	"testing"
)

func TestCLIRejectsTestOrchestration(t *testing.T) {
	for _, args := range [][]string{{"run", "--", "npm", "test"}, {"serve"}, {"--", "npm", "test"}, {"ca", "unexpected"}} {
		if code := execute(context.Background(), args); code != 2 {
			t.Fatalf("execute(%q) = %d, want usage error", args, code)
		}
	}
}

func TestCLIHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"ca", "--help"}, {"report", "--help"}, {"export", "--help"}, {"explore", "--help"}} {
		if code := execute(context.Background(), args); code != 0 {
			t.Fatalf("execute(%q) = %d", args, code)
		}
	}
}

func TestExploreCLIRequiresBundleAndTarget(t *testing.T) {
	for _, args := range [][]string{{"explore"}, {"explore", "--bundle", t.TempDir(), "--max-cases", "-1"}} {
		if code := execute(context.Background(), args); code != 2 {
			t.Fatalf("execute(%q) = %d", args, code)
		}
	}
}

func TestCLIRejectsRemovedExportFormats(t *testing.T) {
	for _, command := range []string{"report", "export"} {
		for _, format := range []string{"workflow", "openapi"} {
			if code := execute(context.Background(), []string{command, "--bundle", t.TempDir(), "--format", format}); code != 2 {
				t.Fatalf("%s accepted removed %s export format: exit %d", command, format, code)
			}
		}
	}
}
