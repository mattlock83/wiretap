package app

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/mock"
	"github.com/phrond/evermock/internal/proxy"
	"github.com/phrond/evermock/internal/redact"
)

type TestCaseStore struct {
	mu        sync.Mutex
	outputDir string
	producer  artifact.Producer
	active    *testCaseState
	last      *artifact.Artifact
	flowSeq   int
	schemaSeq int
	expectSeq int
}

// SetProducer attaches auto-detected producer identity to artifacts written by
// this store.
func (s *TestCaseStore) SetProducer(producer artifact.Producer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.producer = producer
}

type testCaseState struct {
	id          string
	name        string
	startedAt   time.Time
	finishedAt  time.Time
	status      string
	tags        []string
	meta        map[string]any
	sutBaseURL  string
	flows       []artifact.Flow
	schemas     []artifact.SchemaRef
	expectation []Expectation
	redactions  []artifact.Redaction
}

type Expectation struct {
	Channel string         `json:"channel"`
	Match   map[string]any `json:"match"`
	Times   int            `json:"times"`
}

func NewTestCaseStore(outputDir string) *TestCaseStore {
	return &TestCaseStore{outputDir: outputDir}
}

func (s *TestCaseStore) StartCase(name string, tags []string, meta map[string]any, sutBaseURL string) (*artifact.TestCase, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.active != nil {
		return nil, errors.New("test case already active")
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("test case name is required")
	}
	id := fmt.Sprintf("tc-%d", time.Now().UTC().UnixNano())
	startedAt := time.Now().UTC()

	s.active = &testCaseState{
		id:         id,
		name:       name,
		startedAt:  startedAt,
		tags:       tags,
		meta:       meta,
		sutBaseURL: sutBaseURL,
	}
	s.last = nil
	s.flowSeq = 0
	s.schemaSeq = 0
	s.expectSeq = 0

	return &artifact.TestCase{ID: id, Name: name, StartedAt: startedAt, Tags: tags, Meta: meta}, nil
}

type TestCaseStatus struct {
	Active      bool       `json:"active"`
	CurrentID   string     `json:"current_id,omitempty"`
	CurrentName string     `json:"current_name,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	LastID      string     `json:"last_id,omitempty"`
	LastStatus  string     `json:"last_status,omitempty"`
	LastPath    string     `json:"last_path,omitempty"`
}

func (s *TestCaseStore) FinishCase(status string, env *artifact.Environment, mockExchanges []mock.Exchange) (*artifact.Artifact, string, error) {
	s.mu.Lock()
	state := s.active
	if state == nil {
		s.mu.Unlock()
		return nil, "", errors.New("no active test case")
	}
	s.active = nil
	producer := s.producer
	s.mu.Unlock()

	finishedAt := time.Now().UTC()
	state.finishedAt = finishedAt
	state.status = status

	flows := append([]artifact.Flow{}, state.flows...)
	for _, exchange := range mockExchanges {
		flow := s.flowFromMock(exchange)
		flows = append(flows, flow)
	}

	if env == nil {
		env = &artifact.Environment{}
	}
	if state.sutBaseURL != "" {
		env.SUTBaseURL = state.sutBaseURL
	}

	art := &artifact.Artifact{
		Version:    1,
		TestCase:   artifact.TestCase{ID: state.id, Name: state.name, StartedAt: state.startedAt, FinishedAt: finishedAt, Status: status, Tags: state.tags, Meta: state.meta},
		Env:        env,
		Schemas:    append([]artifact.SchemaRef{}, state.schemas...),
		Traffic:    flows,
		Redactions: append([]artifact.Redaction{}, state.redactions...),
		Meta:       map[string]any{},
	}
	if producer != (artifact.Producer{}) {
		p := producer
		art.Producer = &p
	}

	if len(state.expectation) > 0 {
		art.Meta["expectations"] = state.expectation
	}

	applySchemaValidation(art)

	if len(art.Redactions) > 0 {
		if err := redact.Apply(art, art.Redactions); err != nil {
			return nil, "", err
		}
	}

	path, err := artifact.WriteFile(s.outputDir, art)
	if err != nil {
		return nil, "", err
	}

	s.mu.Lock()
	s.last = art
	s.mu.Unlock()

	return art, path, nil
}

func (s *TestCaseStore) Status() TestCaseStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := TestCaseStatus{}
	if s.active != nil {
		status.Active = true
		status.CurrentID = s.active.id
		status.CurrentName = s.active.name
		status.StartedAt = &s.active.startedAt
	}
	if s.last != nil {
		status.LastID = s.last.TestCase.ID
		status.LastStatus = s.last.TestCase.Status
	}
	return status
}

func (s *TestCaseStore) LastArtifact() *artifact.Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return nil
	}
	copy := *s.last
	return &copy
}

func (s *TestCaseStore) AddSchema(ref artifact.SchemaRef) artifact.SchemaRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return ref
	}
	if ref.ID == "" {
		s.schemaSeq++
		ref.ID = fmt.Sprintf("schema-%d", s.schemaSeq)
	}
	s.active.schemas = append(s.active.schemas, ref)
	return ref
}

func (s *TestCaseStore) AddExpectation(expectation Expectation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return
	}
	s.expectSeq++
	s.active.expectation = append(s.active.expectation, expectation)
}

func (s *TestCaseStore) AddRedaction(rule artifact.Redaction) artifact.Redaction {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return rule
	}
	s.active.redactions = append(s.active.redactions, rule)
	return rule
}

func (s *TestCaseStore) RecordHTTP(exchange proxy.HTTPExchange) {
	flow := s.flowFromProxy(exchange)
	if flow.ID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return
	}
	s.active.flows = append(s.active.flows, flow)
}

func (s *TestCaseStore) nextFlowID() string {
	s.flowSeq++
	return fmt.Sprintf("flow-%d", s.flowSeq)
}

func (s *TestCaseStore) flowFromProxy(exchange proxy.HTTPExchange) artifact.Flow {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return artifact.Flow{}
	}
	id := s.nextFlowID()
	reqHeaders := cloneHeaders(exchange.RequestHeaders)
	respHeaders := cloneHeaders(exchange.ResponseHeaders)
	return artifact.Flow{
		ID:         id,
		Protocol:   "http",
		Channel:    "test_to_sut",
		StartedAt:  exchange.StartedAt,
		DurationMS: exchange.Duration.Milliseconds(),
		Request: artifact.Message{HTTP: &artifact.HttpMessage{
			Method:  exchange.Method,
			URL:     exchange.URL,
			Headers: reqHeaders,
			Body:    bodyFromHTTP(reqHeaders, exchange.RequestBody),
		}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{
			Status:  exchange.Status,
			Headers: respHeaders,
			Body:    bodyFromHTTP(respHeaders, exchange.ResponseBody),
		}},
	}
}

func (s *TestCaseStore) flowFromMock(exchange mock.Exchange) artifact.Flow {
	s.mu.Lock()
	id := s.nextFlowID()
	s.mu.Unlock()

	reqHeaders := cloneHeaders(exchange.Request.Headers)
	respHeaders := cloneHeaders(exchange.Response.Headers)
	return artifact.Flow{
		ID:         id,
		Protocol:   "http",
		Channel:    "sut_to_mock",
		StartedAt:  exchange.StartedAt,
		DurationMS: exchange.FinishedAt.Sub(exchange.StartedAt).Milliseconds(),
		Request: artifact.Message{HTTP: &artifact.HttpMessage{
			Method:  exchange.Request.Method,
			URL:     exchange.Request.URL,
			Headers: reqHeaders,
			Body:    bodyFromHTTP(reqHeaders, []byte(exchange.Request.Body)),
		}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{
			Status:  exchange.Response.Status,
			Headers: respHeaders,
			Body:    bodyFromHTTP(respHeaders, []byte(exchange.Response.Body)),
		}},
		MatchedStubID: exchange.MatchedStubID,
		MockStub:      mockStubFromExchange(exchange),
		Unmatched:     !exchange.Matched,
	}
}

// mockStubFromExchange captures the consumer's assumption (match rule + the
// unrendered response template) from the stub that served the exchange.
func mockStubFromExchange(exchange mock.Exchange) *artifact.MockStub {
	stub := exchange.MatchedStub
	if stub == nil {
		return nil
	}
	_, path := stripSessionPath(stub.Match.Path)
	return &artifact.MockStub{
		ID:        stub.ID,
		Default:   stub.Default,
		MatchedOn: matchedOn(stub.Match),
		Match: artifact.StubMatch{
			Method:       stub.Match.Method,
			Path:         path,
			Headers:      stub.Match.Headers,
			Query:        stub.Match.Query,
			Body:         stub.Match.Body,
			BodyContains: stub.Match.BodyContains,
		},
		ResponseTemplate: artifact.StubResponseTmpl{
			Status:  stub.Response.Status,
			Headers: stub.Response.Headers,
			Body:    stub.Response.Body,
			DelayMS: stub.Response.DelayMS,
			Fault:   stub.Response.Fault,
		},
	}
}

// matchedOn lists which match criteria the stub constrained, so a broker knows
// how loose or strict the recorded assumption was.
func matchedOn(match mock.StubMatch) []string {
	var on []string
	if strings.TrimSpace(match.Method) != "" {
		on = append(on, "method")
	}
	if strings.TrimSpace(match.Path) != "" {
		on = append(on, "path")
	}
	if len(match.Headers) > 0 {
		on = append(on, "headers")
	}
	if len(match.Query) > 0 {
		on = append(on, "query")
	}
	if match.Body != "" {
		on = append(on, "body")
	}
	if match.BodyContains != "" {
		on = append(on, "body_contains")
	}
	return on
}

func cloneHeaders(in map[string][]string) artifact.Headers {
	if in == nil {
		return nil
	}
	out := make(artifact.Headers, len(in))
	for key, values := range in {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	return out
}

func bodyFromBytes(raw []byte) *artifact.Body {
	if len(raw) == 0 {
		return nil
	}
	if utf8.Valid(raw) {
		return &artifact.Body{Encoding: "utf8", Text: string(raw)}
	}
	return &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(raw)}
}

// bodyFromHTTP captures a payload, transparently decompressing it when the
// headers declare a Content-Encoding so the stored body is the real content
// (and is diffable). The original wire encoding is preserved on the Body.
func bodyFromHTTP(headers artifact.Headers, raw []byte) *artifact.Body {
	if len(raw) == 0 {
		return nil
	}
	encoding := strings.ToLower(strings.TrimSpace(firstHeaderValue(headers, "Content-Encoding")))
	decoded := raw
	if encoding != "" && encoding != "identity" {
		if out, ok := decompressBody(encoding, raw); ok {
			decoded = out
		}
	}
	body := bodyFromBytes(decoded)
	if body != nil && encoding != "" && encoding != "identity" {
		body.ContentEncoding = encoding
	}
	return body
}

func firstHeaderValue(headers artifact.Headers, key string) string {
	for name, values := range headers {
		if strings.EqualFold(name, key) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// decompressBody decodes gzip/deflate payloads. Other encodings (e.g. br) are
// left untouched and reported as not decoded.
func decompressBody(encoding string, raw []byte) ([]byte, bool) {
	switch encoding {
	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, false
		}
		defer reader.Close()
		return readAllLimited(reader)
	case "deflate":
		// HTTP "deflate" is zlib-wrapped; fall back to raw DEFLATE streams.
		if reader, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			defer reader.Close()
			return readAllLimited(reader)
		}
		reader := flate.NewReader(bytes.NewReader(raw))
		defer reader.Close()
		return readAllLimited(reader)
	default:
		return nil, false
	}
}

func readAllLimited(r io.Reader) ([]byte, bool) {
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, false
	}
	return out, true
}
