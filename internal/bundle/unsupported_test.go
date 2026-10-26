package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestNativeRPCContractsAndCapturesAreUnsupported(t *testing.T) {
	writer, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "test", Name: "HTTP only", StartedAt: time.Now()}
	if _, err := writer.Register(tc, []artifact.SchemaRef{{ID: "rpc", Role: "sut", Type: "protobuf"}}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("protobuf contract accepted: %v", err)
	}
	if _, err := writer.Register(tc, nil); err != nil {
		t.Fatal(err)
	}
	capture := &artifact.Artifact{Version: 1, TestCase: tc, Traffic: []artifact.Flow{{ID: "rpc", Protocol: "grpc"}}}
	if _, err := writer.Commit(capture); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("native RPC capture accepted: %v", err)
	}
}

func TestNativeRPCManifestEvidenceIsRejected(t *testing.T) {
	writer, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(writer.Path(), "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["CONTRACT_RESOLUTIONS"] = []any{map[string]any{"api_id": "rpc", "role": "sut", "protocol": "grpc", "contract_resolution": "pending"}}
	legacyRaw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManifest(legacyRaw); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("native RPC contract acquisition accepted: %v", err)
	}
	for _, mutate := range []func(*Run){
		func(run *Run) { run.RequiredCapabilities = []string{"grpc-observation.v1"} },
		func(run *Run) { run.Schemas = []artifact.SchemaRef{{ID: "rpc", Role: "sut", Type: "protobuf"}} },
		func(run *Run) { run.ProvidedAPIs = []API{{ID: "rpc", Protocols: []string{"grpc"}}} },
		func(run *Run) { run.ProvidedAPIs = []API{{ID: "rpc", Protocols: []string{" GrPc-WeB "}}} },
		func(run *Run) {
			run.Schemas = []artifact.SchemaRef{{ID: "rpc", Role: "sut", Type: "openapi", Protocol: "grpcs"}}
		},
	} {
		run, err := DecodeManifest(data)
		if err != nil {
			t.Fatal(err)
		}
		mutate(run)
		raw, err := json.Marshal(run)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeManifest(raw); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("unsupported manifest accepted: %v", err)
		}
	}
}

func TestNativeRPCAttachmentSelectorsAreRejected(t *testing.T) {
	for _, raw := range []string{`{"kind":"wire"}`, `{"message_index":0}`, `{"status_detail_index":0}`, `{"Kind":"wire"}`, `{"MESSAGE_INDEX":0}`, `{"Status_Detail_Index":0}`} {
		var ref BodyRef
		if err := json.Unmarshal([]byte(raw), &ref); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("native RPC attachment selector accepted: %s: %v", raw, err)
		}
	}
}
