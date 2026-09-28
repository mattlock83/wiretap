package app

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/capture"
	"github.com/phrond/evermock/internal/proxy"
)

func TestBodyFromHTTPDecompressesGzip(t *testing.T) {
	original := `{"result":"ok"}`
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(original))
	_ = gz.Close()
	body := bodyFromHTTP(artifact.Headers{"Content-Encoding": {"gzip"}}, buf.Bytes())
	if body == nil || body.Text != original || body.ContentEncoding != "gzip" {
		t.Fatalf("unexpected decoded body: %#v", body)
	}
}

func TestBodyFromHTTPCapsDecompressedPayload(t *testing.T) {
	original := strings.Repeat("expanded-", 100)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(original))
	_ = gz.Close()
	body := bodyFromCapturedHTTP(artifact.Headers{"Content-Encoding": {"gzip"}}, capture.FromBytes(buf.Bytes(), int64(len(buf.Bytes()))), 16)
	if body == nil || body.Text != original[:16] || !body.Truncated || body.OriginalBytes != int64(len(original)) {
		t.Fatalf("unexpected bounded body: %#v", body)
	}
}

func TestFlowFromProxyCarriesCaptureMetadataAndError(t *testing.T) {
	store := NewTestCaseStore(t.TempDir(), 4)
	_, _ = store.StartCase("bounded", nil, nil, "")
	flow := store.flowFromProxy(proxy.HTTPExchange{
		Method: http.MethodPost, URL: "http://example.test/items",
		RequestBody: []byte("requ"), RequestBodyBytes: 12, RequestBodyTruncated: true,
		Status: http.StatusBadGateway, ResponseBody: []byte("resp"), ResponseBodyBytes: 13, ResponseBodyTruncated: true,
		Error: "dial tcp: connection refused",
	})
	if flow.Request.HTTP == nil || !flow.Request.HTTP.Body.Truncated || flow.Request.HTTP.Body.OriginalBytes != 12 {
		t.Fatalf("request body = %#v", flow.Request.HTTP)
	}
	if flow.Response.HTTP == nil || !flow.Response.HTTP.Body.Truncated || flow.Response.HTTP.Body.OriginalBytes != 13 {
		t.Fatalf("response body = %#v", flow.Response.HTTP)
	}
	if flow.TransportError == "" {
		t.Fatal("expected transport error")
	}
}

func TestFinishCaseAttachesFingerprint(t *testing.T) {
	store := NewTestCaseStore(t.TempDir())
	_, _ = store.StartCase("fingerprint-case", nil, nil, "")
	store.RecordHTTP(proxy.HTTPExchange{Method: http.MethodGet, URL: "http://sut.test/hello", Status: http.StatusOK})
	art, _, err := store.FinishCase("pass", nil)
	if err != nil {
		t.Fatalf("finish case: %v", err)
	}
	if len(art.Traffic) != 1 || art.Traffic[0].Fingerprint == "" {
		t.Fatalf("unexpected traffic: %+v", art.Traffic)
	}
}

func TestFillSchemaSHAInline(t *testing.T) {
	store := NewTestCaseStore(t.TempDir())
	_, _ = store.StartCase("sha-case", nil, nil, "")
	stored := store.AddSchema(artifact.SchemaRef{ID: "sut-openapi", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: "openapi: 3.0.0"}})
	if stored.Source.SHA256 == "" {
		t.Fatal("expected inline schema sha256")
	}
}
