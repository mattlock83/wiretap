package app

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/capture"
	"github.com/mattlock83/wiretap/internal/proxy"
)

func TestBodyFromHTTPDecompressesGzip(t *testing.T) {
	for _, original := range []string{`{"result":"ok"}`, ""} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write([]byte(original))
		_ = gz.Close()
		body := bodyFromHTTP(artifact.Headers{"Content-Encoding": {"gzip"}}, buf.Bytes())
		if body == nil || body.Text != original || body.ContentEncoding != "gzip" {
			t.Fatalf("unexpected decoded body for %q: %#v", original, body)
		}
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

func TestBodyFromHTTPOnlyMarksSuccessfullyDecodedContent(t *testing.T) {
	for _, tc := range []struct {
		name, encoding string
		truncated      bool
	}{
		{name: "invalid gzip", encoding: "gzip"},
		{name: "unsupported encoding", encoding: "br"},
		{name: "truncated wire body", encoding: "gzip", truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := capture.Body{Bytes: []byte("retained bytes"), OriginalBytes: 14, Truncated: tc.truncated}
			body := bodyFromCapturedHTTP(artifact.Headers{"Content-Encoding": {tc.encoding}}, raw, 1024)
			if body == nil || body.ContentEncoding != "" || body.Text != "retained bytes" {
				t.Fatalf("undecoded content was marked as normalized: %#v", body)
			}
		})
	}
}

func TestFlowFromProxyCarriesCaptureMetadataAndError(t *testing.T) {
	store := testRecorder(t, 4)
	_, _ = store.StartCase("bounded", nil, nil, "")
	flow := store.flowFromProxy(proxy.HTTPExchange{
		Method: http.MethodPost, URL: "http://example.test/items",
		RequestBody: []byte("requ"), RequestBodyBytes: 12, RequestBodyTruncated: true,
		Status: http.StatusBadGateway, ResponseBody: []byte("resp"), ResponseBodyBytes: 13, ResponseBodyTruncated: true,
		Error: "dial tcp: connection refused",
	})
	if flow.Request == nil || !flow.Request.Body.Truncated || flow.Request.Body.OriginalBytes != 12 {
		t.Fatalf("request body = %#v", flow.Request)
	}
	if flow.Response == nil || !flow.Response.Body.Truncated || flow.Response.Body.OriginalBytes != 13 {
		t.Fatalf("response body = %#v", flow.Response)
	}
	if flow.TransportError == "" {
		t.Fatal("expected transport error")
	}
}

func TestRegistrationAssignsFrozenSchemaIdentity(t *testing.T) {
	store := testRecorder(t, capture.DefaultMaxBodyBytes)
	_, _ = store.StartCase("sha-case", nil, nil, "")
	schema := *permissiveDependencySchema()
	schema.Role = "dependency"
	schema.AssetID = "inventory-api"
	store.AddSchema(schema)
	if err := store.RegisterActive(); err != nil {
		t.Fatal(err)
	}
	if len(store.active.schemas) != 1 || store.active.schemas[0].Source.SHA256 == "" || store.active.schemas[0].Source.SHA256 != store.active.schemas[0].AssetSHA {
		t.Fatal("registration did not assign the frozen contract hash")
	}
}

func testRecorder(t *testing.T, limit int64) *TestCaseStore {
	t.Helper()
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewTestCaseStore(writer, limit)
}
