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
	"github.com/phrond/evermock/internal/capture"
	"github.com/phrond/evermock/internal/proxy"
	"github.com/phrond/evermock/internal/redact"
)

type TestCaseStore struct {
	mu           sync.Mutex
	outputDir    string
	producer     artifact.Producer
	active       *testCaseState
	last         *artifact.Artifact
	flowSeq      int
	schemaSeq    int
	expectSeq    int
	assertSeq    int
	maxBodyBytes int64
}

// SetProducer attaches auto-detected producer identity to artifacts written by
// this store.
func (s *TestCaseStore) SetProducer(producer artifact.Producer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.producer = producer
}

type testCaseState struct {
	id             string
	name           string
	startedAt      time.Time
	finishedAt     time.Time
	status         string
	tags           []string
	meta           map[string]any
	sutBaseURL     string
	flows          []artifact.Flow
	assertions     []artifact.Assertion
	schemas        []artifact.SchemaRef
	expectation    []Expectation
	redactions     []artifact.Redaction
	generateSchema bool
}

func (s *TestCaseStore) SetSchemaGeneration(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		s.active.generateSchema = enabled
	}
}

type Expectation struct {
	Channel string         `json:"channel"`
	Match   map[string]any `json:"match"`
	Times   int            `json:"times"`
}

func NewTestCaseStore(outputDir string, maxBodyBytes ...int64) *TestCaseStore {
	limit := capture.DefaultMaxBodyBytes
	if len(maxBodyBytes) > 0 {
		limit = maxBodyBytes[0]
	}
	return &TestCaseStore{outputDir: outputDir, maxBodyBytes: limit}
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
	s.assertSeq = 0

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

// FinishCase finalizes the active test case and writes its captured artifact.
func (s *TestCaseStore) FinishCase(status string, env *artifact.Environment) (*artifact.Artifact, string, error) {
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
	for _, assertion := range state.assertions {
		if assertion.Result == "fail" {
			status = "fail"
			break
		}
	}
	state.status = status

	flows := append([]artifact.Flow{}, state.flows...)
	linkCausality(flows)
	for i := range flows {
		flows[i].Fingerprint = flowFingerprint(flows[i])
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
		Assertions: append([]artifact.Assertion{}, state.assertions...),
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
	if state.generateSchema {
		art.Meta["schema_generation"] = true
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
	if ref.AssetName == "" {
		ref.AssetName = ref.ID
	}
	if ref.AssetSHA == "" {
		ref.AssetSHA = ref.Source.SHA256
	}
	s.active.schemas = append(s.active.schemas, ref)
	return ref
}

// fillSchemaSHA computes the content hash for file/inline schemas when the
// caller did not provide one, making attached schema versions identifiable.
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
	channel := exchange.Channel
	if channel == "" {
		channel = "test_to_sut"
	}
	reqHeaders := cloneHeaders(exchange.RequestHeaders)
	respHeaders := cloneHeaders(exchange.ResponseHeaders)
	return artifact.Flow{
		ID:             id,
		Protocol:       "http",
		Channel:        channel,
		AssetID:        exchange.AssetID,
		TraceID:        exchange.TraceID,
		StartedAt:      exchange.StartedAt,
		DurationMS:     exchange.Duration.Milliseconds(),
		TransportError: exchange.Error,
		Request: artifact.Message{HTTP: &artifact.HttpMessage{
			Method:   exchange.Method,
			URL:      exchange.URL,
			Headers:  reqHeaders,
			Trailers: cloneHeaders(exchange.RequestTrailers),
			Body: bodyFromCapturedHTTP(reqHeaders, normalizedCapturedBody(
				exchange.RequestBody,
				exchange.RequestBodyBytes,
				exchange.RequestBodyTruncated,
			), s.maxBodyBytes),
		}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{
			Status:   exchange.Status,
			Headers:  respHeaders,
			Trailers: cloneHeaders(exchange.ResponseTrailers),
			Body: bodyFromCapturedHTTP(respHeaders, normalizedCapturedBody(
				exchange.ResponseBody,
				exchange.ResponseBodyBytes,
				exchange.ResponseBodyTruncated,
			), s.maxBodyBytes),
		}},
	}
}

// linkCausality identifies which inbound request caused each observed
// dependency call. Propagated trace IDs are authoritative; request timing is
// a useful fallback for applications that do not propagate tracing headers.
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
		parents = append(parents, parent{
			id: flow.ID, start: flow.StartedAt,
			end: flow.StartedAt.Add(time.Duration(flow.DurationMS+1) * time.Millisecond),
		})
	}
	for i := range flows {
		if flows[i].Channel != "sut_to_dependency" {
			continue
		}
		if parentID := byTrace[flows[i].TraceID]; parentID != "" {
			flows[i].ParentFlowID = parentID
			continue
		}
		var bestStart time.Time
		for _, candidate := range parents {
			if flows[i].StartedAt.Before(candidate.start) || flows[i].StartedAt.After(candidate.end) {
				continue
			}
			if flows[i].ParentFlowID == "" || candidate.start.After(bestStart) {
				flows[i].ParentFlowID = candidate.id
				bestStart = candidate.start
			}
		}
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
	channel := flow.Channel
	if flow.AssetID != "" {
		channel += ":" + flow.AssetID
	}
	canonical := strings.Join([]string{
		channel, reqMethod, reqPath, strconv.Itoa(status), reqShape, respShape,
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
	if body.Truncated {
		return "truncated"
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
	return bodyFromCapturedHTTP(headers, capture.FromBytes(raw, capture.DefaultMaxBodyBytes), capture.DefaultMaxBodyBytes)
}

func normalizedCapturedBody(data []byte, originalBytes int64, truncated bool) capture.Body {
	if originalBytes == 0 && len(data) > 0 {
		originalBytes = int64(len(data))
	}
	if originalBytes < int64(len(data)) {
		originalBytes = int64(len(data))
	}
	return capture.Body{
		Bytes:         data,
		OriginalBytes: originalBytes,
		Truncated:     truncated || int64(len(data)) < originalBytes,
	}
}

func bodyFromCapturedHTTP(headers artifact.Headers, raw capture.Body, maxBodyBytes int64) *artifact.Body {
	if len(raw.Bytes) == 0 && raw.OriginalBytes == 0 {
		return nil
	}
	encoding := strings.ToLower(strings.TrimSpace(firstHeaderValue(headers, "Content-Encoding")))
	decoded := raw
	if !raw.Truncated && encoding != "" && encoding != "identity" {
		if out, ok := decompressBody(encoding, raw.Bytes, maxBodyBytes); ok {
			decoded = out
		}
	}
	body := bodyFromBytes(decoded.Bytes)
	if body != nil && encoding != "" && encoding != "identity" {
		body.ContentEncoding = encoding
	}
	if body != nil && decoded.Truncated {
		body.Truncated = true
		body.OriginalBytes = decoded.OriginalBytes
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
func decompressBody(encoding string, raw []byte, maxBodyBytes int64) (capture.Body, bool) {
	switch encoding {
	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return capture.Body{}, false
		}
		defer reader.Close()
		return readAllLimited(reader, maxBodyBytes)
	case "deflate":
		// HTTP "deflate" is zlib-wrapped; fall back to raw DEFLATE streams.
		if reader, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			defer reader.Close()
			return readAllLimited(reader, maxBodyBytes)
		}
		reader := flate.NewReader(bytes.NewReader(raw))
		defer reader.Close()
		return readAllLimited(reader, maxBodyBytes)
	default:
		return capture.Body{}, false
	}
}

func readAllLimited(r io.Reader, maxBodyBytes int64) (capture.Body, bool) {
	out, err := capture.ReadAll(r, maxBodyBytes)
	if err != nil {
		return capture.Body{}, false
	}
	return out, true
}
