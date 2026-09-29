package bundlereport

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/phrond/evermock/internal/bundle"
	"gopkg.in/yaml.v3"
)

var markdownLink = regexp.MustCompile(`!?\[[^]]*\]\(([^)]+)\)`)

// Validate checks the disposable Wiretap views and their local links. Evidence
// readers and exports deliberately never call this function.
func Validate(root string) error {
	run, captures, err := bundle.Load(root)
	if err != nil {
		return err
	}
	if err := checkViewer(root, run); err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	flows := map[string]map[string]bool{}
	for _, capture := range captures {
		ids := map[string]bool{}
		for _, flow := range capture.Traffic {
			ids[flow.ID] = true
		}
		flows[capture.TestCase.ID] = ids
	}
	for _, rel := range []string{"index.md", "run.md", "coverage.md", "apis/index.md", "dependencies/index.md", "tests/index.md"} {
		if err := validatePage(root, rel, run.RunID, run.Revision, flows, false); err != nil {
			return err
		}
	}
	for _, attempt := range run.Attempts {
		if err := validatePage(root, "tests/"+attempt.AttemptID+".md", run.RunID, run.Revision, flows, true); err != nil {
			return err
		}
	}
	for _, dir := range []string{"apis", "dependencies"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || entry.Name() == "index.md" {
				return nil
			}
			if entry.Name() != "api.md" && filepath.Base(filepath.Dir(path)) != "operations" {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return validatePage(root, rel, run.RunID, run.Revision, flows, true)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func validatePage(root, rel, runID string, runRevision uint64, flows map[string]map[string]bool, requiredProfile bool) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("view %s: %w", rel, err)
	}
	var front map[string]any
	if bytes.HasPrefix(data, []byte("---\n")) {
		parts := bytes.SplitN(data, []byte("---\n"), 3)
		if len(parts) != 3 || yaml.Unmarshal(parts[1], &front) != nil {
			return fmt.Errorf("view %s: invalid YAML frontmatter", rel)
		}
	} else if requiredProfile || rel == "run.md" || rel == "coverage.md" {
		return fmt.Errorf("view %s: missing Wiretap frontmatter", rel)
	}
	if wiretap, exists := front["wiretap"]; exists {
		if err := validateProfile(root, path, rel, front, wiretap, runID, runRevision, flows); err != nil {
			return err
		}
	} else if requiredProfile || rel == "run.md" || rel == "coverage.md" {
		return fmt.Errorf("view %s: missing Wiretap profile", rel)
	}
	for _, match := range markdownLink.FindAllSubmatch(withoutFencedBlocks(data), -1) {
		link := string(match[1])
		if strings.Contains(link, "://") || strings.HasPrefix(link, "#") {
			continue
		}
		link = strings.SplitN(link, "#", 2)[0]
		if link == "" {
			continue
		}
		if err := localReference(root, path, link); err != nil {
			return fmt.Errorf("view %s: link %q: %w", rel, link, err)
		}
	}
	return nil
}

func validateProfile(root, path, rel string, front map[string]any, value any, runID string, runRevision uint64, flows map[string]map[string]bool) error {
	profile, ok := value.(map[string]any)
	if !ok || profile["profile_version"] != "1.0" || profile["run_id"] != runID {
		return fmt.Errorf("view %s: unsupported profile or run identity", rel)
	}
	revision, ok := profile["snapshot_revision"].(int)
	if !ok || revision < 1 || uint64(revision) > runRevision {
		return fmt.Errorf("view %s: missing snapshot revision", rel)
	}
	kind, ok := profile["kind"].(string)
	if !ok || !map[string]bool{"run": true, "coverage": true, "api": true, "operation": true, "assumptions": true, "attempt": true}[kind] {
		return fmt.Errorf("view %s: unsupported concept kind", rel)
	}
	for _, key := range []string{"type", "title", "description"} {
		if s, ok := front[key].(string); !ok || strings.TrimSpace(s) == "" {
			return fmt.Errorf("view %s: missing %s", rel, key)
		}
	}
	generated, ok := front["generated"].(map[string]any)
	if !ok || generated["by"] != "wiretap/1.0" {
		return fmt.Errorf("view %s: invalid generator", rel)
	}
	at, ok := generated["at"].(string)
	if !ok {
		return fmt.Errorf("view %s: missing generated time", rel)
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		return fmt.Errorf("view %s: invalid generated time: %w", rel, err)
	}
	if sources, ok := front["sources"]; ok {
		list, ok := sources.([]any)
		if !ok {
			return fmt.Errorf("view %s: invalid sources", rel)
		}
		for _, item := range list {
			source, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("view %s: invalid source", rel)
			}
			resource, ok := source["resource"].(string)
			if !ok {
				return fmt.Errorf("view %s: missing source resource", rel)
			}
			if err := localReference(root, path, resource); err != nil {
				return fmt.Errorf("view %s: source %q: %w", rel, resource, err)
			}
		}
	}
	switch evidence := profile["evidence"].(type) {
	case string:
		if err := rootReference(root, evidence); err != nil {
			return fmt.Errorf("view %s: evidence: %w", rel, err)
		}
	case []any:
		for _, item := range evidence {
			ref, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("view %s: invalid evidence pointer", rel)
			}
			attemptID, _ := ref["attempt_id"].(string)
			flowID, _ := ref["flow_id"].(string)
			path, _ := ref["path"].(string)
			if !flows[attemptID][flowID] || path != "evidence/attempts/"+attemptID+".json" {
				return fmt.Errorf("view %s: evidence pointer does not identify a committed flow", rel)
			}
		}
	case nil:
		if kind == "run" || kind == "coverage" || kind == "attempt" {
			return fmt.Errorf("view %s: missing evidence pointer", rel)
		}
	default:
		return fmt.Errorf("view %s: invalid evidence pointer", rel)
	}
	return nil
}

func withoutFencedBlocks(data []byte) []byte {
	var out bytes.Buffer
	fenceWidth := 0
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		width := 0
		for width < len(trimmed) && trimmed[width] == '`' {
			width++
		}
		if fenceWidth == 0 && width >= 3 {
			fenceWidth = width
			continue
		}
		if fenceWidth > 0 && width >= fenceWidth {
			fenceWidth = 0
			continue
		}
		if fenceWidth == 0 {
			out.Write(line)
		}
	}
	return out.Bytes()
}

func rootReference(root, rel string) error {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("reference escapes bundle")
	}
	return localReference(root, filepath.Join(root, "index.md"), rel)
}

func localReference(root, page, rel string) error {
	if filepath.IsAbs(rel) {
		return fmt.Errorf("absolute reference")
	}
	target := filepath.Join(filepath.Dir(page), filepath.FromSlash(rel))
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	back, err := filepath.Rel(root, target)
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return fmt.Errorf("reference escapes bundle")
	}
	return nil
}
