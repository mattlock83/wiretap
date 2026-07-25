package app

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
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

// FinishCase finalizes the active test case. consumedFields maps a response id
// (matched stub id) to the fields the test declared it depends on.
func (s *TestCaseStore) FinishCase(status string, env *artifact.Environment, mockExchanges []mock.Exchange, consumedFields map[string][]string) (*artifact.Artifact, string, error) {
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
	linkCausality(flows)
	for i := range flows {
		flows[i].Fingerprint = flowFingerprint(flows[i])
		if len(consumedFields) > 0 && flows[i].MatchedStubID != "" {
			if fields, ok := consumedFields[flows[i].MatchedStubID]; ok {
				flows[i].ConsumedFields = append([]string{}, fields...)
			}
		}
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
	fillSchemaSHA(&ref)
	s.active.schemas = append(s.active.schemas, ref)
	return ref
}

// fillSchemaSHA computes the content hash for file/inline schemas when the
// caller did not provide one, so asset_sha (the asset version key) is always
// populated for locally available specs.
func fillSchemaSHA(ref *artifact.SchemaRef) {
	if ref == nil || strings.TrimSpace(ref.Source.SHA256) != "" {
		return
	}
	switch strings.ToLower(strings.TrimSpace(ref.Source.Type)) {
	case "inline":
		if content := ref.Source.Content; content != "" {
			sum := sha256.Sum256([]byte(content))
			ref.Source.SHA256 = hex.EncodeToString(sum[:])
		}
	case "file":
		path := strings.TrimSpace(ref.Source.Path)
		if path == "" {
			return
		}
		data, err := os.ReadFile(resolvePath(path))
		if err != nil {
			return
		}
		sum := sha256.Sum256(data)
		ref.Source.SHA256 = hex.EncodeToString(sum[:])
	}
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
		TraceID:    exchange.TraceID,
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
		AssetID:    exchange.AssetID,
		TraceID:    exchange.TraceID,
		StartedAt:  exchange.StartedAt,
		DurationMS: exchange.FinishedAt.Sub(exchange.StartedAt).Milliseconds(),
		Request: artifact.Message{HTTP: &artifact.HttpMessage{
			Method:   exchange.Request.Method,
			URL:      exchange.Request.URL,
			Headers:  reqHeaders,
			Trailers: cloneHeaders(exchange.Request.Trailers),
			Body:     bodyFromHTTP(reqHeaders, []byte(exchange.Request.Body)),
		}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{
			Status:   exchange.Response.Status,
			Headers:  respHeaders,
			Trailers: cloneHeaders(exchange.Response.Trailers),
			Body:     bodyFromHTTP(respHeaders, []byte(exchange.Response.Body)),
		}},
		MatchedStubID:  exchange.MatchedStubID,
		MockStub:       mockStubFromExchange(exchange),
		MockMode:       exchange.MockMode,
		TransportError: exchange.Error,
		Unmatched:      exchange.MockMode != "external" && !exchange.Matched,
	}
}

// mockStubFromExchange captures the consumer's assumption (match rule + the
// response template) from the stub that served the exchange.
func mockStubFromExchange(exchange mock.Exchange) *artifact.MockStub {
	stub := exchange.MatchedStub
	if stub == nil {
		return nil
	}
	return &artifact.MockStub{
		ID:        stub.ID,
		Default:   stub.Default,
		Source:    stub.Source,
		MatchedOn: matchedOn(stub.Match),
		Match: artifact.StubMatch{
			Method:       stub.Match.Method,
			Path:         stub.Match.Path,
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

// linkCausality resolves which test_to_sut flow caused each sut_to_mock flow.
// Trace ids (propagated by the SUT) are authoritative; time-window nesting is
// the best-effort fallback.
func linkCausality(flows []artifact.Flow) {
	type parent struct {
		id    string
		start time.Time
		end   time.Time
	}
	byTrace := map[string]string{}
	var parents []parent
	for _, flow := range flows {
		if flow.Channel != "test_to_sut" {
			continue
		}
		if flow.TraceID != "" {
			byTrace[flow.TraceID] = flow.ID
		}
		// DurationMS is floor-rounded; extend the window by one millisecond so
		// downstream calls issued at the tail of the request still nest.
		end := flow.StartedAt.Add(time.Duration(flow.DurationMS+1) * time.Millisecond)
		parents = append(parents, parent{id: flow.ID, start: flow.StartedAt, end: end})
	}
	for i := range flows {
		if flows[i].Channel != "sut_to_mock" {
			continue
		}
		if flows[i].TraceID != "" {
			if parentID, ok := byTrace[flows[i].TraceID]; ok {
				flows[i].ParentFlowID = parentID
				continue
			}
		}
		// Fallback: latest-starting test_to_sut flow whose window contains
		// this call's start.
		best := ""
		var bestStart time.Time
		for _, candidate := range parents {
			if flows[i].StartedAt.Before(candidate.start) || flows[i].StartedAt.After(candidate.end) {
				continue
			}
			if best == "" || candidate.start.After(bestStart) {
				best = candidate.id
				bestStart = candidate.start
			}
		}
		flows[i].ParentFlowID = best
	}
}

// flowFingerprint hashes the interaction shape (not its values) so identical
// interactions across runs and repos dedupe even when test data varies.
func flowFingerprint(flow artifact.Flow) string {
	var reqMethod, reqPath, reqShape, respShape string
	status := 0
	if flow.Request.HTTP != nil {
		reqMethod = flow.Request.HTTP.Method
		if parsed, err := url.Parse(flow.Request.HTTP.URL); err == nil {
			reqPath = parsed.Path
		} else {
			reqPath = flow.Request.HTTP.URL
		}
		reqShape = bodyShape(flow.Request.HTTP.Body)
	}
	if flow.Response.HTTP != nil {
		status = flow.Response.HTTP.Status
		respShape = bodyShape(flow.Response.HTTP.Body)
	}
	canonical := strings.Join([]string{
		flow.Channel, reqMethod, reqPath, strconv.Itoa(status), reqShape, respShape,
	}, "|")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:16]
}

// bodyShape reduces a JSON body to its key/type structure; non-JSON bodies
// collapse to a coarse token.
func bodyShape(body *artifact.Body) string {
	if body == nil {
		return ""
	}
	if body.Encoding == "base64" {
		return "binary"
	}
	trimmed := strings.TrimSpace(body.Text)
	if trimmed == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "text"
	}
	return valueShape(payload)
}

func valueShape(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, key+":"+valueShape(typed[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		if len(typed) == 0 {
			return "[]"
		}
		return "[" + valueShape(typed[0]) + "]"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	default:
		return "unknown"
	}
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
