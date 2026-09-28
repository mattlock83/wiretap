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
	for _, args := range [][]string{{"--help"}, {"ca", "--help"}, {"report", "--help"}, {"export", "--help"}} {
		if code := execute(context.Background(), args); code != 0 {
			t.Fatalf("execute(%q) = %d", args, code)
		}
	}
}

func TestCLIRejectsWorkflowExport(t *testing.T) {
	for _, command := range []string{"report", "export"} {
		if code := execute(context.Background(), []string{command, "--bundle", t.TempDir(), "--format", "workflow"}); code != 2 {
			t.Fatalf("%s accepted removed export format: exit %d", command, code)
		}
	}
}
