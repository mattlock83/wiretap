package postman

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

func TestAllAttemptsRetainedWithUniqueNames(t *testing.T) {
	f, s := grpcFixture(t, "Echo")
	a := capture("first", "retry scenario", f.StartedAt, []artifact.Flow{f})
	a.Schemas = []artifact.SchemaRef{s}
	b := capture("second", "retry scenario", f.StartedAt, []artifact.Flow{f})
	b.Schemas = []artifact.SchemaRef{s}
	c := capture("third", "retry scenario [attempt first]", f.StartedAt, []artifact.Flow{f})
	c.Schemas = []artifact.SchemaRef{s}
	root := configuredBundle(t, bundle.Identity{}, a, b, c)
	before := bundleFiles(t, root)
	exports, err := GenerateAll(root, "")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	ids := map[string]bool{}
	for _, folder := range exports[0].GRPC.Items {
		if names[folder.Name] || ids[folder.ID] {
			t.Fatal("retry identity collapsed")
		}
		names[folder.Name] = true
		ids[folder.ID] = true
	}
	if len(names) != 3 || !ids["first"] || !ids["second"] {
		t.Fatalf("attempts lost: %v", ids)
	}
	if !reflect.DeepEqual(before, bundleFiles(t, root)) {
		t.Fatal("raw archive changed")
	}
}

func TestOmitBadFlowKeepsOtherProtocolsAndDiagnostics(t *testing.T) {
	bad, s := grpcFixture(t, "Echo")
	bad.Response.GRPC.CaptureTruncated = true
	good, _ := grpcFixture(t, "Watch")
	good.ID = "good-grpc"
	http := httpFlow("http", "http://sut/", 200, bad.StartedAt)
	c := capture("attempt", "mixed", bad.StartedAt, []artifact.Flow{bad, good, http})
	c.Schemas = []artifact.SchemaRef{s}
	root := configuredBundle(t, bundle.Identity{}, c)
	before := bundleFiles(t, root)
	result, err := GenerateAllWithOptions(root, "", Options{})
	if err == nil || len(result.Diagnostics) != 1 || len(result.Exports) != 0 {
		t.Fatalf("strict export: %+v %v", result, err)
	}
	result, err = GenerateAllWithOptions(root, "", Options{OmitIncomplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Exports) != 2 || len(result.Diagnostics) != 1 || result.Diagnostics[0].FlowID != bad.ID || len(result.Exports[1].GRPC.Items[0].Items) != 1 {
		t.Fatalf("silent omission or other exports lost: %+v", result)
	}
	if !reflect.DeepEqual(before, bundleFiles(t, root)) {
		t.Fatal("omission changed archive")
	}
}

func TestStrictHTTPTruncationAndOmission(t *testing.T) {
	f := httpFlow("truncated", "http://sut/", 200, grpcTime())
	f.Request.HTTP.Body = &artifact.Body{Text: "partial", Truncated: true}
	root := configuredBundle(t, bundle.Identity{}, capture("attempt", "partial", f.StartedAt, []artifact.Flow{f}))
	if _, err := GenerateAll(root, ""); err == nil {
		t.Fatal("strict export replayed truncated HTTP request")
	}
	result, err := GenerateAllWithOptions(root, "", Options{OmitIncomplete: true})
	if err != nil || len(result.Diagnostics) != 1 || len(result.Exports[0].Collection.Items[0].Items) != 0 {
		t.Fatalf("HTTP omission: %+v %v", result, err)
	}
}

func TestGRPCTargetCopiesExplicitSourceAndEndpoint(t *testing.T) {
	flow, schema := grpcFixture(t, "Echo")
	c := capture("attempt", "replay", flow.StartedAt, []artifact.Flow{flow})
	c.Schemas = []artifact.SchemaRef{schema}
	root := configuredBundle(t, bundle.Identity{}, c)
	protoRoot, err := filepath.Abs("../../examples/grpc-streaming/fixture")
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{GRPC: map[string]GRPCTarget{"sut": {URL: "https://grpc.example.test:443", ProtoRoot: protoRoot, ProtoFile: "stream.proto"}}}
	result, err := GenerateAllWithOptions(root, "", opts)
	if err != nil {
		t.Fatal(err)
	}
	export := result.Exports[0]
	request := export.GRPC.Items[0].Items[0]
	if request.URL != "grpc.example.test:443" || request.Settings == nil || !request.Settings.SecureConnection || request.Schema == nil || request.Schema.Source != "file" || request.Examples[0].Request.URL != request.URL {
		t.Fatalf("replay mapping absent: %+v", request)
	}
	found := false
	source, _ := os.ReadFile(filepath.Join(protoRoot, "stream.proto"))
	for _, file := range export.Files {
		if file.Path == request.Schema.Location && bytes.Equal(file.Data, source) {
			found = true
		}
	}
	if !found {
		t.Fatal("source proto not copied exactly")
	}
	var data bytes.Buffer
	if err := WriteExport(&data, export); err != nil {
		t.Fatal(err)
	}
	validateV3(t, data.Bytes())
	for _, target := range []GRPCTarget{{URL: "http://user:secret@localhost:42"}, {ProtoRoot: protoRoot, ProtoFile: "../outside.proto"}, {ProtoFile: "stream.proto"}} {
		if _, err := GenerateAllWithOptions(root, "", Options{GRPC: map[string]GRPCTarget{"sut": target}}); err == nil {
			t.Fatalf("invalid target accepted: %+v", target)
		}
	}
	if _, err := GenerateAllWithOptions(root, "", Options{GRPC: map[string]GRPCTarget{"unknown": {URL: "localhost:42"}}}); err == nil {
		t.Fatal("unmatched mapping silently ignored")
	}
}

func TestReadPostmanOptionsRelativePathsAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "postman.json")
	os.WriteFile(path, []byte(`{"grpc":{"sut":{"proto_root":"protos","proto_file":"echo.proto"}}}`), 0600)
	options, err := ReadOptions(path)
	if err != nil || options.GRPC["sut"].ProtoRoot != filepath.Join(dir, "protos") {
		t.Fatalf("relative config: %+v %v", options, err)
	}
	os.WriteFile(path, []byte(`{"unknown":true}`), 0600)
	if _, err := ReadOptions(path); err == nil {
		t.Fatal("unknown config accepted")
	}
}

// TestWritePostmanParserFixtures exercises the real bundle reader and exporter.
// Node creates a temporary output directory and asks this test to populate it.
func TestWritePostmanParserFixtures(t *testing.T) {
	dir := os.Getenv("WIRETAP_POSTMAN_FIXTURE_DIR")
	if dir == "" {
		t.Skip("Node compatibility harness supplies fixture output directory")
	}
	var captures []*artifact.Artifact
	for _, method := range []string{"Echo", "Watch", "Collect", "Chat"} {
		f, s := grpcFixture(t, method)
		c := capture("attempt-"+method, method, f.StartedAt, []artifact.Flow{f})
		c.Schemas = []artifact.SchemaRef{s}
		captures = append(captures, c)
	}
	errorFlow, s := grpcFixture(t, "Echo")
	res := errorFlow.Response.GRPC
	res.StatusCode = 3
	res.StatusMessage = "invalid argument"
	res.Messages = nil
	res.ObservedBytes = 0
	res.Observation.ObservedBytes = 0
	res.Observation.CompleteFrames = 0
	res.Observation.RetainedFrames = 0
	res.Trailers["grpc-status"] = []string{"3"}
	res.Trailers["grpc-status-details-bin"] = []string{base64.StdEncoding.EncodeToString([]byte{8, 3, 18, 3, 'b', 'a', 'd'})}
	c := capture("attempt-error", "error", errorFlow.StartedAt, []artifact.Flow{errorFlow})
	c.Schemas = []artifact.SchemaRef{s}
	captures = append(captures, c)
	retry := *captures[0]
	retry.TestCase.ID = "attempt-retry"
	captures = append(captures, &retry)
	http := httpFlow("binary-http", "http://sut:8080/bytes?x=a&x=b&encoded=%2F", 201, errorFlow.StartedAt)
	http.Request.HTTP.Method = "POST"
	http.Request.HTTP.Headers = artifact.Headers{"X-Repeated": {"one", "two"}, "Content-Type": {"application/octet-stream"}}
	http.Request.HTTP.Body = &artifact.Body{Encoding: "base64", Bytes: "AP8BgA=="}
	http.Response.HTTP.Body = &artifact.Body{Encoding: "base64", Bytes: "/wAB"}
	http.Response.HTTP.Headers = artifact.Headers{"Set-Cookie": {"a=1", "b=2"}}
	captures = append(captures, capture("attempt-http", "HTTP binary", http.StartedAt, []artifact.Flow{http}))
	root := configuredBundle(t, bundle.Identity{Application: &bundle.Application{ID: "compat", Name: "Compatibility"}}, captures...)
	protoRoot, _ := filepath.Abs("../../examples/grpc-streaming/fixture")
	result, err := GenerateAllWithOptions(root, "", Options{GRPC: map[string]GRPCTarget{"sut": {URL: "http://localhost:50051", ProtoRoot: protoRoot, ProtoFile: "stream.proto"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, export := range result.Exports {
		var data bytes.Buffer
		if err := WriteExport(&data, export); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, export.Filename), data.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		for _, file := range export.Files {
			path := filepath.Join(dir, filepath.FromSlash(file.Path))
			os.MkdirAll(filepath.Dir(path), 0700)
			if err := os.WriteFile(path, file.Data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func grpcTime() (v time.Time) { return time.Date(2026, 10, 24, 1, 2, 3, 0, time.UTC) }

func TestOmittedEntireGRPCBoundaryKeepsHealthyHTTPWithTargetMapping(t *testing.T) {
	for _, dependency := range []bool{false, true} {
		t.Run(map[bool]string{false: "sut", true: "dependency"}[dependency], func(t *testing.T) {
			bad, schema := grpcFixture(t, "Echo")
			bad.Response.GRPC.CaptureTruncated = true
			identity := bundle.Identity{}
			key := "sut"
			if dependency {
				bad.Channel, bad.AssetID = "sut_to_dependency", "inventory"
				schema.Role, schema.AssetID = "dependency", "inventory"
				identity.DependencyKinds = map[string]string{"inventory": "mock"}
				key = "dependency:inventory"
			}
			http := httpFlow("healthy-http", "http://sut/healthy", 200, bad.StartedAt)
			c := capture("attempt", "mixed", bad.StartedAt, []artifact.Flow{bad, http})
			c.Schemas = []artifact.SchemaRef{schema}
			root := configuredBundle(t, identity, c)
			options := Options{OmitIncomplete: true, GRPC: map[string]GRPCTarget{key: {URL: "http://localhost:50051"}}}
			result, err := GenerateAllWithOptions(root, "", options)
			if err != nil {
				t.Fatalf("recognized omitted boundary blocked healthy HTTP: %v", err)
			}
			if len(result.Diagnostics) != 1 || len(result.Exports) != 1 || result.Exports[0].Collection == nil || len(result.Exports[0].Collection.Items[0].Items) != 1 {
				t.Fatalf("healthy export or omission lost: %+v", result)
			}
			options.GRPC = map[string]GRPCTarget{"dependency:typo": {URL: "http://localhost:50051"}}
			if _, err := GenerateAllWithOptions(root, "", options); err == nil {
				t.Fatal("target typo accepted as an omitted boundary")
			}
		})
	}
}

func TestReadPostmanOptionsRejectsOversizedDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "postman.json")
	// A valid object followed by enough whitespace to hide more bytes beyond
	// the old LimitReader EOF must be rejected, not mistaken for a complete file.
	data := "{}" + strings.Repeat(" ", 1<<20) + `{"grpc":{}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOptions(path); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("oversized input accepted: %v", err)
	}
	if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOptions(path); err == nil {
		t.Fatal("null options are not an object")
	}
}

func TestStrictPostmanRefusesOmittedBodyEvenWithoutTruncation(t *testing.T) {
	flow := httpFlow("omitted", "http://sut/", 200, grpcTime())
	flow.Request.HTTP.Body = &artifact.Body{Encoding: "utf8", Omitted: true, Redacted: true, OriginalBytes: 200}
	root := configuredBundle(t, bundle.Identity{}, capture("attempt", "omitted", flow.StartedAt, []artifact.Flow{flow}))
	if _, err := GenerateAll(root, ""); err == nil {
		t.Fatal("replayed an omitted body as empty")
	}
	result, err := GenerateAllWithOptions(root, "", Options{OmitIncomplete: true})
	if err != nil || len(result.Diagnostics) != 1 {
		t.Fatalf("missing omission: %+v %v", result, err)
	}
}
