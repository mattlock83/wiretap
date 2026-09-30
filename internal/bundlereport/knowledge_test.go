package bundlereport

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
)

func knowledgeFixture(t *testing.T) string {
	t.Helper()
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Configure(bundle.Identity{APIID: "orders", SUTTargetKind: "service", DependencyKinds: map[string]string{"stock": "mock", "unused": "unknown"}}); err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt-1", Name: "create order", Status: "pass", StartedAt: time.Now().UTC()}
	refs := []artifact.SchemaRef{
		{ID: "orders", Role: "sut", AssetID: "orders", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.3
info: {title: Orders, version: "1"}
paths:
  /orders:
    post:
      responses:
        "201": {description: created}
  /unobserved:
    get:
      responses:
        "200": {description: ok}
`}},
		{ID: "stock", Role: "dependency", AssetID: "stock", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.3
info: {title: Stock, version: "1"}
paths:
  /stock:
    get:
      responses:
        "200": {description: available}
`}},
	}
	frozen, err := w.Register(tc, refs)
	if err != nil {
		t.Fatal(err)
	}
	flow := func(id, channel, asset, method, path string, status int) artifact.Flow {
		return artifact.Flow{ID: id, Protocol: "http", Channel: channel, AssetID: asset, Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: method, URL: "http://example" + path}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: status}}}
	}
	a := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: frozen, SchemaValidation: &artifact.SchemaValidation{Status: "pass"}, Redactions: []artifact.Redaction{{Field: "request.headers.Authorization", Rule: "remove"}}, Traffic: []artifact.Flow{flow("f1", "test_to_sut", "orders", "POST", "/orders", 201), flow("f2", "sut_to_dependency", "stock", "GET", "/stock", 200), flow("f3", "test_to_sut", "orders", "GET", "/undocumented?token=omitted", 200)}}
	a.Traffic[1].ParentFlowID = "f1"
	a.Traffic[1].ParentCorrelation = "timing"
	a.Traffic[1].TestCorrelation = "active_session"
	if _, err = w.Commit(a); err != nil {
		t.Fatal(err)
	}
	if err = w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	if _, err = Rebuild(w.Path()); err != nil {
		t.Fatal(err)
	}
	return w.Path()
}

func readKnowledge(t *testing.T, root string) knowledgeIndex {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	var k knowledgeIndex
	if err = json.Unmarshal(data, &k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestKnowledgeSeparatesMockClaimsGapsAndUnmatchedTraffic(t *testing.T) {
	root := knowledgeFixture(t)
	k := readKnowledge(t, root)
	var mock, unobserved, unmatched, unused bool
	for _, api := range k.APIs {
		if api.ID == "unused" {
			unused = true
		}
		for _, op := range api.Operations {
			switch op.Operation {
			case "GET /stock":
				mock = true
				if op.ProviderCheck != "not_performed" || op.ConsumedFields != "unknown" || op.EvidenceBasis != "observed_mock" || len(op.Evidence) != 1 || api.ContractRevision == "" {
					t.Fatalf("mock claim overstated or missing context: %+v", op)
				}
				e := op.Evidence[0]
				if e.TestStatus != "pass" || e.ParentCorrelation != "timing" || !e.RedactionsConfigured || e.Path != "evidence/attempts/attempt-1.json" {
					t.Fatalf("evidence qualifiers lost: %+v", e)
				}
			case "GET /unobserved":
				unobserved = true
				if op.EvidenceBasis != "declared_only" || op.ContractMatch != "unobserved" || len(op.Evidence) != 0 {
					t.Fatal("declaration presented as an observation")
				}
			case "GET /undocumented":
				unmatched = true
				if op.Declared || op.ContractMatch != "unmatched" {
					t.Fatal("unknown route presented as declared or missing contract")
				}
			}
		}
	}
	if !mock || !unobserved || !unmatched || !unused {
		t.Fatalf("incomplete inventory: %t %t %t %t", mock, unobserved, unmatched, unused)
	}
	tasks := map[string]bool{}
	for _, task := range k.Verification {
		tasks[task.Kind] = true
		if task.Kind == "supply_contract" && task.APIID == "orders" {
			t.Fatal("unmatched route should reconcile against the supplied contract")
		}
	}
	for _, kind := range []string{"verify_dependency", "unobserved_operation", "reconcile_unmatched_operation", "establish_provenance"} {
		if !tasks[kind] {
			t.Errorf("missing %s task", kind)
		}
	}
	data, _ := os.ReadFile(filepath.Join(root, "knowledge.json"))
	if strings.Contains(string(data), "token=omitted") {
		t.Fatal("index copied query values")
	}
}

func TestKnowledgeExpectedNegativesAndLineageAreNotProviderContradictions(t *testing.T) {
	run := &bundle.Run{RunID: "new-run", APIID: "orders", RunState: "complete", Revision: 1, Attempts: []bundle.AttemptRef{{AttemptID: "negative", FileRef: bundle.FileRef{Path: "evidence/attempts/negative.json"}}}}
	a := &artifact.Artifact{TestCase: artifact.TestCase{ID: "negative", Name: "reject invalid order", Status: "pass"}, Exploration: &artifact.Exploration{Mode: "rejection", SourceRunID: "seed", SourceAttemptID: "happy", SourceSHA256: "digest"}}
	report := &coverage.Report{Contracts: &coverage.ContractCoverage{Specs: []*coverage.ContractSpec{{APIID: "orders", Origin: "provided", Revision: "contract", Violations: []coverage.ContractViolation{{Expected: true, Operation: "POST /orders"}}}}}}
	k := buildKnowledge(t.TempDir(), run, []*artifact.Artifact{a}, report)
	for _, task := range k.Verification {
		if task.Kind == "contract_mismatch" || task.Kind == "investigate_attempt" {
			t.Fatal("expected rejection became a defect")
		}
	}
	if !strings.Contains(strings.Join(k.Scope, " "), "Downstream dependency traffic is not captured") {
		t.Fatal("exploration dependency scope missing")
	}
	if k.Attempts[0].Kind != "generated_rejection" || k.Attempts[0].Exploration.SourceRunID != "seed" || len(k.SUTSource) != 0 {
		t.Fatal("seed lineage lost or promoted to current SUT provenance")
	}
	a.TestCase.Status = "fail"
	a.CaptureIncomplete = true
	k = buildKnowledge(t.TempDir(), run, []*artifact.Artifact{a}, report)
	found := false
	for _, task := range k.Verification {
		if task.Kind == "repair_capture" {
			found = true
		}
	}
	if !found {
		t.Fatal("incomplete evidence omitted from verification work")
	}
}

func TestKnowledgeRebuildPreservesEvidenceAndRejectsBrokenReferences(t *testing.T) {
	root := knowledgeFixture(t)
	digests := func() map[string][32]byte {
		out := map[string][32]byte{}
		for _, dir := range []string{"evidence", "schemas", "payloads"} {
			filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					b, e := os.ReadFile(path)
					if e != nil {
						t.Fatal(e)
					}
					out[path] = sha256.Sum256(b)
				}
				return nil
			})
		}
		return out
	}
	before := digests()
	original, _ := os.ReadFile(filepath.Join(root, "knowledge.json"))
	k := readKnowledge(t, root)
	for i := range k.APIs {
		for j := range k.APIs[i].Operations {
			if len(k.APIs[i].Operations[j].Evidence) > 0 {
				k.APIs[i].Operations[j].Evidence[0].FlowID = "invented"
			}
		}
	}
	data, _ := json.Marshal(k)
	if err := os.WriteFile(filepath.Join(root, "knowledge.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(root); err == nil {
		t.Fatal("invalid evidence reference accepted")
	}
	if _, err := Rebuild(root); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "knowledge.json"))
	if string(original) != string(after) {
		t.Fatal("knowledge rebuild not deterministic")
	}
	for path, sum := range digests() {
		if sum != before[path] {
			t.Fatalf("canonical data changed: %s", path)
		}
	}
}

func TestKnowledgeTransportFailureIsNotAMockResponseAssumption(t *testing.T) {
	run := &bundle.Run{RunID: "run", APIID: "orders", SUTTargetKind: "service", DependencyKinds: map[string]string{"stock": "mock"}, Attempts: []bundle.AttemptRef{{AttemptID: "attempt", FileRef: bundle.FileRef{Path: "evidence/attempts/attempt.json"}}}}
	a := &artifact.Artifact{TestCase: artifact.TestCase{ID: "attempt", Status: "pass"}, Traffic: []artifact.Flow{{ID: "failed", Channel: "sut_to_dependency", AssetID: "stock", TransportError: "connection refused", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://stock/items"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 502}}}}}
	k := buildKnowledge(t.TempDir(), run, []*artifact.Artifact{a}, &coverage.Report{})
	for _, api := range k.APIs {
		if api.ID == "stock" {
			if len(api.Operations) != 1 || len(api.Operations[0].Statuses) != 0 || api.Operations[0].EvidenceBasis != "incomplete_exchange" {
				t.Fatal("proxy 502 promoted to provider behavior")
			}
		}
	}
	found := false
	for _, task := range k.Verification {
		if task.Kind == "verify_dependency" {
			t.Fatal("failed transport became a mock response assumption")
		}
		if task.Kind == "transport_failure" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing transport investigation despite passing containing test")
	}
}

func TestKnowledgeUnusableContractDoesNotInventRouteMismatch(t *testing.T) {
	run := &bundle.Run{RunID: "run", APIID: "orders", SUTTargetKind: "service", Attempts: []bundle.AttemptRef{{AttemptID: "attempt", FileRef: bundle.FileRef{Path: "evidence/attempts/attempt.json"}}}}
	a := &artifact.Artifact{TestCase: artifact.TestCase{ID: "attempt", Status: "pass"}, Traffic: []artifact.Flow{{ID: "f1", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://orders/items"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}}}}
	report := &coverage.Report{Provided: coverage.SpecCoverageGroup{Specs: []coverage.SpecCoverage{{AssetID: "orders", SchemaID: "broken", Source: "schemas/broken.json", DenominatorKnown: false}}}}
	k := buildKnowledge(t.TempDir(), run, []*artifact.Artifact{a}, report)
	repairs := 0
	for _, task := range k.Verification {
		if task.Kind == "reconcile_unmatched_operation" {
			t.Fatal("unusable contract became a route mismatch")
		}
		if task.Kind == "supply_contract" {
			repairs++
		}
	}
	if repairs != 1 {
		t.Fatalf("expected one contract repair task, got %d", repairs)
	}
	for _, api := range k.APIs {
		for _, op := range api.Operations {
			if op.ContractMatch != "not_evaluated" {
				t.Fatal("unusable contract presented as evaluated")
			}
		}
	}
}
