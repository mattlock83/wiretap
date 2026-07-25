package app

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/capture"
	"github.com/phrond/evermock/internal/mock"
	"github.com/phrond/evermock/internal/proxy"
)

func TestBodyFromHTTPDecompressesGzip(t *testing.T) {
	original := `{"result":"alpha-ok"}`
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(original)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	headers := artifact.Headers{"Content-Encoding": {"gzip"}}
	body := bodyFromHTTP(headers, buf.Bytes())
	if body == nil {
		t.Fatal("expected body, got nil")
	}
	if body.Text != original {
		t.Fatalf("expected decompressed text %q, got %q", original, body.Text)
	}
	if body.Encoding != "utf8" {
		t.Fatalf("expected utf8 encoding, got %q", body.Encoding)
	}
	if body.ContentEncoding != "gzip" {
		t.Fatalf("expected content_encoding gzip, got %q", body.ContentEncoding)
	}
}

func TestBodyFromHTTPUndecodableEncodingKeptRaw(t *testing.T) {
	raw := []byte{0x1f, 0x8b, 0x00, 0x01} // not a real brotli stream
	headers := artifact.Headers{"Content-Encoding": {"br"}}
	body := bodyFromHTTP(headers, raw)
	if body == nil {
		t.Fatal("expected body, got nil")
	}
	// Cannot decode br: stored as base64 but still flagged with the wire encoding.
	if body.Encoding != "base64" {
		t.Fatalf("expected base64 encoding for undecodable payload, got %q", body.Encoding)
	}
	if body.ContentEncoding != "br" {
		t.Fatalf("expected content_encoding br, got %q", body.ContentEncoding)
	}
}

func TestBodyFromHTTPCapsDecompressedPayload(t *testing.T) {
	original := strings.Repeat("expanded-", 100)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(original)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	headers := artifact.Headers{"Content-Encoding": {"gzip"}}
	raw := capture.FromBytes(buf.Bytes(), int64(len(buf.Bytes())))
	body := bodyFromCapturedHTTP(headers, raw, 16)
	if body == nil {
		t.Fatal("expected body")
	}
	if body.Text != original[:16] {
		t.Fatalf("captured decompressed prefix = %q", body.Text)
	}
	if !body.Truncated || body.OriginalBytes != int64(len(original)) {
		t.Fatalf("decompressed capture metadata = %#v", body)
	}
	if body.ContentEncoding != "gzip" {
		t.Fatalf("content encoding = %q", body.ContentEncoding)
	}
}

func TestFlowFromProxyCarriesCaptureMetadata(t *testing.T) {
	store := NewTestCaseStore(t.TempDir(), 4)
	if _, err := store.StartCase("bounded", nil, nil, ""); err != nil {
		t.Fatalf("start case: %v", err)
	}
	flow := store.flowFromProxy(proxy.HTTPExchange{
		Method:                http.MethodPost,
		URL:                   "http://example.test/items",
		RequestBody:           []byte("requ"),
		RequestBodyBytes:      12,
		RequestBodyTruncated:  true,
		Status:                http.StatusOK,
		ResponseBody:          []byte("resp"),
		ResponseBodyBytes:     13,
		ResponseBodyTruncated: true,
	})
	if flow.Request.HTTP == nil || flow.Request.HTTP.Body == nil ||
		!flow.Request.HTTP.Body.Truncated || flow.Request.HTTP.Body.OriginalBytes != 12 {
		t.Fatalf("request body = %#v", flow.Request.HTTP)
	}
	if flow.Response.HTTP == nil || flow.Response.HTTP.Body == nil ||
		!flow.Response.HTTP.Body.Truncated || flow.Response.HTTP.Body.OriginalBytes != 13 {
		t.Fatalf("response body = %#v", flow.Response.HTTP)
	}
	if got := bodyShape(flow.Request.HTTP.Body); got != "truncated" {
		t.Fatalf("body shape = %q", got)
	}
}

func TestSchemaValidationSkipsTruncatedBodies(t *testing.T) {
	spec, err := loadOpenAPISpec(artifact.SchemaRef{
		ID:   "bounded-openapi",
		Type: "openapi",
		Source: artifact.SchemaSource{
			Type: "inline",
			Content: `openapi: 3.0.0
info:
  title: bounded
  version: "1"
paths:
  /items:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [id]
`,
		},
	})
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	flow := artifact.Flow{
		ID:       "flow-1",
		Protocol: "http",
		Channel:  "test_to_sut",
		Request: artifact.Message{HTTP: &artifact.HttpMessage{
			Method:  http.MethodPost,
			URL:     "http://example.test/items",
			Headers: artifact.Headers{"Content-Type": {"application/json"}},
			Body:    &artifact.Body{Encoding: "utf8", Text: `{"cut`, Truncated: true, OriginalBytes: 100},
		}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{
			Status:  http.StatusOK,
			Headers: artifact.Headers{"Content-Type": {"application/json"}},
			Body:    &artifact.Body{Encoding: "utf8", Text: `{"cut`, Truncated: true, OriginalBytes: 100},
		}},
	}
	if issues := validateHTTPFlow(flow, []openapiSpec{spec}); len(issues) != 0 {
		t.Fatalf("truncated body validation issues = %#v", issues)
	}
}

func TestMockStubFromExchangeCapturesAssumption(t *testing.T) {
	exchange := mock.Exchange{
		Matched: true,
		MatchedStub: &mock.Stub{
			ID:     "alpha",
			Source: "queued",
			Match: mock.StubMatch{
				Method: "POST",
				Path:   "/alpha",
				Body:   `{"id":"123"}`,
			},
			Response: mock.StubResponse{
				Status: 200,
				Body:   `{"result":"alpha-ok"}`,
			},
		},
	}

	stub := mockStubFromExchange(exchange)
	if stub == nil {
		t.Fatal("expected mock stub, got nil")
	}
	if stub.Match.Path != "/alpha" {
		t.Fatalf("expected path /alpha, got %q", stub.Match.Path)
	}
	if stub.Source != "queued" {
		t.Fatalf("expected source queued, got %q", stub.Source)
	}
	if stub.ResponseTemplate.Body != `{"result":"alpha-ok"}` {
		t.Fatalf("expected response template, got %q", stub.ResponseTemplate.Body)
	}
	wantOn := map[string]bool{"method": true, "path": true, "body": true}
	if len(stub.MatchedOn) != len(wantOn) {
		t.Fatalf("expected matched_on %v, got %v", wantOn, stub.MatchedOn)
	}
	for _, on := range stub.MatchedOn {
		if !wantOn[on] {
			t.Fatalf("unexpected matched_on entry %q", on)
		}
	}
}

func TestFlowFromMockMarksUnmatched(t *testing.T) {
	store := NewTestCaseStore(t.TempDir())
	flow := store.flowFromMock(mock.Exchange{
		Matched:   false,
		StartedAt: time.Now(),
		Request:   mock.RecordedRequest{Method: "GET", Path: "/unknown"},
		Response:  mock.RecordedResponse{Status: 501},
	})
	if !flow.Unmatched {
		t.Fatal("expected unmatched flow to be flagged")
	}
	if flow.MockStub != nil {
		t.Fatal("expected no mock stub for unmatched flow")
	}
}

func TestFinishCaseAttachesProducer(t *testing.T) {
	store := NewTestCaseStore(t.TempDir())
	store.SetProducer(artifact.Producer{Repo: "acme/widgets", CommitSHA: "abc123", Source: "git"})
	if _, err := store.StartCase("producer-case", nil, nil, ""); err != nil {
		t.Fatalf("start case: %v", err)
	}
	art, _, err := store.FinishCase("pass", nil, nil, nil)
	if err != nil {
		t.Fatalf("finish case: %v", err)
	}
	if art.Producer == nil {
		t.Fatal("expected producer on artifact")
	}
	if art.Producer.Repo != "acme/widgets" || art.Producer.CommitSHA != "abc123" {
		t.Fatalf("unexpected producer: %+v", art.Producer)
	}
	if art.SchemaValidation == nil || art.SchemaValidation.Status != "skipped" {
		t.Fatalf("expected schema validation skipped without schemas, got %+v", art.SchemaValidation)
	}
}

func TestLinkCausalityTraceAndTimeWindow(t *testing.T) {
	base := time.Now().UTC()
	flows := []artifact.Flow{
		{ID: "flow-1", Channel: "test_to_sut", TraceID: "phtr-1", StartedAt: base, DurationMS: 100},
		{ID: "flow-2", Channel: "sut_to_mock", TraceID: "phtr-1", StartedAt: base.Add(200 * time.Millisecond)},
		{ID: "flow-3", Channel: "sut_to_mock", StartedAt: base.Add(50 * time.Millisecond)},
		{ID: "flow-4", Channel: "sut_to_mock", StartedAt: base.Add(5 * time.Second)},
	}
	linkCausality(flows)
	if flows[1].ParentFlowID != "flow-1" {
		t.Fatalf("expected trace-id link to flow-1, got %q", flows[1].ParentFlowID)
	}
	if flows[2].ParentFlowID != "flow-1" {
		t.Fatalf("expected time-window link to flow-1, got %q", flows[2].ParentFlowID)
	}
	if flows[3].ParentFlowID != "" {
		t.Fatalf("expected no parent outside window, got %q", flows[3].ParentFlowID)
	}
}

func TestFlowFingerprintStableAcrossValues(t *testing.T) {
	makeFlow := func(id, qty string) artifact.Flow {
		return artifact.Flow{
			ID:      "flow-x",
			Channel: "sut_to_mock",
			Request: artifact.Message{HTTP: &artifact.HttpMessage{
				Method: "POST",
				URL:    "/orders",
				Body:   &artifact.Body{Encoding: "utf8", Text: `{"id":"` + id + `","qty":` + qty + `}`},
			}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{
				Status: 201,
				Body:   &artifact.Body{Encoding: "utf8", Text: `{"order_id":"` + id + `"}`},
			}},
		}
	}
	a := flowFingerprint(makeFlow("abc", "1"))
	b := flowFingerprint(makeFlow("zzz", "42"))
	if a == "" || a != b {
		t.Fatalf("expected identical shape fingerprints, got %q vs %q", a, b)
	}
	c := flowFingerprint(makeFlow("abc", `"not-a-number"`))
	if c == a {
		t.Fatalf("expected different fingerprint for different shape")
	}
}

func TestFillSchemaSHAInline(t *testing.T) {
	store := NewTestCaseStore(t.TempDir())
	if _, err := store.StartCase("sha-case", nil, nil, ""); err != nil {
		t.Fatalf("start case: %v", err)
	}
	stored := store.AddSchema(artifact.SchemaRef{
		ID:     "dep-openapi",
		Role:   "fake",
		Type:   "openapi",
		Source: artifact.SchemaSource{Type: "inline", Content: "openapi: 3.0.0"},
	})
	if stored.Source.SHA256 == "" {
		t.Fatal("expected inline schema sha256 to be computed")
	}
}

func TestFinishCaseAttachesConsumedFields(t *testing.T) {
	store := NewTestCaseStore(t.TempDir())
	if _, err := store.StartCase("consumed-case", nil, nil, ""); err != nil {
		t.Fatalf("start case: %v", err)
	}
	exchanges := []mock.Exchange{{
		Matched:       true,
		MatchedStubID: "account-lookup",
		Request:       mock.RecordedRequest{Method: "GET", Path: "/accounts/1"},
		Response:      mock.RecordedResponse{Status: 200, Body: `{"id":"1","balance":10}`},
	}}
	art, _, err := store.FinishCase("pass", nil, exchanges, map[string][]string{
		"account-lookup": {"body.id", "body.balance"},
	})
	if err != nil {
		t.Fatalf("finish case: %v", err)
	}
	var found bool
	for _, flow := range art.Traffic {
		if flow.MatchedStubID == "account-lookup" {
			found = true
			if len(flow.ConsumedFields) != 2 {
				t.Fatalf("expected 2 consumed fields, got %v", flow.ConsumedFields)
			}
			if flow.Fingerprint == "" {
				t.Fatal("expected fingerprint on flow")
			}
		}
	}
	if !found {
		t.Fatal("expected flow for matched stub")
	}
}
