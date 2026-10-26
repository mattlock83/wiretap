package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/assumptions"
)

func cliWriteCheckArazzo(t *testing.T, archive, directory string) (string, []string) {
	t.Helper()
	export, err := arazzo.Generate(archive, "")
	if err != nil || export.Document == nil {
		t.Fatalf("generate Arazzo fixture: %v", err)
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err := arazzo.Write(&data, export.Document); err != nil {
		t.Fatal(err)
	}
	document := filepath.Join(directory, "arazzo.json")
	if err := os.WriteFile(document, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var companions []string
	for _, file := range export.Files {
		path := filepath.Join(directory, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, file.Data, 0600); err != nil {
			t.Fatal(err)
		}
		companions = append(companions, path)
	}
	return document, companions
}

func cliCheckArazzoArchive(t *testing.T, consumer bool, response string) string {
	t.Helper()
	archive := cliCheckArchive(t, consumer, "/items?limit=1", 200, response)
	cliWriteCheckArazzo(t, archive, filepath.Join(archive, "exports", "arazzo"))
	return archive
}

func TestCLIArazzoCheckUsesCompleteResponseEvidence(t *testing.T) {
	consumer := cliCheckArazzoArchive(t, true, `{"stock":3}`)
	for _, scenario := range []struct {
		name, response string
		exit           int
	}{
		{"compatible", `{"stock":3.0}`, 0},
		{"response mismatch despite same success criterion", `{"stock":"three"}`, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			provider := cliCheckArazzoArchive(t, false, scenario.response)

			args := []string{"check", "--consumer", filepath.Join(consumer, "bundle.json"), "--provider", provider}
			code, data := executeCapturingStdout(t, append(args, "--format", "json"))
			var report assumptions.Report
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatalf("invalid JSON report: %v\n%s", err, data)
			}
			if code != scenario.exit || report.Compatible != (scenario.exit == 0) || report.Counts.Assumptions != 1 {
				t.Fatalf("check exit %d: %s", code, data)
			}
			for _, source := range []assumptions.Source{report.Consumer, report.Provider} {
				if source.Arazzo == "" || len(source.ArazzoFiles) == 0 {
					t.Fatalf("missing Arazzo provenance: %+v", source)
				}
			}
			if scenario.exit == 0 && report.Counts.Verified != 1 || scenario.exit == 1 && (report.Counts.Mismatched != 1 || len(report.Findings) != 1 || report.Findings[0].Code != "provider_response_mismatch") {
				t.Fatalf("unexpected evidence verdict: %s", data)
			}
			code, data = executeCapturingStdout(t, append(args, "--format", "text"))
			if code != scenario.exit || len(data) == 0 {
				t.Fatalf("text report exit %d: %s", code, data)
			}

		})
	}
}

func TestCLIArazzoCheckExplicitFileAndDirectoryOverrides(t *testing.T) {
	consumer := cliCheckArchive(t, true, "/items?limit=1", 200, `{"stock":3}`)
	provider := cliCheckArchive(t, false, "/items?limit=1", 200, `{"stock":3}`)
	consumerDocument, _ := cliWriteCheckArazzo(t, consumer, t.TempDir())
	providerDirectory := t.TempDir()
	providerDocument, _ := cliWriteCheckArazzo(t, provider, providerDirectory)
	args := []string{"check", "--consumer", consumer, "--provider", provider, "--consumer-arazzo", consumerDocument, "--provider-arazzo", providerDirectory, "--format", "json"}
	code, data := executeCapturingStdout(t, args)
	var report assumptions.Report
	if err := json.Unmarshal(data, &report); err != nil || code != 0 || !report.Compatible || report.Consumer.Arazzo != consumerDocument || report.Provider.Arazzo != providerDocument {
		t.Fatalf("custom exports exit %d: %s, %v", code, data, err)
	}
}

func TestCLIArazzoCheckRejectsInvalidInputsWithoutReplacingReport(t *testing.T) {
	for _, scenario := range []string{"missing document", "invalid document", "missing companion", "changed companion"} {
		t.Run(scenario, func(t *testing.T) {
			consumer := cliCheckArazzoArchive(t, true, `{"stock":3}`)
			provider := cliCheckArazzoArchive(t, false, `{"stock":3}`)
			document, companions := cliWriteCheckArazzo(t, provider, t.TempDir())
			switch scenario {
			case "missing document":
				if err := os.Remove(document); err != nil {
					t.Fatal(err)
				}
			case "invalid document":
				if err := os.WriteFile(document, []byte(`{"arazzo":"1.0.1","workflows":[]}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing companion":
				if err := os.Remove(companions[0]); err != nil {
					t.Fatal(err)
				}
			case "changed companion":
				if err := os.WriteFile(companions[0], []byte(`{"openapi":"3.0.3","paths":{}}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(t.TempDir(), "report.json")
			if err := os.WriteFile(output, []byte("keep previous report"), 0600); err != nil {
				t.Fatal(err)
			}
			code, stdout := executeCapturingStdout(t, []string{"check", "--consumer", consumer, "--provider", provider, "--provider-arazzo", document, "--format", "json", "--output", output})
			data, err := os.ReadFile(output)
			if code != 2 || len(stdout) != 0 || err != nil || string(data) != "keep previous report" {
				t.Fatalf("invalid export exit %d, stdout %s, report %s, error %v", code, stdout, data, err)
			}
		})
	}
}

func TestCLIArazzoCheckRejectsRemovedFlagsAndDiscoveryOverrides(t *testing.T) {
	for _, flags := range [][]string{
		{"--consumer-collection", "c.json"}, {"--provider-collection", "p.json"},
		{"--input-format", "arazzo"}, {"--artifacts", "root", "--consumer-arazzo", "c.json"},
		{"--consumer-set", "c.json", "--provider-set", "p.json", "--provider-arazzo", "p.json"},
	} {
		args := append([]string{"consumer", "--consumer", "consumer", "--provider", "provider"}, flags...)
		code, err := checkCommand(context.Background(), args)
		if code != 2 || !errors.Is(err, errUsage) {
			t.Fatalf("invalid inputs %v: exit %d, error %v", flags, code, err)
		}
	}
}

func TestCLIArazzoCheckProtectsExternalExportInputs(t *testing.T) {
	for _, state := range []string{"complete", "interrupted"} {
		t.Run(state, func(t *testing.T) {
			consumer := cliCheckArazzoArchive(t, true, `{"stock":3}`)
			provider := cliCheckArchive(t, false, "/items?limit=1", 200, `{"stock":3}`, state)
			directory := t.TempDir()
			document, companions := cliWriteCheckArazzo(t, provider, directory)
			args := []string{"check", "--consumer", consumer, "--provider", provider, "--provider-arazzo", document, "--format", "json"}
			paths := append([]string{document, filepath.Join(directory, "new-report.json")}, companions...)
			alias := filepath.Join(t.TempDir(), "exports")
			if err := os.Symlink(directory, alias); err == nil {
				paths = append(paths, filepath.Join(alias, "new-report.json"))
			}
			if state == "complete" {
				hardlink := filepath.Join(t.TempDir(), "companion.json")
				if err := os.Link(companions[0], hardlink); err == nil {
					paths = append(paths, hardlink)
				}
			}
			for _, output := range paths {
				before, beforeErr := os.ReadFile(output)
				code := execute(context.Background(), append(args, "--output", output))
				after, afterErr := os.ReadFile(output)
				if code != 2 || !bytes.Equal(before, after) || errors.Is(beforeErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist) {
					t.Fatalf("input destination %s: exit %d, changed input or created report", output, code)
				}
			}
		})
	}
}

func TestCLIArazzoCheckSupportsRunSets(t *testing.T) {
	consumer := cliCheckArchive(t, true, "/items", 200, `{"stock":3}`)
	provider := cliCheckArchive(t, false, "/items", 200, `{"stock":3}`)
	sets := make([]string, 2)
	for index, archive := range []string{consumer, provider} {
		sets[index] = filepath.Join(t.TempDir(), "run-set.json")
		if code := execute(context.Background(), []string{"run-set", "--bundle", archive, "--output", sets[index]}); code != 0 {
			t.Fatalf("create run set: exit %d", code)
		}
	}
	code, data := executeCapturingStdout(t, []string{"check", "--consumer-set", sets[0], "--provider-set", sets[1], "--format", "json"})
	var report assumptions.SetReport
	if err := json.Unmarshal(data, &report); err != nil || code != 0 || !report.Compatible || report.Counts.Verified != 1 || len(report.Consumers) != 1 || len(report.Providers) != 1 {
		t.Fatalf("run set check exit %d: %s, %v", code, data, err)
	}
}
