package postman

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
)

// Options affects only derived exports. Strict export is the default. Opting
// into omission returns a diagnostic for every excluded recorded interaction.
type Options struct {
	OmitIncomplete bool `json:"omit_incomplete,omitempty" yaml:"omit_incomplete,omitempty"`
	// Keys are "sut" or "dependency:<exact dependency ID>". Endpoint URLs are never inferred
	// from the process performing an offline export.
	GRPC map[string]GRPCTarget `json:"grpc,omitempty" yaml:"grpc,omitempty"`
}
type GRPCTarget struct {
	URL       string `json:"url,omitempty" yaml:"url,omitempty"`
	ProtoRoot string `json:"proto_root,omitempty" yaml:"proto_root,omitempty"`
	ProtoFile string `json:"proto_file,omitempty" yaml:"proto_file,omitempty"`
}
type Diagnostic struct {
	AttemptID string `json:"attempt_id"`
	FlowID    string `json:"flow_id"`
	Protocol  string `json:"protocol"`
	Message   string `json:"message"`
}
type Result struct {
	Exports     []Export
	Diagnostics []Diagnostic
}
type CompanionFile struct {
	Path string
	Data []byte
}

// ReadOptions reads a strict local JSON configuration. Proto roots are relative
// to this file, so exports do not depend on the invoking shell's working dir.
func ReadOptions(path string) (Options, error) {
	var options Options
	f, err := os.Open(path)
	if err != nil {
		return options, err
	}
	defer f.Close()
	const maxOptionsBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxOptionsBytes+1))
	if err != nil {
		return options, err
	}
	if len(data) > maxOptionsBytes {
		return options, errors.New("Postman options exceed 1 MiB")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return options, errors.New("Postman options must contain one JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(&options); err != nil {
		return options, err
	}
	if d.Decode(new(any)) != io.EOF {
		return options, errors.New("Postman options must contain one JSON object")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return options, err
	}
	for id, target := range options.GRPC {
		if target.ProtoRoot != "" && !filepath.IsAbs(target.ProtoRoot) {
			target.ProtoRoot = filepath.Join(base, target.ProtoRoot)
			options.GRPC[id] = target
		}
	}
	return options, nil
}

func GenerateAllWithOptions(path, name string, options Options) (*Result, error) {
	run, captures, err := loadRun(path)
	if err != nil {
		return nil, err
	}
	result := &Result{Diagnostics: []Diagnostic{}}
	filtered := make([]*artifact.Artifact, 0, len(captures))
	var failures []error
	grpcBoundaries := map[string]bool{}
	for _, capture := range captures {
		copy := *capture
		copy.Traffic = make([]artifact.Flow, 0, len(capture.Traffic))
		for _, flow := range capture.Traffic {
			eligible := flow.Interaction == nil && (flow.Channel == "test_to_sut" || flow.Channel == "sut_to_dependency" && flow.AssetID != "" && run.DependencyKinds[flow.AssetID] == "mock") && (flow.Protocol == "http" || flow.Protocol == "grpc")
			if !eligible {
				copy.Traffic = append(copy.Traffic, flow)
				continue
			}
			if flow.Protocol == "grpc" {
				key := "sut"
				if flow.Channel == "sut_to_dependency" {
					key = "dependency:" + flow.AssetID
				}
				grpcBoundaries[key] = true
			}
			var problem error
			if capture.CaptureIncomplete {
				problem = errors.New("test capture is incomplete")
			} else if len(capture.Redactions) > 0 {
				// Legacy producers did not record which fields were changed, so
				// their configured rules cannot safely be replayed as originals.
				problem = errors.New("legacy capture contains redaction rules; original exchange is unavailable")
			} else if flow.Protocol == "grpc" {
				_, problem = exportGRPCFlow(flow, 1)
			} else {
				problem = httpCompleteness(flow)
			}
			if problem == nil {
				copy.Traffic = append(copy.Traffic, flow)
				continue
			}
			diagnostic := Diagnostic{capture.TestCase.ID, flow.ID, flow.Protocol, problem.Error()}
			result.Diagnostics = append(result.Diagnostics, diagnostic)
			failures = append(failures, fmt.Errorf("test %q flow %q (%s): %w", diagnostic.AttemptID, flow.ID, flow.Protocol, problem))
		}
		// All retained interactions have been checked individually. The original
		// recording and capture completeness marker are never changed on disk.
		copy.CaptureIncomplete = false
		filtered = append(filtered, &copy)
	}
	if len(failures) > 0 && !options.OmitIncomplete {
		return result, fmt.Errorf("Postman export refused incomplete/unrepresentable interactions (use --postman-omit-incomplete to export the others): %w", errors.Join(failures...))
	}
	result.Exports, err = generateAllRun(run, filtered, name)
	if err != nil {
		return result, err
	}
	if err = applyTargets(result.Exports, options.GRPC, grpcBoundaries); err != nil {
		return result, err
	}
	return result, nil
}

func httpCompleteness(flow artifact.Flow) error {
	if flow.Request.HTTP == nil || flow.Response.HTTP == nil || flow.Response.HTTP.Status < 100 || flow.Response.HTTP.Status > 999 {
		return errors.New("a complete HTTP request and response status are required")
	}
	if flow.TransportError != "" {
		return fmt.Errorf("HTTP transport error: %s", flow.TransportError)
	}
	for _, message := range []*artifact.HttpMessage{flow.Request.HTTP, flow.Response.HTTP} {
		if message.Body != nil && (message.Body.Truncated || message.Body.Omitted || message.Body.Redacted) {
			return errors.New("HTTP body was truncated, omitted or changed by a legacy producer")
		}
	}
	return nil
}

// A logical name can repeat across retries. Retain each raw attempt ID as the
// folder ID, suffix repeated labels with it, then resolve even literal suffix
// collisions deterministically. This never changes raw test names or IDs.
func attemptFolderNames(captures []*artifact.Artifact) map[string]string {
	names := map[string]string{}
	counts := map[string]int{}
	for _, c := range captures {
		n := c.TestCase.Name
		if strings.TrimSpace(n) == "" {
			n = c.TestCase.ID
		}
		names[c.TestCase.ID] = n
		counts[n]++
	}
	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	used := map[string]bool{}
	for _, id := range ids {
		n := names[id]
		if counts[n] > 1 {
			n += " [attempt " + id + "]"
		}
		base := n
		for i := 1; used[n]; i++ {
			n = fmt.Sprintf("%s [attempt %s-%d]", base, id, i)
		}
		used[n] = true
		names[id] = n
	}
	return names
}
