package assumptions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/arazzo"
)

// verifyArazzo verifies an offline projection against its original archive. The
// archive remains the comparison evidence; Arazzo expressions are never run.
// Only the selected boundary is an obligation, but every selected exchange must
// be represented, so a partial JSON/REST export cannot silently omit other traffic.
func (s *snapshot) verifyArazzo(input, boundary string) (documentPath string, companions []string, err error) {
	expected, err := arazzo.GenerateSnapshot(s.run, s.captures, "")
	if err != nil {
		return "", nil, err
	}
	selected, channel := "sut", "test_to_sut"
	if boundary != "" {
		selected, channel = "mock:"+boundary, "sut_to_dependency"
	}
	samples := s.samples(channel, boundary)
	if err := arazzoCoverage(expected.Document, selected, samples); err != nil {
		return "", nil, err
	}
	defaultInput := input == ""
	if defaultInput {
		input = filepath.Join(s.root, "exports", "arazzo", "arazzo.json")
	}
	path, err := filepath.Abs(input)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && defaultInput && expected.Document == nil && len(samples) == 0 {
		return "", nil, s.unchanged()
	}
	if err != nil {
		return "", nil, fmt.Errorf("Arazzo input %q: %w", input, err)
	}
	if expected.Document == nil {
		return "", nil, errors.New("Arazzo document is stale or unverifiable: this capture has no generated JSON/REST document")
	}
	if info.IsDir() {
		path = filepath.Join(path, "arazzo.json")
	} else if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("Arazzo input %q must be a regular JSON file or directory", input)
	}
	if !strings.EqualFold(filepath.Ext(path), ".json") {
		return "", nil, errors.New("Arazzo document must be a JSON file")
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", nil, fmt.Errorf("Arazzo document directory: %w", err)
	}
	data, err := readArazzoFile(path, root)
	if err != nil {
		return "", nil, fmt.Errorf("Arazzo document %q: %w", path, err)
	}
	actualValue, err := decodeProjectionJSON(data)
	if err != nil {
		return "", nil, fmt.Errorf("Arazzo document %q: %w", path, err)
	}
	actual, ok := actualValue.(map[string]any)
	if !ok {
		return "", nil, errors.New("Arazzo document must be one JSON object")
	}
	encoded, err := json.Marshal(expected.Document)
	if err != nil {
		return "", nil, err
	}
	expectedValue, err := decodeProjectionJSON(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("expected Arazzo document: %w", err)
	}
	wanted := expectedValue.(map[string]any)
	wantedProjection, wantedSources, err := arazzoProjection(wanted, selected)
	if err != nil {
		return "", nil, fmt.Errorf("expected Arazzo document: %w", err)
	}
	actualProjection, actualSources, err := arazzoProjection(actual, selected)
	if err != nil {
		return "", nil, err
	}
	if err := sameArazzoJSON(wantedProjection, actualProjection); err != nil {
		return "", nil, fmt.Errorf("Arazzo selected boundary %q does not match its capture: workflows, inputs, operation references, statuses or attempt/flow IDs are missing, extra, stale or edited: %w", selected, err)
	}
	for name, source := range wantedSources {
		if err := sameArazzoJSON(source, actualSources[name]); err != nil {
			return "", nil, fmt.Errorf("Arazzo source %q is missing, stale or edited", name)
		}
	}
	// Frozen source packages contain their complete transitive reference set.
	// Verify the entire selected package, including cyclic/shared companions,
	// without resolving references or opening any path supplied by the document.
	files := map[string][]byte{}
	if len(expected.Files) > maxProjectionFiles {
		return "", nil, fmt.Errorf("Arazzo export exceeds %d companion files", maxProjectionFiles)
	}
	for _, file := range expected.Files {
		if _, duplicate := files[file.Path]; duplicate {
			return "", nil, errors.New("duplicate expected Arazzo companion path")
		}
		files[file.Path] = file.Data
	}
	used := map[string]bool{}
	for _, source := range wantedSources {
		address, _ := source["url"].(string)
		if err := localArazzoPath(address); err != nil {
			return "", nil, fmt.Errorf("Arazzo source path: %w", err)
		}
		if _, found := files[address]; !found {
			return "", nil, errors.New("selected Arazzo source has no generated companion")
		}
		used[address] = true
		if strings.HasPrefix(address, "contracts/") {
			prefix := filepath.ToSlash(filepath.Dir(filepath.FromSlash(address))) + "/"
			for member := range files {
				if strings.HasPrefix(member, prefix) {
					used[member] = true
				}
			}
		}
	}
	paths := make([]string, 0, len(used))
	for member := range used {
		paths = append(paths, member)
	}
	sort.Strings(paths)
	total := int64(len(data))
	for _, member := range paths {
		if err := localArazzoPath(member); err != nil {
			return "", nil, err
		}
		companion := filepath.Join(filepath.Dir(path), filepath.FromSlash(member))
		actualData, err := readArazzoFile(companion, root)
		if err != nil {
			return "", nil, fmt.Errorf("Arazzo companion %q: %w", member, err)
		}
		total += int64(len(actualData))
		if total > maxProjectionsBytes {
			return "", nil, fmt.Errorf("Arazzo document and companions exceed %d MiB in total", maxProjectionsBytes>>20)
		}
		actualJSON, err := decodeProjectionJSON(actualData)
		if err != nil {
			return "", nil, fmt.Errorf("Arazzo companion %q: %w", member, err)
		}
		wantedJSON, err := decodeProjectionJSON(files[member])
		if err != nil {
			return "", nil, fmt.Errorf("expected Arazzo companion %q: %w", member, err)
		}
		if err := sameArazzoJSON(wantedJSON, actualJSON); err != nil {
			return "", nil, fmt.Errorf("Arazzo companion %q does not match its capture: stale or edited source", member)
		}
		companions = append(companions, companion)
	}
	return path, companions, s.unchanged()
}

func arazzoCoverage(document *arazzo.Document, boundary string, samples []sample) error {
	coverage := map[[2]string]int{}
	if document != nil {
		for _, workflow := range document.Workflows {
			if workflow.Boundary != boundary {
				continue
			}
			for _, step := range workflow.Steps {
				coverage[[2]string{workflow.AttemptID, step.FlowID}]++
			}
		}
	}
	for _, sample := range samples {
		key := [2]string{sample.evidence.AttemptID, sample.evidence.FlowID}
		if coverage[key] != 1 {
			return fmt.Errorf("selected boundary %q is not completely representable by Arazzo JSON/REST workflows: attempt %q flow %q is omitted or ambiguous", boundary, key[0], key[1])
		}
		delete(coverage, key)
	}
	if len(coverage) != 0 {
		return errors.New("Arazzo selected workflows do not match the checker sample inventory")
	}
	return nil
}

func selectedArazzoWorkflows(document map[string]any, boundary string) ([]any, error) {
	values, ok := document["workflows"].([]any)
	if !ok {
		return nil, errors.New("Arazzo workflows must be an array")
	}
	selected := []any{}
	ids := map[string]bool{}
	for _, value := range values {
		workflow, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("Arazzo workflows must contain objects")
		}
		id, _ := workflow["workflowId"].(string)
		if id == "" || ids[id] {
			return nil, errors.New("Arazzo workflow IDs must be nonempty and unique")
		}
		ids[id] = true
		if workflow["x-wiretap-boundary"] == boundary {
			selected = append(selected, workflow)
		}
	}
	return selected, nil
}

// Project all document-level behavior and the selected workflows, retaining the
// source descriptions they reference. Unselected workflows are not obligations.
func arazzoProjection(document map[string]any, boundary string) (map[string]any, map[string]map[string]any, error) {
	workflows, err := selectedArazzoWorkflows(document, boundary)
	if err != nil {
		return nil, nil, err
	}
	info, ok := document["info"].(map[string]any)
	if !ok {
		return nil, nil, errors.New("Arazzo info must be an object")
	}
	if title, ok := info["title"].(string); !ok || title == "" {
		return nil, nil, errors.New("Arazzo info title must be a nonempty string")
	}
	if description, exists := info["description"]; exists {
		if _, ok := description.(string); !ok {
			return nil, nil, errors.New("Arazzo info description must be a string")
		}
	}
	projectedInfo := map[string]any{}
	for key, value := range info {
		if key != "title" && key != "description" {
			projectedInfo[key] = value
		}
	}
	allSources, ok := document["sourceDescriptions"].([]any)
	if !ok {
		return nil, nil, errors.New("Arazzo sourceDescriptions must be an array")
	}
	sources := map[string]map[string]any{}
	for _, value := range allSources {
		source, ok := value.(map[string]any)
		if !ok {
			return nil, nil, errors.New("Arazzo sourceDescriptions must contain objects")
		}
		name, _ := source["name"].(string)
		if name == "" || sources[name] != nil {
			return nil, nil, errors.New("Arazzo source names must be nonempty and unique")
		}
		sources[name] = source
	}
	used := map[string]map[string]any{}
	for _, value := range workflows {
		workflow := value.(map[string]any)
		steps, ok := workflow["steps"].([]any)
		if !ok {
			return nil, nil, errors.New("Arazzo workflow steps must be an array")
		}
		for _, value := range steps {
			step, ok := value.(map[string]any)
			if !ok {
				return nil, nil, errors.New("Arazzo workflow steps must contain objects")
			}
			operation, _ := step["operationPath"].(string)
			const prefix = "{$sourceDescriptions."
			if !strings.HasPrefix(operation, prefix) {
				return nil, nil, errors.New("Arazzo steps require generated local operation references")
			}
			name, _, found := strings.Cut(strings.TrimPrefix(operation, prefix), ".url}#")
			if !found || sources[name] == nil {
				return nil, nil, errors.New("Arazzo operation references a missing source description")
			}
			address, ok := sources[name]["url"].(string)
			if !ok {
				return nil, nil, errors.New("Arazzo source URL must be a local relative path")
			}
			if err := localArazzoPath(address); err != nil {
				return nil, nil, err
			}
			used[name] = sources[name]
		}
	}
	projection := map[string]any{}
	for key, value := range document {
		if key != "info" && key != "workflows" && key != "sourceDescriptions" {
			projection[key] = value
		}
	}
	projection["info"], projection["workflows"], projection["sourceDescriptions"] = projectedInfo, workflows, used
	return projection, used, nil
}

func localArazzoPath(path string) error {
	parsed, err := url.Parse(path)
	if err != nil || path == "" || parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(path, "\\") || strings.Contains(path, ":") || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) != path || path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return fmt.Errorf("Arazzo companion path %q must be a local relative path without traversal", path)
	}
	return nil
}

func readArazzoFile(path, root string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("must be a regular file, not a symbolic link")
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, physical)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("file escapes the Arazzo document directory")
	}
	return readProjection(path)
}

func sameArazzoJSON(expected, actual any) error {
	var wanted, got bytes.Buffer
	if err := writeCanonicalJSON(&wanted, expected); err != nil {
		return err
	}
	if err := writeCanonicalJSON(&got, actual); err != nil {
		return err
	}
	if wanted.String() != got.String() {
		return errors.New("JSON content differs")
	}
	return nil
}
