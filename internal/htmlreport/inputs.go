package htmlreport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
	"gopkg.in/yaml.v3"
)

// Files names the complete set of standalone report inputs. References inside
// these documents are display content: loading never follows them.
type Files struct {
	Coverage string
	Specs    []string
	Arazzo   string
}

// Load reads a coverage JSON document and only the explicitly named local files.
func Load(files Files) (Inputs, error) {
	if strings.TrimSpace(files.Coverage) == "" {
		return Inputs{}, errors.New("HTML report requires Wiretap coverage JSON")
	}
	limits, err := bundle.ConfiguredLoadLimits()
	if err != nil {
		return Inputs{}, err
	}
	reader := inputReader{limits: limits}
	data, err := reader.read(files.Coverage)
	if err != nil {
		return Inputs{}, fmt.Errorf("read Wiretap JSON: %w", err)
	}
	value, err := jsonObject(data)
	if err != nil {
		return Inputs{}, fmt.Errorf("read Wiretap JSON: %w", err)
	}
	if err := coverageShape(value); err != nil {
		return Inputs{}, fmt.Errorf("read Wiretap JSON: %w", err)
	}
	var report coverage.Report
	if err := decodeJSON(data, &report); err != nil {
		return Inputs{}, fmt.Errorf("read Wiretap JSON: %w", err)
	}
	if report.ReportVersion != coverage.ReportVersion || report.GeneratedAt.IsZero() || report.ArtifactCount < 0 || report.Capture == nil || report.Capture.Revision == 0 || report.Contracts == nil {
		return Inputs{}, errors.New("read Wiretap JSON: invalid coverage report metadata")
	}
	if report.Producer.Name != "wiretap" || report.Producer.Version == "" || report.Capture.FormatVersion == "" || report.Capture.RunID == "" {
		return Inputs{}, errors.New("read Wiretap JSON: invalid producer or capture identity")
	}
	if report.Capture.State != "open" && report.Capture.State != "complete" && report.Capture.State != "interrupted" {
		return Inputs{}, errors.New("read Wiretap JSON: invalid capture state")
	}
	result := Inputs{Report: &report, WiretapName: filepath.Base(files.Coverage), WiretapJSON: string(data)}
	names := map[string]int{}
	seen := map[string]bool{}
	for _, path := range files.Specs {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return Inputs{}, err
		}
		if seen[absolute] {
			continue
		}
		seen[absolute] = true
		data, err := reader.read(path)
		if err != nil {
			return Inputs{}, fmt.Errorf("read spec %q: %w", path, err)
		}
		if err := specShape(data); err != nil {
			return Inputs{}, fmt.Errorf("read spec %q: %w", path, err)
		}
		name := filepath.Base(path)
		names[name]++
		if names[name] > 1 {
			name = fmt.Sprintf("%s (%d)", name, names[name])
		}
		result.Specs = append(result.Specs, Document{Name: name, Content: string(data)})
	}
	if files.Arazzo != "" {
		data, err := reader.read(files.Arazzo)
		if err != nil {
			return Inputs{}, fmt.Errorf("read Arazzo: %w", err)
		}
		value, err := jsonObject(data)
		if err != nil {
			return Inputs{}, fmt.Errorf("read Arazzo: %w", err)
		}
		if err := arazzoShape(value); err != nil {
			return Inputs{}, fmt.Errorf("read Arazzo: %w", err)
		}
		var doc arazzo.Document
		if err := decodeJSON(data, &doc); err != nil {
			return Inputs{}, fmt.Errorf("read Arazzo: %w", err)
		}
		if doc.Arazzo != value["arazzo"] {
			return Inputs{}, errors.New("read Arazzo: ambiguous version field")
		}
		result.Arazzo = &doc
		result.ArazzoName = filepath.Base(files.Arazzo)
		result.ArazzoJSON = string(data)
	}
	return result, nil
}

// Archive derives report inputs from verified capture evidence and its frozen
// contracts. Missing Arazzo eligibility is a visible report note, not lost API
// coverage. Contract URLs and original source paths are never loaded.
func Archive(ctx context.Context, path, sutName string, includeArtifacts bool) (Inputs, error) {
	if err := ctx.Err(); err != nil {
		return Inputs{}, err
	}
	run, captures, err := bundle.Load(path)
	if err != nil {
		return Inputs{}, err
	}
	report, err := coverage.GenerateBundleSnapshot(path, run, captures, sutName, includeArtifacts)
	if err != nil {
		return Inputs{}, err
	}
	result := Inputs{Report: report, WiretapName: "coverage.json"}
	specs := map[string]string{}
	seenContracts := map[string]bool{}
	for _, ref := range run.Schemas {
		if err := ctx.Err(); err != nil {
			return Inputs{}, err
		}
		if ref.Type != "openapi" || ref.Source.Type == "omitted" {
			continue
		}
		key := ref.Source.Path + "\x00" + ref.Source.SHA256
		if seenContracts[key] {
			continue
		}
		seenContracts[key] = true
		_, files, err := bundle.OpenAPIFiles(ref)
		if err != nil {
			return Inputs{}, fmt.Errorf("read frozen OpenAPI %q: %w", ref.ID, err)
		}
		for name, data := range files {
			if previous, exists := specs[name]; exists && previous != string(data) {
				return Inputs{}, fmt.Errorf("frozen OpenAPI documents disagree at %q", name)
			}
			specs[name] = string(data)
		}
	}
	if err := ctx.Err(); err != nil {
		return Inputs{}, err
	}
	export, err := arazzo.Generate(path, sutName)
	if canceled := ctx.Err(); canceled != nil {
		return Inputs{}, canceled
	}
	if err != nil {
		report.Notes = append(report.Notes, "Arazzo workflows unavailable: "+err.Error())
	} else if export.Document == nil {
		report.Notes = append(report.Notes, "Arazzo workflows unavailable: the capture contains no eligible JSON/REST interactions.")
	} else {
		result.Arazzo = export.Document
		result.ArazzoName = "arazzo.json"
		for _, file := range export.Files {
			if previous, exists := specs[file.Path]; exists && previous != string(file.Data) {
				return Inputs{}, fmt.Errorf("Arazzo and frozen OpenAPI documents disagree at %q", file.Path)
			}
			specs[file.Path] = string(file.Data)
		}
	}
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result.Specs = append(result.Specs, Document{Name: name, Content: specs[name]})
	}
	return result, nil
}

type inputReader struct {
	limits bundle.LoadLimits
	reads  int
	bytes  int64
}

func (reader *inputReader) read(path string) ([]byte, error) {
	if reader.reads >= reader.limits.MaxFileReads {
		return nil, fmt.Errorf("%w: too many HTML input files", bundle.ErrLoadLimit)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	check := func(info os.FileInfo) error {
		if !info.Mode().IsRegular() {
			return errors.New("HTML input must be a regular local file")
		}
		if info.Size() > reader.limits.MaxFileBytes || info.Size() > reader.limits.MaxTotalBytes-reader.bytes {
			return fmt.Errorf("%w: HTML input exceeds its byte budget", bundle.ErrLoadLimit)
		}
		return nil
	}
	if err := check(info); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if err := check(info); err != nil {
		return nil, err
	}
	limit := min(reader.limits.MaxFileBytes, reader.limits.MaxTotalBytes-reader.bytes)
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: HTML input grew beyond its byte budget", bundle.ErrLoadLimit)
	}
	reader.reads++
	reader.bytes += int64(len(data))
	return data, nil
}

const maxInputDepth = 128
const maxInputNodes = 1_000_000

func jsonObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nodes := 0
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		nodes++
		if depth > maxInputDepth || nodes > maxInputNodes {
			return nil, errors.New("JSON input exceeds its nesting or value budget")
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				object := map[string]any{}
				for decoder.More() {
					keyToken, err := decoder.Token()
					if err != nil {
						return nil, err
					}
					key, ok := keyToken.(string)
					if !ok {
						return nil, errors.New("invalid JSON object key")
					}
					if _, exists := object[key]; exists {
						return nil, fmt.Errorf("duplicate JSON key %q", key)
					}
					value, err := read(depth + 1)
					if err != nil {
						return nil, err
					}
					object[key] = value
				}
				_, err := decoder.Token()
				return object, err
			case '[':
				array := []any{}
				for decoder.More() {
					value, err := read(depth + 1)
					if err != nil {
						return nil, err
					}
					array = append(array, value)
				}
				_, err := decoder.Token()
				return array, err
			default:
				return nil, errors.New("unexpected JSON delimiter")
			}
		}
		return token, nil
	}
	value, err := read(0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("extra JSON content")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("input must be a JSON object")
	}
	return object, nil
}

func decodeJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(value)
}

func coverageShape(value map[string]any) error {
	if value["report_version"] != coverage.ReportVersion {
		return fmt.Errorf("expected coverage report_version %q", coverage.ReportVersion)
	}
	for _, key := range []string{"producer", "capture", "provided", "dependent", "contract_coverage"} {
		if _, ok := value[key].(map[string]any); !ok {
			return fmt.Errorf("coverage report requires object %q", key)
		}
	}
	for _, key := range []string{"generated_at", "sut_name"} {
		if _, ok := value[key].(string); !ok {
			return fmt.Errorf("coverage report requires string %q", key)
		}
	}
	if _, ok := value["artifact_count"].(json.Number); !ok {
		return errors.New("coverage report requires artifact_count")
	}
	for _, key := range []string{"provided", "dependent"} {
		group := value[key].(map[string]any)
		if specs, exists := group["specs"]; !exists || !arrayOrNull(specs) {
			return fmt.Errorf("coverage report requires %s.specs array or null", key)
		}
	}
	capture := value["capture"].(map[string]any)
	for _, key := range []string{"format_version", "run_id", "state"} {
		if !nonemptyString(capture[key]) {
			return fmt.Errorf("coverage report requires capture.%s", key)
		}
	}
	if _, ok := capture["revision"].(json.Number); !ok {
		return errors.New("coverage report requires capture.revision")
	}
	contracts := value["contract_coverage"].(map[string]any)
	for _, key := range []string{"rules_version", "run_id", "run_state"} {
		if !nonemptyString(contracts[key]) {
			return fmt.Errorf("coverage report requires contract_coverage.%s", key)
		}
	}
	if _, ok := contracts["complete"].(bool); !ok {
		return errors.New("coverage report requires contract_coverage.complete")
	}
	if specs, exists := contracts["specs"]; !exists || !arrayOrNull(specs) {
		return errors.New("coverage report requires contract_coverage.specs array or null")
	}
	return nil
}

func arrayOrNull(value any) bool {
	_, array := value.([]any)
	return value == nil || array
}

func arazzoShape(value map[string]any) error {
	version, _ := value["arazzo"].(string)
	parts := strings.Split(version, ".")
	if len(parts) != 3 || parts[0] != "1" || parts[1] != "0" {
		return errors.New("expected Arazzo version 1.0.x")
	}
	if patch, err := strconv.Atoi(parts[2]); err != nil || patch < 0 {
		return errors.New("expected Arazzo version 1.0.x")
	}
	info, ok := value["info"].(map[string]any)
	if !ok || !nonemptyString(info["title"]) || !nonemptyString(info["version"]) {
		return errors.New("Arazzo requires info.title and info.version")
	}
	sources, ok := value["sourceDescriptions"].([]any)
	if !ok || len(sources) == 0 {
		return errors.New("Arazzo requires sourceDescriptions")
	}
	for _, value := range sources {
		source, ok := value.(map[string]any)
		if !ok || !nonemptyString(source["name"]) || !nonemptyString(source["url"]) || (source["type"] != "openapi" && source["type"] != "arazzo") {
			return errors.New("invalid Arazzo source description")
		}
	}
	workflows, ok := value["workflows"].([]any)
	if !ok || len(workflows) == 0 {
		return errors.New("Arazzo requires workflows")
	}
	ids := map[string]bool{}
	for _, value := range workflows {
		workflow, ok := value.(map[string]any)
		if !ok || !nonemptyString(workflow["workflowId"]) {
			return errors.New("invalid Arazzo workflow")
		}
		id := workflow["workflowId"].(string)
		if ids[id] {
			return fmt.Errorf("duplicate Arazzo workflowId %q", id)
		}
		ids[id] = true
		steps, ok := workflow["steps"].([]any)
		if !ok || len(steps) == 0 {
			return errors.New("Arazzo workflow requires steps")
		}
		stepIDs := map[string]bool{}
		for _, value := range steps {
			step, ok := value.(map[string]any)
			if !ok || !nonemptyString(step["stepId"]) {
				return errors.New("invalid Arazzo step")
			}
			id := step["stepId"].(string)
			if stepIDs[id] {
				return fmt.Errorf("duplicate Arazzo stepId %q", id)
			}
			stepIDs[id] = true
			operations := 0
			for _, key := range []string{"operationId", "operationPath", "workflowId"} {
				if nonemptyString(step[key]) {
					operations++
				}
			}
			if operations != 1 {
				return errors.New("Arazzo step requires exactly one operation or workflow reference")
			}
		}
	}
	return nil
}

func nonemptyString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}

// specShape checks local syntax without constructing a loader or resolving refs.
// Component documents are accepted because frozen OpenAPI packages can contain
// explicitly supplied fragments as well as their root document.
func specShape(data []byte) error {
	var value map[string]any
	if json.Valid(data) {
		var err error
		value, err = jsonObject(data)
		if err != nil {
			return err
		}
	} else {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		var document yaml.Node
		if err := decoder.Decode(&document); err != nil {
			return err
		}
		nodes := 0
		var check func(*yaml.Node, int) error
		check = func(node *yaml.Node, depth int) error {
			nodes++
			if depth > maxInputDepth || nodes > maxInputNodes {
				return errors.New("YAML input exceeds its nesting or value budget")
			}
			if node.Kind == yaml.AliasNode {
				return errors.New("YAML aliases are not supported in HTML spec inputs")
			}
			if node.Kind == yaml.MappingNode {
				keys := map[string]bool{}
				for i := 0; i < len(node.Content); i += 2 {
					key := node.Content[i]
					if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
						return errors.New("spec object keys must be strings")
					}
					if keys[key.Value] {
						return fmt.Errorf("duplicate YAML key %q", key.Value)
					}
					keys[key.Value] = true
				}
			}
			for _, child := range node.Content {
				if err := check(child, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		if err := check(&document, 0); err != nil {
			return err
		}
		if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
			return errors.New("spec input must be an object")
		}
		if err := document.Decode(&value); err != nil {
			return err
		}
		var extra yaml.Node
		if err := decoder.Decode(&extra); err != io.EOF {
			return errors.New("extra YAML document")
		}
	}
	if version, exists := value["openapi"]; exists {
		text, ok := version.(string)
		parts := strings.Split(text, ".")
		valid := ok && len(parts) == 3 && parts[0] == "3"
		if valid {
			for _, part := range parts[1:] {
				number, err := strconv.Atoi(part)
				valid = valid && err == nil && number >= 0
			}
		}
		if !valid {
			return errors.New("spec requires an OpenAPI 3.x version")
		}
		info, ok := value["info"].(map[string]any)
		if !ok || !nonemptyString(info["title"]) || !nonemptyString(info["version"]) {
			return errors.New("OpenAPI spec requires info.title and info.version")
		}
		return nil
	}
	for _, other := range []string{"arazzo", "report_version", "format_version", "swagger"} {
		if _, exists := value[other]; exists {
			return errors.New("expected an OpenAPI 3.x spec or component document")
		}
	}
	for _, key := range []string{"components", "$schema", "$defs", "definitions", "type", "properties", "$ref"} {
		if _, exists := value[key]; exists {
			return nil
		}
	}
	return errors.New("expected an OpenAPI 3.x spec or component document")
}
