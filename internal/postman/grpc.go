package postman

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

// GenerateAll adds v3 gRPC collections to the unchanged v2.1 HTTP exports.
// A single verified snapshot supplies every output. The first export is the
// SUT HTTP collection when present, or the SUT gRPC collection otherwise.
func GenerateAll(bundlePath, sutName string) ([]Export, error) {
	result, err := GenerateAllWithOptions(bundlePath, sutName, Options{})
	if err != nil {
		return nil, err
	}
	return result.Exports, nil
}

func generateAllRun(run *bundle.Run, captures []*artifact.Artifact, sutName string) ([]Export, error) {
	exports, err := generateHTTP(run, captures, sutName)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, export := range exports {
		used[strings.ToLower(export.Filename)] = true
	}
	targets := map[string]bool{}
	for _, capture := range captures {
		for _, flow := range capture.Traffic {
			if flow.Protocol != "grpc" || flow.Interaction != nil {
				continue
			}
			if flow.Channel == "test_to_sut" {
				targets[""] = true
			} else if flow.Channel == "sut_to_dependency" && flow.AssetID != "" && run.DependencyKinds[flow.AssetID] == "mock" {
				targets[flow.AssetID] = true
			}
		}
	}
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	hasSUTHTTP := hasHTTPRequests(exports[0].Collection)
	if len(ids) > 0 && !hasSUTHTTP {
		exports = exports[1:]
	}
	for _, id := range ids {
		role, channel, filenameID, name := "mock", "sut_to_dependency", id, mockCollectionName(run, id)
		if id == "" {
			role, channel, filenameID = "sut", "test_to_sut", applicationID(run)
			name = strings.TrimSpace(sutName)
			if name == "" {
				name = collectionName(run)
				if name == "Wiretap "+run.RunID {
					name = "Wiretap"
				}
			}
		}
		collection, err := generateGRPC(captures, name, channel, id)
		if err != nil {
			return nil, err
		}
		// Reserve the complete filename so a peculiar API ID cannot collide with
		// an existing HTTP filename or another gRPC collection.
		filename := exportFilename(role, filenameID, map[string]bool{})
		stem := strings.TrimSuffix(filename, ".postman_collection.json") + ".grpc"
		filename = stem + ".postman_collection.json"
		for n := 1; used[strings.ToLower(filename)]; n++ {
			filename = fmt.Sprintf("%s-%d.postman_collection.json", stem, n)
		}
		used[strings.ToLower(filename)] = true
		export := Export{Filename: filename, GRPC: collection, BoundaryID: id}
		if id == "" && !hasSUTHTTP {
			exports = append([]Export{export}, exports...)
		} else {
			exports = append(exports, export)
		}
	}
	return exports, nil
}

func hasHTTPRequests(collection *Collection) bool {
	for _, folder := range collection.Items {
		if len(folder.Items) > 0 {
			return true
		}
	}
	return false
}

// WriteExport writes either protocol's native JSON collection format.
func WriteExport(w io.Writer, export Export) error {
	if export.Collection != nil && export.GRPC == nil {
		return Write(w, export.Collection)
	}
	if export.GRPC == nil || export.Collection != nil {
		return fmt.Errorf("Postman export must contain exactly one collection")
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(export.GRPC)
}

func generateGRPC(captures []*artifact.Artifact, name, channel, assetID string) (*GRPCCollection, error) {
	collection := &GRPCCollection{Kind: "collection", Name: name + " (gRPC)", Description: "Captured gRPC interaction examples. Contracts are supplied separately. Binary metadata uses base64. Message order is the order Wiretap observed complete messages; lifecycle events and execution results are excluded.", Items: []GRPCFolder{}}
	labels := attemptFolderNames(captures)
	seen := map[string]bool{}
	for _, capture := range captures {
		flows := []artifact.Flow{}
		chronological := true
		for _, flow := range capture.Traffic {
			if flow.Protocol != "grpc" || flow.Interaction != nil || flow.Channel != channel || assetID != "" && flow.AssetID != assetID {
				continue
			}
			flows = append(flows, flow)
			chronological = chronological && !flow.StartedAt.IsZero()
		}
		if len(flows) == 0 {
			continue
		}
		name := labels[capture.TestCase.ID]
		if seen[capture.TestCase.ID] {
			return nil, fmt.Errorf("duplicate test attempt %q", capture.TestCase.ID)
		}
		seen[capture.TestCase.ID] = true
		if capture.CaptureIncomplete {
			return nil, fmt.Errorf("export gRPC Postman test %q: capture is incomplete", name)
		}
		if chronological {
			sort.SliceStable(flows, func(i, j int) bool { return flows[i].StartedAt.Before(flows[j].StartedAt) })
		}
		folder := GRPCFolder{Kind: "collection", ID: capture.TestCase.ID, Name: name, Items: []GRPCRequest{}}
		for i, flow := range flows {
			request, err := exportGRPCFlow(flow, i+1)
			if err != nil {
				return nil, fmt.Errorf("export gRPC Postman test %q flow %q: %w", name, flow.ID, err)
			}
			folder.Items = append(folder.Items, request)
		}
		collection.Items = append(collection.Items, folder)
	}
	// Test execution order is not interaction knowledge.
	sort.Slice(collection.Items, func(i, j int) bool { return collection.Items[i].Name < collection.Items[j].Name })
	return collection, nil
}

func exportGRPCFlow(flow artifact.Flow, ordinal int) (GRPCRequest, error) {
	req, res := flow.Request.GRPC, flow.Response.GRPC
	if req == nil || res == nil || !artifact.GRPCDirectionComplete(req, true) || !artifact.GRPCDirectionComplete(res, true) {
		return GRPCRequest{}, fmt.Errorf("complete gRPC request and response observations are required")
	}
	if err := artifact.ValidateGRPCObservation(flow); err != nil {
		return GRPCRequest{}, err
	}
	if flow.TransportError != "" || res.StatusCode < 0 || res.StatusCode > 16 || res.StatusSource != "headers" && res.StatusSource != "trailers" {
		return GRPCRequest{}, fmt.Errorf("an observed terminal gRPC status is required")
	}
	if len(req.Trailers) != 0 {
		return GRPCRequest{}, fmt.Errorf("Postman v3 cannot represent request trailers")
	}
	method := strings.TrimPrefix(req.Procedure, "/")
	if method == "" || strings.Count(method, "/") != 1 {
		return GRPCRequest{}, fmt.Errorf("a fully qualified gRPC method is required")
	}
	metadata, err := grpcHeaders(req.Metadata)
	if err != nil {
		return GRPCRequest{}, fmt.Errorf("request metadata: %w", err)
	}
	call := GRPCCall{MethodPath: method, Metadata: metadata}
	type orderedMessage struct {
		sequence uint64
		message  GRPCMessage
	}
	ordered := []orderedMessage{}
	for direction, message := range []*artifact.GrpcMessage{req, res} {
		for _, payload := range message.Messages {
			if payload.DecodeState != "decoded" || payload.DecodeError != "" || payload.Body == nil || payload.Body.Truncated || payload.Body.Encoding != "json" || payload.Observation == nil {
				return GRPCRequest{}, fmt.Errorf("all gRPC messages need complete decoded JSON projections")
			}
			// UseNumber avoids losing precision in protobuf JSON numeric fields.
			decoder := json.NewDecoder(strings.NewReader(payload.Body.Text))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				return GRPCRequest{}, fmt.Errorf("invalid decoded gRPC JSON: %w", err)
			}
			if decoder.Decode(new(any)) != io.EOF {
				return GRPCRequest{}, fmt.Errorf("trailing decoded gRPC JSON")
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return GRPCRequest{}, err
			}
			kind := "outgoing"
			if direction == 1 {
				kind = "incoming"
			} else if call.Message == nil {
				call.Message = &GRPCContent{Content: string(encoded)}
			}
			ordered = append(ordered, orderedMessage{sequence: payload.Observation.CompleteSequence, message: GRPCMessage{Type: kind, Content: string(encoded)}})
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].sequence < ordered[j].sequence })
	messages := []GRPCMessage{}
	for i, item := range ordered {
		item.message.ID = fmt.Sprintf("message-%d", i+1)
		messages = append(messages, item.message)
	}
	responseMetadata := cloneGRPCHeaders(res.Metadata)
	trailers := cloneGRPCHeaders(res.Trailers)
	if status := res.StatusDetails; status != nil {
		if status.State == "truncated" || status.State == "redacted" || status.Wire == nil || status.Wire.Truncated {
			return GRPCRequest{}, fmt.Errorf("complete retained grpc-status-details-bin is required")
		}
		target := trailers
		if status.Source == "headers" {
			target = responseMetadata
		}
		target["grpc-status-details-bin"] = []string{status.Wire.Bytes}
	}
	// Capture stores a decoded convenience scalar separately. Preserve it when
	// its original metadata was not retained, using gRPC's percent encoding.
	if res.StatusMessage != "" && !hasGRPCHeader(responseMetadata, "grpc-message") && !hasGRPCHeader(trailers, "grpc-message") {
		target := trailers
		if res.StatusSource == "headers" {
			target = responseMetadata
		}
		target["grpc-message"] = []string{url.PathEscape(res.StatusMessage)}
	}
	for _, target := range []artifact.Headers{responseMetadata, trailers} {
		for name, values := range target {
			if strings.EqualFold(name, "grpc-status") {
				if len(values) != 1 || values[0] != strconv.Itoa(res.StatusCode) {
					return GRPCRequest{}, fmt.Errorf("grpc-status metadata disagrees with terminal status")
				}
			}
		}
	}
	responseHeaders, err := grpcHeaders(responseMetadata)
	if err != nil {
		return GRPCRequest{}, fmt.Errorf("response metadata: %w", err)
	}
	responseTrailers, err := grpcHeaders(trailers)
	if err != nil {
		return GRPCRequest{}, fmt.Errorf("response trailers: %w", err)
	}
	example := GRPCExample{Kind: "grpc-example", Name: "Captured interaction", Request: call, Response: GRPCResponse{Code: res.StatusCode, Metadata: responseHeaders, Trailers: responseTrailers, Messages: messages}}
	return GRPCRequest{Kind: "grpc-request", ID: fmt.Sprintf("step-%d", ordinal), Name: method, GRPCCall: call, Examples: []GRPCExample{example}}, nil
}

func cloneGRPCHeaders(headers artifact.Headers) artifact.Headers {
	result := artifact.Headers{}
	for key, values := range headers {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func hasGRPCHeader(headers artifact.Headers, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func grpcHeaders(source artifact.Headers) ([]Header, error) {
	result := headers(source, nil)
	for i := range result {
		result[i].Key = strings.ToLower(result[i].Key)
		if strings.HasSuffix(result[i].Key, "-bin") {
			// Native HTTP/2 headers contain base64 already, never raw binary.
			// A proxy may coalesce multiple binary values with commas.
			parts := strings.Split(result[i].Value, ",")
			for n, part := range parts {
				part = strings.TrimSpace(part)
				raw, err := base64.StdEncoding.DecodeString(part)
				if err != nil {
					raw, err = base64.RawStdEncoding.DecodeString(part)
				}
				if err != nil {
					return nil, fmt.Errorf("binary metadata %q is not base64", result[i].Key)
				}
				parts[n] = base64.StdEncoding.EncodeToString(raw)
			}
			result[i].Value = strings.Join(parts, ",")
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}
