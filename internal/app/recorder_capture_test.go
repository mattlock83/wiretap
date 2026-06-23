package app

import (
	"bytes"
	"compress/gzip"
	"testing"
	"time"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/mock"
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

func TestMockStubFromExchangeCapturesAssumption(t *testing.T) {
	exchange := mock.Exchange{
		Matched: true,
		MatchedStub: &mock.Stub{
			ID: "alpha",
			Match: mock.StubMatch{
				Method: "POST",
				Path:   "/__phrond/session/session-1/alpha",
				Body:   `{"id":"123"}`,
			},
			Response: mock.StubResponse{
				Status: 200,
				Body:   `{"served_at":"{{cel "now"}}"}`,
			},
		},
	}

	stub := mockStubFromExchange(exchange)
	if stub == nil {
		t.Fatal("expected mock stub, got nil")
	}
	if stub.Match.Path != "/alpha" {
		t.Fatalf("expected session-stripped path /alpha, got %q", stub.Match.Path)
	}
	if stub.ResponseTemplate.Body != `{"served_at":"{{cel "now"}}"}` {
		t.Fatalf("expected unrendered template, got %q", stub.ResponseTemplate.Body)
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
	art, _, err := store.FinishCase("pass", nil, nil)
	if err != nil {
		t.Fatalf("finish case: %v", err)
	}
	if art.Producer == nil {
		t.Fatal("expected producer on artifact")
	}
	if art.Producer.Repo != "acme/widgets" || art.Producer.CommitSHA != "abc123" {
		t.Fatalf("unexpected producer: %+v", art.Producer)
	}
}
