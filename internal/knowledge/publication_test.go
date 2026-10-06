package knowledge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func writeJSON(t *testing.T, p string, v any) {
	t.Helper()
	b, err := Canonical(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
}
func readJSON(t *testing.T, p string) Object {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return object(v)
}
func registerCapture(t *testing.T, w *bundle.Writer, refs []artifact.SchemaRef, flow []artifact.Flow) {
	t.Helper()
	tc := artifact.TestCase{ID: "attempt-1", Name: "checkout", StartedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 10, 6, 0, 0, 1, 0, time.UTC), Status: "pass"}
	frozen, err := w.Register(tc, refs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit(&artifact.Artifact{Version: 3, TestCase: tc, Schemas: frozen, Traffic: flow, Meta: map[string]any{"precise": json.Number("9007199254740993")}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
}
func contractConfig() Config {
	c := configFixture("checkout")
	c.Subjects = append(c.Subjects, Object{"id": "api:shop", "kind": "provided-api", "key": "shop", "title": "Shop API", "parent_id": "application", "contract_ids": []any{"contract:shop-v1"}}, Object{"id": "op:shop:checkout", "kind": "operation", "key": "checkout", "title": "Checkout", "parent_id": "api:shop", "protocol": "http", "operation": "POST /checkout", "contract_ids": []any{"contract:shop-v1"}})
	c.Contracts = []ContractMapping{{SchemaID: "shop", ID: "contract:shop-v1", Key: "shop-v1", SubjectIDs: []string{"api:shop", "op:shop:checkout"}, Origin: "supplied"}}
	return c
}
func openAPIArchive(t *testing.T, root, description string) string {
	t.Helper()
	os.MkdirAll(filepath.Join(root, "schemas/a"), 0755)
	os.MkdirAll(filepath.Join(root, "schemas/b"), 0755)
	os.WriteFile(filepath.Join(root, "schemas/a/item.json"), []byte(`{"type":"string","description":"`+description+`"}`), 0644)
	os.WriteFile(filepath.Join(root, "schemas/b/item.json"), []byte(`{"type":"integer","maximum":9007199254740993}`), 0644)
	spec := `{"openapi":"3.0.3","info":{"title":"Shop","version":"1"},"paths":{"/checkout":{"post":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"a/item.json"}}}}}}}},"components":{"schemas":{"Other":{"$ref":"b/item.json"}}}}`
	p := filepath.Join(root, "schemas/shop.json")
	os.WriteFile(p, []byte(spec), 0644)
	w, err := bundle.NewAt(filepath.Join(root, "run"))
	if err != nil {
		t.Fatal(err)
	}
	registerCapture(t, w, []artifact.SchemaRef{{ID: "shop", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "file", Path: p}}}, []artifact.Flow{{ID: "flow-1", Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://shop/checkout", Body: &artifact.Body{Text: `{"n":9007199254740993}`, Encoding: "json"}}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Body: &artifact.Body{Bytes: base64.StdEncoding.EncodeToString([]byte{0, 255, 1}), Encoding: "base64", Truncated: true, OriginalBytes: 10}}}}})
	return w.Path()
}
func TestContractImportsStableAcrossCheckoutsAndRevisions(t *testing.T) {
	base := t.TempDir()
	a := openAPIArchive(t, filepath.Join(base, "checkout-a"), "same")
	b := openAPIArchive(t, filepath.Join(base, "checkout-b"), "same")
	c := contractConfig()
	oa, ob := filepath.Join(base, "ka"), filepath.Join(base, "kb")
	if _, err := Publish(a, oa, c, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(b, ob, c, false); err != nil {
		t.Fatal(err)
	}
	fa, _ := snapshot(oa)
	fb, _ := snapshot(ob)
	for p, data := range fa {
		if strings.HasPrefix(p, "contracts/") && !bytes.Equal(data, fb[p]) {
			t.Fatalf("checkout location changed contract %s\n%s\n%s", p, data, fb[p])
		}
	}
	for _, p := range []string{"contracts/shop-v1/shop.json", "contracts/shop-v1/a/item.json", "contracts/shop-v1/b/item.json"} {
		if fa[p] == nil {
			t.Fatalf("readable source missing: %s", p)
		}
	}
	newRun := openAPIArchive(t, filepath.Join(base, "changed"), "changed")
	result, err := Publish(newRun, oa, c, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range result.Changes {
		if strings.HasSuffix(change.Path, ".md") {
			t.Fatalf("contract edit changed page: %+v", change)
		}
	}
	record := readJSON(t, filepath.Join(oa, "evidence/tests/checkout/primary/recording.json"))
	capture := object(record["capture"])
	if str(object(capture["meta"])["precise"]) != "" {
		t.Fatal("number became a string")
	}
	if object(capture["meta"])["precise"].(json.Number).String() != "9007199254740993" {
		t.Fatal("numeric precision lost")
	}
	flow := object(list(capture["traffic"])[0])
	body := object(object(flow["response"])["body"])
	if body["bytes"] != base64.StdEncoding.EncodeToString([]byte{0, 255, 1}) || body["truncated"] != true {
		t.Fatal("binary/truncation data lost")
	}
}
func TestPartialSelectionPreservesOldSourcesAndEvidence(t *testing.T) {
	base := t.TempDir()
	oldRun := captureFixture(t, filepath.Join(base, "old"), "checkout", "refund")
	output := filepath.Join(base, "knowledge")
	c := configFixture("checkout", "refund")
	if _, err := Publish(oldRun, output, c, false); err != nil {
		t.Fatal(err)
	}
	before, _ := snapshot(output)
	newRun := captureFixture(t, filepath.Join(base, "new"), "checkout")
	c.Selection = Object{"mode": "replace-selected-tests", "scope": "partial", "test_ids": []any{"test:checkout"}, "reason": "checkout changes only"}
	c.Sources = []Object{{"id": "new-app-target", "role": "application", "state": "known", "revision": "new-revision"}, {"id": "new-tests-target", "role": "tests", "state": "known", "revision": "new-revision"}}
	c.TargetSourceIDs = []string{"new-app-target", "new-tests-target"}
	if _, err := Publish(newRun, output, c, false); err != nil {
		t.Fatal(err)
	}
	after, _ := snapshot(output)
	for p, b := range before {
		if strings.Contains(p, "/refund/") && !bytes.Equal(b, after[p]) {
			t.Fatalf("unselected test changed: %s", p)
		}
	}
	oldAttempt := readJSON(t, filepath.Join(output, "evidence/tests/refund/primary/attempt.json"))
	if list(oldAttempt["source_ids"])[0] == "new-app-target" {
		t.Fatal("old evidence relabelled as new target")
	}
}
func TestUnknownFieldsAndMappingsSurvivePublication(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	c := configFixture("checkout")
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, filepath.Join(output, "bundle.json"))
	m["future_field"] = Object{"value": "keep", "decimal": json.Number("-1.25"), "source_ids": []any{"z", "a"}, "attempt_ordinal": json.Number("0.125")}
	object(list(m["subjects"])[0])["future_subject"] = true
	writeJSON(t, filepath.Join(output, "bundle.json"), m)
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	m = readJSON(t, filepath.Join(output, "bundle.json"))
	if object(m["future_field"])["value"] != "keep" || object(m["future_field"])["decimal"] != json.Number("-1.25") || list(object(m["future_field"])["source_ids"])[0] != "z" || object(list(m["subjects"])[0])["future_subject"] != true {
		t.Fatal("unknown fields dropped")
	}
	c.Subjects[1]["title"] = "New display title"
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, "tests/checkout/test.md")); err != nil {
		t.Fatal("title changed path")
	}
}
func TestExplicitMigrationAndTombstone(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	c := configFixture("checkout")
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	c.Subjects[1]["key"] = "buy"
	c.Migrations = []Object{{"subject_id": "test:checkout", "from": "tests/checkout/test.md", "to": "tests/buy/test.md", "reason": "Reviewed terminology change"}}
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(input, output, c, true); err != nil {
		t.Fatal("migration is not repeatable:", err)
	}
	b, _ := os.ReadFile(filepath.Join(output, "tests/checkout/test.md"))
	if !bytes.Contains(b, []byte("deprecated")) {
		t.Fatal("no forwarding concept")
	}
	c.Migrations = nil
	c.Subjects = c.Subjects[:1]
	c.Selection = Object{"mode": "replace-selected-tests", "scope": "complete", "test_ids": []any{}}
	c.RemovedSubjects = []Object{{"id": "test:checkout", "kind": "test", "key": "buy", "path": "tests/buy/test.md", "reason": "Explicit test retirement"}}
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
}
func TestUnavailableSelectionReplacesPriorPass(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "old"), "checkout")
	output := filepath.Join(base, "knowledge")
	c := configFixture("checkout")
	if _, err := Publish(input, output, c, false); err != nil {
		t.Fatal(err)
	}
	empty := captureFixture(t, filepath.Join(base, "empty"))
	if _, err := Publish(empty, output, c, false); err != nil {
		t.Fatal(err)
	}
	a := readJSON(t, filepath.Join(output, "evidence/tests/checkout/primary/attempt.json"))
	if a["outcome"] != "unknown" || a["capture_state"] != "unavailable" || len(list(a["artifacts"])) != 0 {
		t.Fatal("previous pass reused")
	}
}
func TestPathsAndConcurrentPublicationAreRejected(t *testing.T) {
	for _, p := range []string{"../escape", "/absolute", "x\\y", "CON.json", "a/../b", "a.", strings.Repeat("x", 129), "x?q", "x#f"} {
		if safePath(p) {
			t.Errorf("accepted %s", p)
		}
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "note.md"), []byte("---\ntype: Note\n---\n"), 0644)
	if err := os.Symlink("note.md", filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}
	if Validate(root).Application != "invalid" {
		t.Fatal("symlink accepted")
	}
	os.Remove(filepath.Join(root, "alias.md"))
	if err := os.Link(filepath.Join(root, "note.md"), filepath.Join(root, "hard.md")); err != nil {
		t.Fatal(err)
	}
	if Validate(root).Application != "invalid" {
		t.Fatal("hard link accepted")
	}
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output, _ := realParentPath(filepath.Join(base, "knowledge"))
	os.WriteFile(output+".wiretap-lock", []byte("another publisher"), 0600)
	if _, err := Publish(input, output, configFixture("checkout"), false); err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatal("concurrent publisher accepted")
	}
}
func TestGRPCRecordingRetainsWireAndVerifiesProjection(t *testing.T) {
	base := t.TempDir()
	w, err := bundle.NewAt(filepath.Join(base, "run"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(grpc_health_v1.File_grpc_health_v1_health_proto)}})
	wire, _ := proto.Marshal(&grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING})
	req := &artifact.GrpcMessage{Kind: "grpc", Procedure: "/grpc.health.v1.Health/Check", Service: "grpc.health.v1.Health", Method: "Check", SchemaRef: "shop", Messages: []artifact.GrpcPayload{{Index: 0, Body: &artifact.Body{Encoding: "json", Text: `{}`}, Wire: &artifact.Body{Encoding: "base64"}}}}
	resp := &artifact.GrpcMessage{Kind: "grpc", Procedure: req.Procedure, Service: req.Service, Method: req.Method, SchemaRef: "shop", HTTPStatus: 200, Messages: []artifact.GrpcPayload{{Index: 0, Body: &artifact.Body{Encoding: "json", Text: `{"status":"SERVING"}`}, Wire: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(wire)}}}}
	registerCapture(t, w, []artifact.SchemaRef{{ID: "shop", AssetID: "shop", Role: "sut", Type: "protobuf", Services: []string{"grpc.health.v1.Health"}, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(raw)}}}, []artifact.Flow{{ID: "flow-1", Protocol: "grpc", Channel: "test_to_sut", Request: artifact.Message{GRPC: req}, Response: artifact.Message{GRPC: resp}}})
	output := filepath.Join(base, "knowledge")
	if _, err := Publish(w.Path(), output, contractConfig(), false); err != nil {
		t.Fatal(err)
	}
	record := readJSON(t, filepath.Join(output, "evidence/tests/checkout/primary/recording.json"))
	data, _ := Canonical(record)
	if !bytes.Contains(data, []byte(base64.StdEncoding.EncodeToString(wire))) {
		t.Fatal("protobuf wire bytes lost")
	}
	flow := object(list(object(record["capture"])["traffic"])[0])
	payload := object(list(object(flow["response"])["messages"])[0])
	object(payload["body"])["text"] = `{"status":"NOT_SERVING"}`
	envelope := readJSON(t, filepath.Join(output, "evidence/tests/checkout/primary/attempt.json"))
	manifest := readJSON(t, filepath.Join(output, "bundle.json"))
	f, _ := snapshot(output)
	reg := map[string]Object{}
	for _, x := range list(manifest["files"]) {
		x := object(x)
		reg[str(x["path"])] = x
	}
	binding := object(list(envelope["contracts"])[0])
	data, _ = Canonical(record)
	if err := validateRecording(data, envelope, map[string]string{str(binding["contract_id"]): str(binding["manifest_sha256"])}, reg, f); err == nil {
		t.Fatal("forged projection accepted with unchanged source wire")
	}
}

func TestEventRecordingPreservesOrderAndAvailability(t *testing.T) {
	base := t.TempDir()
	w, err := bundle.NewAt(filepath.Join(base, "run"))
	if err != nil {
		t.Fatal(err)
	}
	interaction := &artifact.Interaction{Kind: "stream", Boundary: artifact.BoundaryRef{Role: "dependency", ID: "broker"}, Transport: "kafka", Operation: "orders", CaptureState: "incomplete", StreamMode: "subscription", Events: []artifact.InteractionEvent{
		{ID: "event-0", Sequence: 0, Direction: "received", DirectionSequence: 0, Kind: "message", Payloads: []artifact.EventPayload{{Name: "value", Encoding: "avro", Availability: "present", Body: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString([]byte{0, 255, 1})}}}},
		{ID: "event-1", Sequence: 1, Direction: "sent", DirectionSequence: 0, Kind: "ack"},
		{ID: "event-2", Sequence: 2, Direction: "received", DirectionSequence: 1, Kind: "message", Payloads: []artifact.EventPayload{{Name: "value", Encoding: "avro", Availability: "null"}}},
	}}
	registerCapture(t, w, nil, []artifact.Flow{{ID: "flow-1", Protocol: "avro", Interaction: interaction}})
	c := configFixture("checkout")
	c.Subjects = append(c.Subjects, Object{"id": "dep:broker", "kind": "dependency", "key": "broker", "title": "Broker", "parent_id": "application"})
	output := filepath.Join(base, "knowledge")
	if _, err := Publish(w.Path(), output, c, false); err != nil {
		t.Fatal(err)
	}
	r := readJSON(t, filepath.Join(output, "evidence/tests/checkout/primary/recording.json"))
	f := object(list(object(r["capture"])["traffic"])[0])
	events := list(object(f["interaction"])["events"])
	if len(events) != 3 || object(events[1])["id"] != "event-1" || object(list(object(events[2])["payloads"])[0])["availability"] != "null" {
		t.Fatal("event order or tombstone distinction lost")
	}
}

func TestSelectedFinishedAndInterruptedRetriesAreBothRetained(t *testing.T) {
	base := t.TempDir()
	w, err := bundle.NewAt(filepath.Join(base, "run"))
	if err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt-fail", Name: "checkout", StartedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 10, 6, 0, 0, 1, 0, time.UTC), Status: "fail"}
	if _, err := w.Register(tc, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit(&artifact.Artifact{Version: 3, TestCase: tc, Traffic: []artifact.Flow{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Register(artifact.TestCase{ID: "attempt-interrupted", Name: "checkout", StartedAt: tc.FinishedAt}, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Finalize("interrupted"); err != nil {
		t.Fatal(err)
	}
	c := configFixture("checkout")
	c.Tests[0].AttemptOrdinals = map[string]int{"attempt-fail": 0, "attempt-interrupted": 1}
	output := filepath.Join(base, "knowledge")
	if _, err := Publish(w.Path(), output, c, false); err != nil {
		t.Fatal(err)
	}
	first := readJSON(t, filepath.Join(output, "evidence/tests/checkout/primary/attempt.json"))
	retry := readJSON(t, filepath.Join(output, "evidence/tests/checkout/retry-1/attempt.json"))
	if first["outcome"] != "fail" || retry["outcome"] != "unknown" || retry["attempt_id"] != "attempt-interrupted" {
		t.Fatal("failed/interrupted attempts lost")
	}
}

func TestRelocatingTheSameArchiveHasIdenticalOutput(t *testing.T) {
	base := t.TempDir()
	input := openAPIArchive(t, filepath.Join(base, "original"), "same")
	copyInput := filepath.Join(base, "moved")
	source, _ := snapshot(input)
	for p, b := range source {
		if err := stageFile(copyInput, p, b); err != nil {
			t.Fatal(err)
		}
	}
	c := contractConfig()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	if _, err := Publish(input, a, c, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(copyInput, b, c, false); err != nil {
		t.Fatal(err)
	}
	left, _ := snapshot(a)
	right, _ := snapshot(b)
	if !sameSnapshot(left, right) {
		t.Fatal("absolute archive location changed publication")
	}
}

func TestUnsupportedProfilesRemainDistinctFromIntegrity(t *testing.T) {
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	if _, err := Publish(input, output, configFixture("checkout"), false); err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, filepath.Join(output, "bundle.json"))
	m["required_capabilities"] = []any{"test-result.v1", "urn:example:unsupported"}
	writeJSON(t, filepath.Join(output, "bundle.json"), m)
	v := Validate(output)
	if v.BaseOKF != "valid" || v.Application != "unsupported" {
		t.Fatalf("%+v", v)
	}
	m["profile"] = Object{"id": "application-knowledge", "version": "2.0.0"}
	writeJSON(t, filepath.Join(output, "bundle.json"), m)
	v = Validate(output)
	if v.BaseOKF != "valid" || v.Application != "unsupported" {
		t.Fatalf("%+v", v)
	}
}

// UPDATE_WIRETAP_HTTP_EXAMPLE=1 updates the public protocol fixture using the
// actual producer. Its declared archive identity and inline contract are fixed.
func TestPublicHTTPRecordingExample(t *testing.T) {
	base := t.TempDir()
	w, err := bundle.NewAt(filepath.Join(base, "run"))
	if err != nil {
		t.Fatal(err)
	}
	spec := `{"openapi":"3.0.3","info":{"title":"Shop","version":"1"},"paths":{"/checkout":{"post":{"responses":{"200":{"description":"ok"}}}}}}`
	registerCapture(t, w, []artifact.SchemaRef{{ID: "shop", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: spec}}}, []artifact.Flow{{ID: "flow-1", Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://example.test/checkout", Headers: artifact.Headers{"content-type": []string{"application/json"}}, Body: &artifact.Body{Encoding: "json", Text: `{"item":"book"}`}}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"content-type": []string{"application/json"}}, Body: &artifact.Body{Encoding: "json", Text: `{"accepted":true}`}}}}})
	run := readJSON(t, filepath.Join(w.Path(), "bundle.json"))
	run["run_id"] = "example-wiretap-http"
	attemptRef := object(list(run["tests"])[0])
	attemptPath := filepath.Join(w.Path(), str(attemptRef["path"]))
	attempt := readJSON(t, attemptPath)
	attempt["run_id"] = "example-wiretap-http"
	writeJSON(t, attemptPath, attempt)
	bytes, _ := os.ReadFile(attemptPath)
	attemptRef["sha256"] = hash(bytes)
	writeJSON(t, filepath.Join(w.Path(), "bundle.json"), run)
	output := filepath.Join(base, "knowledge")
	if os.Getenv("UPDATE_WIRETAP_HTTP_EXAMPLE") == "1" {
		output, err = filepath.Abs("../../docs/spec/examples/wiretap-http")
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Publish(w.Path(), output, contractConfig(), false); err != nil {
		t.Fatal(err)
	}
	if v := Validate(output); !v.Valid() {
		t.Fatalf("%+v", v)
	}
}

func TestRetainedAttemptRequiresDistinctContractRevisionSlot(t *testing.T) {
	base := t.TempDir()
	oldRun := openAPIArchive(t, filepath.Join(base, "old"), "old")
	output := filepath.Join(base, "knowledge")
	c := contractConfig()
	if _, err := Publish(oldRun, output, c, false); err != nil {
		t.Fatal(err)
	}
	newRun := openAPIArchive(t, filepath.Join(base, "new"), "new")
	c.Subjects = append(c.Subjects, Object{"id": "test:refund", "kind": "test", "key": "refund", "title": "Refund", "parent_id": "application"})
	c.Tests = []TestMapping{{ID: "test:refund", Name: "checkout"}}
	c.Selection = Object{"mode": "replace-selected-tests", "scope": "partial", "test_ids": []any{"test:refund"}, "reason": "Keep the original checkout observation"}
	before, _ := snapshot(output)
	if _, err := Publish(newRun, output, c, false); err == nil || !strings.Contains(err.Error(), "retained evidence") {
		t.Fatalf("old contract was silently overwritten: %v", err)
	}
	after, _ := snapshot(output)
	if !sameSnapshot(before, after) {
		t.Fatal("failed publication changed prior bytes")
	}
	c.Contracts[0].ID = "contract:shop-v2"
	c.Contracts[0].Key = "shop-v2"
	if _, err := Publish(newRun, output, c, false); err != nil {
		t.Fatal(err)
	}
	after, _ = snapshot(output)
	if !bytes.Equal(before["contracts/shop-v1/manifest.json"], after["contracts/shop-v1/manifest.json"]) || !bytes.Equal(before["evidence/tests/checkout/primary/attempt.json"], after["evidence/tests/checkout/primary/attempt.json"]) {
		t.Fatal("old contract/observation changed")
	}
	if after["contracts/shop-v2/manifest.json"] == nil {
		t.Fatal("new revision missing")
	}
}

func TestBaseReservedFilesAndOptionalNarrativeLinks(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		valid            bool
	}{
		{"frontmatter-only", "concept.md", "---\ntype: Unfamiliar\n---", true},
		{"nested-index", "group/index.md", "---\ntype: Listing\n---\n# Bad index\n", false},
		{"bad-log-date", "log.md", "# Log\n## Yesterday\n- Changed.\n", false},
		{"log", "log.md", "# Log\n## 2026-10-07\n- Changed.\n\n## 2026-10-06\n- Created.\n", true},
		{"duplicate-yaml", "concept.md", "---\ntype: A\ntype: B\n---\n", false},
		{"unsafe-tag", "concept.md", "---\ntype: A\nextra: !Execute anything\n---\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, tc.path)
			os.MkdirAll(filepath.Dir(p), 0755)
			os.WriteFile(p, []byte(tc.body), 0644)
			v := Validate(root)
			if (v.BaseOKF == "valid") != tc.valid {
				t.Fatalf("%+v", v)
			}
		})
	}
	base := t.TempDir()
	input := captureFixture(t, filepath.Join(base, "run"), "checkout")
	output := filepath.Join(base, "knowledge")
	if _, err := Publish(input, output, configFixture("checkout"), false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(output, "system.md")
	b, _ := os.ReadFile(p)
	b = append(b, []byte("\n[Unresolved narrative](future-note.md)\n")...)
	os.WriteFile(p, b, 0644)
	m := readJSON(t, filepath.Join(output, "bundle.json"))
	for _, file := range list(m["files"]) {
		file := object(file)
		if file["path"] == "system.md" {
			file["sha256"] = hash(b)
			file["ownership"] = "authored"
		}
	}
	writeJSON(t, filepath.Join(output, "bundle.json"), m)
	if v := Validate(output); !v.Valid() {
		t.Fatalf("optional narrative rejected: %+v", v)
	}
}
