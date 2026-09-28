// Package bundlereport renders disposable OKF views from canonical bundle data.
package bundlereport

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
	"github.com/phrond/evermock/internal/coverage"
	"gopkg.in/yaml.v3"
)

// Refresh regenerates only Phrond-owned paths. Evidence and exports remain
// usable if this operation fails or all Markdown files are removed.
func Refresh(root string) (*coverage.Report, error) {
	return render(root, false)
}

// Rebuild regenerates every owned page against one inventory snapshot.
func Rebuild(root string) (*coverage.Report, error) {
	report, err := render(root, true)
	if err != nil {
		return nil, err
	}
	if err := Validate(root); err != nil {
		return nil, err
	}
	return report, nil
}

func render(root string, force bool) (*coverage.Report, error) {
	run, captures, err := bundle.Load(root)
	if err != nil {
		return nil, err
	}
	report, err := coverage.GenerateBundleSnapshot(root, run, captures, "", false)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	generatedAt := run.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	base := map[string]any{"profile_version": "1.0", "run_id": run.RunID, "snapshot_revision": run.Revision}
	write := func(rel, kind, title, description, body string, extra map[string]any) error {
		phrond := map[string]any{}
		for k, v := range base {
			phrond[k] = v
		}
		phrond["kind"] = kind
		for k, v := range extra {
			phrond[k] = v
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		if !force && unchangedPage(path, kind, title, description, extra, body) {
			return nil
		}
		front := map[string]any{"type": title, "title": title, "description": description, "generated": map[string]string{"by": "phrond/1.0", "at": generatedAt}, "phrond": phrond}
		if source, ok := extra["evidence"].(string); ok {
			relative, err := filepath.Rel(filepath.Dir(path), filepath.Join(root, filepath.FromSlash(source)))
			if err != nil {
				return err
			}
			front["sources"] = []map[string]string{{"id": "capture", "resource": filepath.ToSlash(relative)}}
		} else if refs, ok := extra["evidence"].([]map[string]string); ok {
			var sources []map[string]string
			seen := map[string]bool{}
			for _, ref := range refs {
				if seen[ref["path"]] {
					continue
				}
				seen[ref["path"]] = true
				relative, err := filepath.Rel(filepath.Dir(path), filepath.Join(root, filepath.FromSlash(ref["path"])))
				if err != nil {
					return err
				}
				sources = append(sources, map[string]string{"resource": filepath.ToSlash(relative)})
			}
			if len(sources) > 0 {
				front["sources"] = sources
			}
		}
		data, err := yaml.Marshal(front)
		if err != nil {
			return err
		}
		return writePage(path, []byte("---\n"+string(data)+"---\n\n"+body))
	}
	index := "---\nokf_version: \"0.2\"\n---\n\n# Phrond capture\n\n" +
		"- [Interactive report](viewer/index.html)\n- [Run](run.md)\n- [Coverage](coverage.md)\n- [Provided APIs](apis/index.md)\n- [Dependencies](dependencies/index.md)\n- [Tests](tests/index.md)\n"
	if err := writePage(filepath.Join(root, "index.md"), []byte(index)); err != nil {
		return nil, err
	}
	runBody := fmt.Sprintf("# Run %s\n\nState: **%s**. Revision: %d. Started: %s. Attempts: %d committed, %d pending.\n\n[Canonical run inventory](evidence/run.json)\n\n", escape(run.RunID), escape(run.RunState), run.Revision, run.StartedAt.Format("2006-01-02T15:04:05Z07:00"), len(run.Attempts), len(run.Pending))
	for _, pending := range run.Pending {
		runBody += fmt.Sprintf("- Pending: %s (%s)\n", escape(pending.Name), escape(pending.AttemptID))
	}
	if err := write("run.md", "run", "Capture Run", "Capture lifecycle and provenance", runBody, map[string]any{"evidence": "evidence/run.json"}); err != nil {
		return nil, err
	}
	coverageBody := fmt.Sprintf("# API operation coverage\n\nRun state: **%s**. Attempts: %d. Counts below describe observed operations, including failed attempts. A passing observation requires a complete passing attempt. These are API operation counts, not code or scenario coverage.\n\n", escape(run.RunState), len(run.Attempts))
	for _, group := range []struct {
		title string
		specs []coverage.SpecCoverage
	}{{"Provided APIs", report.Provided.Specs}, {"Dependencies", report.Dependent.Specs}} {
		coverageBody += "## " + group.title + "\n\n"
		if len(group.specs) == 0 {
			coverageBody += "No supplied schemas. Denominator unknown.\n\n"
		}
		for _, spec := range group.specs {
			denominator := len(spec.Exercised) + len(spec.NotCovered)
			passing := 0
			for _, op := range spec.Exercised {
				if passingObservation(op.Samples) {
					passing++
				}
			}
			if spec.DenominatorKnown {
				coverageBody += fmt.Sprintf("- %s: %d of %d declared operations observed; %d in a passing, complete attempt; %d unobserved.\n", escape(spec.AssetName), len(spec.Exercised), denominator, passing, len(spec.NotCovered))
			} else {
				coverageBody += fmt.Sprintf("- %s: declared operation denominator unknown; %d captured flows matched to a schema.\n", escape(spec.AssetName), len(spec.Exercised))
			}
		}
		coverageBody += "\n"
	}
	if len(report.Notes) > 0 {
		coverageBody += "## Diagnostics\n\n"
		for _, note := range report.Notes {
			coverageBody += "- " + escape(note) + "\n"
		}
	}
	if err := write("coverage.md", "coverage", "Coverage Summary", "Observed API operation counts and their limits", coverageBody, map[string]any{"evidence": "evidence/run.json"}); err != nil {
		return nil, err
	}

	byAttempt := map[string]*artifact.Artifact{}
	attemptPaths := map[string]string{}
	for _, ref := range run.Attempts {
		attemptPaths[ref.AttemptID] = ref.Path
	}
	for _, capture := range captures {
		byAttempt[capture.TestCase.ID] = capture
	}
	var tests strings.Builder
	tests.WriteString("# Test attempts\n\n")
	for _, ref := range run.Attempts {
		capture := byAttempt[ref.AttemptID]
		if capture == nil {
			return nil, fmt.Errorf("missing committed attempt %s", ref.AttemptID)
		}
		tests.WriteString(fmt.Sprintf("- [%s](%s.md): %s; %d interactions\n", escape(capture.TestCase.Name), ref.AttemptID, escape(capture.TestCase.Status), len(capture.Traffic)))
		if !force && currentAttemptPage(filepath.Join(root, "tests", ref.AttemptID+".md"), run.RunID, ref.AttemptID, ref.Path) {
			continue
		}
		var page strings.Builder
		page.WriteString("# " + escape(capture.TestCase.Name) + "\n\n")
		page.WriteString(fmt.Sprintf("Attempt `%s`; result **%s**; %d interactions.\n\n", ref.AttemptID, escape(capture.TestCase.Status), len(capture.Traffic)))
		if capture.TestCase.TraceID != "" {
			page.WriteString("Test trace: `" + escape(capture.TestCase.TraceID) + "`; root span: `" + escape(capture.TestCase.RootSpanID) + "`.\n\n")
		}
		if len(capture.TestCase.Tags) > 0 {
			page.WriteString("Tags: " + escape(strings.Join(capture.TestCase.Tags, ", ")) + ".\n\n")
		}
		if capture.CaptureIncomplete {
			page.WriteString("**Capture incomplete.**\n\n")
		}
		for _, diagnostic := range capture.CaptureDiagnostics {
			page.WriteString("- Capture diagnostic: " + escape(diagnostic) + "\n")
		}
		if capture.SchemaValidation != nil {
			page.WriteString("\nSchema validation: **" + escape(capture.SchemaValidation.Status) + "**.\n\n")
			for _, issue := range capture.SchemaValidation.Errors {
				page.WriteString("- " + escape(issue.Message) + "\n")
			}
		}
		page.WriteString("\n[Canonical attempt JSON](../" + ref.Path + ")\n\n")
		for _, flow := range capture.Traffic {
			page.WriteString(fmt.Sprintf("## Flow %s\n\n", escape(flow.ID)))
			page.WriteString(fmt.Sprintf("Channel: `%s`; trace: `%s`; correlation: `%s`; parent: `%s`.\n\n", escape(flow.Channel), escape(flow.TraceID), escape(flow.TestCorrelation), escape(flow.ParentFlowID)))
			if flow.TransportError != "" {
				page.WriteString("Transport error: " + escape(flow.TransportError) + "\n\n")
			}
			for _, part := range []struct {
				name string
				msg  artifact.Message
			}{{"Request", flow.Request}, {"Response", flow.Response}} {
				if part.msg.HTTP == nil {
					continue
				}
				m := part.msg.HTTP
				page.WriteString("### " + part.name + "\n\n")
				if part.name == "Request" {
					page.WriteString(fmt.Sprintf("`%s %s`\n\n", escape(m.Method), escape(m.URL)))
				} else {
					page.WriteString(fmt.Sprintf("Status: **%d**\n\n", m.Status))
				}
				for _, name := range sortedHeaderNames(m.Headers) {
					for _, value := range m.Headers[name] {
						page.WriteString("- " + escape(name) + ": " + escape(value) + "\n")
					}
				}
				for _, name := range sortedHeaderNames(m.Trailers) {
					for _, value := range m.Trailers[name] {
						page.WriteString("- Trailer " + escape(name) + ": " + escape(value) + "\n")
					}
				}
				if m.Body != nil {
					body := m.Body
					page.WriteString("\n")
					if body.Truncated {
						page.WriteString("**Body truncated during capture.**\n\n")
					}
					var raw []byte
					if body.Bytes != "" {
						raw, _ = base64.StdEncoding.DecodeString(body.Bytes)
					} else {
						raw = []byte(body.Text)
					}
					if len(raw) > 0 {
						hash := sha256.Sum256(raw)
						page.WriteString(fmt.Sprintf("Retained body: %d bytes; [attachment](../payloads/%s.bin).\n\n", len(raw), hex.EncodeToString(hash[:])))
					}
					if body.Bytes != "" {
						page.WriteString("Binary body; inspect attachment for retained bytes.\n\n")
					} else if body.Text != "" {
						page.WriteString(preview(body.Text))
					}
				}
			}
		}
		if err := write("tests/"+ref.AttemptID+".md", "attempt", "Test Attempt", "Observed exchanges for a test attempt", page.String(), map[string]any{"attempt_id": ref.AttemptID, "evidence": ref.Path}); err != nil {
			return nil, err
		}
	}
	if err := writePage(filepath.Join(root, "tests", "index.md"), []byte(tests.String())); err != nil {
		return nil, err
	}
	for _, role := range []struct {
		dir   string
		specs []coverage.SpecCoverage
	}{{"apis", report.Provided.Specs}, {"dependencies", report.Dependent.Specs}} {
		var overview strings.Builder
		overview.WriteString("# " + strings.Title(role.dir) + "\n\n")
		for _, spec := range role.specs {
			key := shortHash(role.dir + "|" + spec.AssetID + "|" + spec.SchemaID + "|" + spec.SchemaSet)
			apiDir := role.dir + "/" + key
			name := spec.AssetName
			if name == "" {
				name = spec.SchemaID
			}
			overview.WriteString(fmt.Sprintf("- [%s](%s/api.md)\n", escape(name), key))
			ops := append([]coverage.OperationSamples{}, spec.Exercised...)
			ops = append(ops, spec.NotCovered...)
			sort.Slice(ops, func(i, j int) bool { return ops[i].Operation < ops[j].Operation })
			var apiBody strings.Builder
			apiBody.WriteString("# " + escape(name) + "\n\n")
			apiBody.WriteString(fmt.Sprintf("Role: `%s`; schema: `%s`; asset ID: `%s`; schema set: `%s`.\n\n", role.dir, escape(spec.SchemaID), escape(spec.AssetID), escape(spec.SchemaSet)))
			if role.dir == "apis" {
				kind := run.SUTTargetKind
				if kind == "" {
					kind = "unknown"
				}
				apiBody.WriteString("SUT target kind: **" + escape(kind) + "**.\n\n")
			}
			if strings.HasPrefix(spec.Source, "schemas/") {
				apiBody.WriteString("[Frozen OpenAPI schema](../../" + spec.Source + ")\n\n")
			}
			apiBody.WriteString("## Declared operations\n\n")
			for _, op := range ops {
				opKey := shortHash(op.Operation)
				apiBody.WriteString(fmt.Sprintf("- [%s](operations/%s.md): %d observed exchanges\n", escape(op.Operation), opKey, len(op.Samples)))
				var opBody strings.Builder
				opBody.WriteString("# " + escape(op.Operation) + "\n\n")
				opBody.WriteString(fmt.Sprintf("Observed exchanges: **%d**. Passing complete observation: **%t**.\n\n", len(op.Samples), passingObservation(op.Samples)))
				if role.dir == "dependencies" {
					kind := run.DependencyKinds[spec.AssetID]
					if kind == "" {
						kind = "unknown"
					}
					opBody.WriteString("Upstream kind: **" + kind + "**. These are observed dependency responses. Consumer field use and provider agreement are unknown.\n\n")
				}
				opBody.WriteString("## Evidence\n\n")
				evidence := make([]map[string]string, 0, len(op.Samples))
				for i, sample := range op.Samples {
					path := attemptPaths[sample.TestCaseID]
					if path == "" {
						return nil, fmt.Errorf("operation references unknown attempt %q", sample.TestCaseID)
					}
					evidence = append(evidence, map[string]string{"attempt_id": sample.TestCaseID, "flow_id": sample.FlowID, "path": path})
					opBody.WriteString(fmt.Sprintf("- [Attempt %s](../../../tests/%s.md), flow `%s`; [JSON](../../../%s); result `%s`\n", sample.TestCaseID, sample.TestCaseID, escape(sample.FlowID), path, escape(sample.TestStatus)))
					if i < 3 {
						opBody.WriteString("  - Example: `" + escape(sample.Request.Method) + " " + escape(sample.Request.URL) + "` → HTTP " + fmt.Sprint(sample.Response.Status) + "\n")
					}
				}
				opBody.WriteString("\nPreviews show at most three examples; the evidence list includes every observation.\n")
				kind := "operation"
				if role.dir == "dependencies" {
					kind = "assumptions"
				}
				title := "API Operation"
				if role.dir == "dependencies" {
					title = "Dependency Assumptions"
				}
				if err := write(apiDir+"/operations/"+opKey+".md", kind, title, "Observed exchanges for a declared operation", opBody.String(), map[string]any{"api_id": spec.AssetID, "operation": op.Operation, "schema_id": spec.SchemaID, "schema_set": spec.SchemaSet, "evidence": evidence}); err != nil {
					return nil, err
				}
			}
			if err := write(apiDir+"/api.md", "api", "API", "Declared API schema and operations", apiBody.String(), map[string]any{"api_id": spec.AssetID, "schema_id": spec.SchemaID, "schema_set": spec.SchemaSet}); err != nil {
				return nil, err
			}
		}
		if err := writePage(filepath.Join(root, role.dir, "index.md"), []byte(overview.String())); err != nil {
			return nil, err
		}
	}
	if err := writeViewer(root, run, captures, report); err != nil {
		return nil, err
	}
	return report, nil
}

func escape(s string) string {
	s = html.EscapeString(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	return strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_", "`", "&#96;").Replace(s)
}

func unchangedPage(path, kind, title, description string, extra map[string]any, body string) bool {
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(data, []byte("---\n")) {
		return false
	}
	parts := strings.SplitN(string(data), "---\n", 3)
	if len(parts) != 3 || strings.TrimPrefix(parts[2], "\n") != body {
		return false
	}
	var front map[string]any
	if yaml.Unmarshal([]byte(parts[1]), &front) != nil {
		return false
	}
	if front["type"] != title || front["title"] != title || front["description"] != description {
		return false
	}
	phrond, ok := front["phrond"].(map[string]any)
	if !ok || phrond["kind"] != kind || phrond["profile_version"] != "1.0" {
		return false
	}
	for key, value := range extra {
		left, err1 := json.Marshal(phrond[key])
		right, err2 := json.Marshal(value)
		if err1 != nil || err2 != nil || !bytes.Equal(left, right) {
			return false
		}
	}
	return true
}

func currentAttemptPage(path, runID, attemptID, evidencePath string) bool {
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(data, []byte("---\n")) {
		return false
	}
	parts := bytes.SplitN(data, []byte("---\n"), 3)
	if len(parts) != 3 {
		return false
	}
	var front map[string]any
	if yaml.Unmarshal(parts[1], &front) != nil {
		return false
	}
	phrond, ok := front["phrond"].(map[string]any)
	return ok && phrond["profile_version"] == "1.0" && phrond["kind"] == "attempt" && phrond["run_id"] == runID && phrond["attempt_id"] == attemptID && phrond["evidence"] == evidencePath
}

func shortHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:10]) }

func passingObservation(samples []coverage.FlowSample) bool {
	for _, sample := range samples {
		if sample.TestStatus == "pass" && !sample.CaptureIncomplete {
			return true
		}
	}
	return false
}

func sortedHeaderNames(headers artifact.Headers) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func preview(value string) string {
	data := []byte(value)
	truncated := len(data) > 4096
	if truncated {
		data = data[:4096]
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}
	content := string(data)
	fence := "```"
	for strings.Contains(content, fence) {
		fence += "`"
	}
	var out strings.Builder
	out.WriteString(fence + "text\n" + content + "\n" + fence + "\n\n")
	if truncated {
		out.WriteString("Preview limited to 4 KiB; use the attachment for retained bytes.\n\n")
	}
	return out.String()
}

func writePage(path string, data []byte) error {
	if previous, err := os.ReadFile(path); err == nil && bytes.Equal(previous, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".phrond-view-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := bytes.NewReader(data).WriteTo(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
