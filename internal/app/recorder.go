package app

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/capture"
	"github.com/mattlock83/wiretap/internal/proxy"
	"go.opentelemetry.io/otel/trace"
)

type TestCaseStore struct {
	mu           sync.Mutex
	sink         CaptureSink
	active       *testCaseState
	pending      *artifact.Artifact
	flowSeq      int
	schemaSeq    int
	maxBodyBytes int64
}

// CaptureSink stores registered attempts in a runtime-owned run.
type CaptureSink interface {
	Register(artifact.TestCase, []artifact.SchemaRef) ([]artifact.SchemaRef, error)
	Commit(*artifact.Artifact) (string, error)
}

func (s *TestCaseStore) RegisterActive() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return errors.New("no active test case")
	}
	tc := artifact.TestCase{Identity: s.active.identity, ID: s.active.id, Name: s.active.name, StartedAt: s.active.startedAt, Tags: s.active.tags, Meta: s.active.meta}
	frozen, err := s.sink.Register(tc, s.active.schemas)
	if err != nil {
		return err
	}
	s.active.schemas = frozen
	return nil
}

func (s *TestCaseStore) AbortCase() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = nil
}

type testCaseState struct {
	identity           *artifact.TestIdentity
	root               trace.SpanContext
	captureIncomplete  bool
	captureDiagnostics []string
	id                 string
	name               string
	startedAt          time.Time
	tags               []string
	meta               map[string]any
	sutBaseURL         string
	flows              []artifact.Flow
	schemas            []artifact.SchemaRef
}

func NewTestCaseStore(sink CaptureSink, maxBodyBytes int64) *TestCaseStore {
	return &TestCaseStore{sink: sink, maxBodyBytes: maxBodyBytes}
}

func (s *TestCaseStore) StartCase(name string, tags []string, meta map[string]any, sutBaseURL string) (*artifact.TestCase, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.active != nil {
		return nil, errors.New("test case already active")
	}
	if s.pending != nil {
		return nil, errors.New("previous test attempt has a pending persistence failure")
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
	s.pending = nil
	s.flowSeq = 0
	s.schemaSeq = 0

	return &artifact.TestCase{ID: id, Name: name, StartedAt: startedAt, Tags: tags, Meta: meta}, nil
}

func (s *TestCaseStore) HasPendingCommit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending != nil
}

// FinishCase finalizes the active test case and writes its captured artifact.
func (s *TestCaseStore) FinishCase(status string, env *artifact.Environment) (*artifact.Artifact, string, error) {
	s.mu.Lock()
	state := s.active
	if state == nil {
		if s.pending != nil {
			art := s.pending
			path, err := s.sink.Commit(art)
			if err == nil {
				s.pending = nil
			}
			s.mu.Unlock()
			return art, path, err
		}
		s.mu.Unlock()
		return nil, "", errors.New("no active test case")
	}
	s.active = nil
	s.mu.Unlock()

	finishedAt := time.Now().UTC()
	if state.captureIncomplete {
		status = "fail"
	}

	flows := append([]artifact.Flow{}, state.flows...)
	linkCausality(flows)
	for i := range flows {
		if flows[i].Channel == "sut_to_dependency" {
			flows[i].Assumption = &artifact.DependencyAssumption{EvidenceBasis: "observed_exchange", ConsumedFields: "unknown"}
			flows[i].Assumption.ProviderCheck.Status = "not_checked"
		}
	}

	if env == nil {
		env = &artifact.Environment{}
	}
	if state.sutBaseURL != "" {
		env.SUTBaseURL = state.sutBaseURL
	}

	art := &artifact.Artifact{
		Version:           2,
		CaptureIncomplete: state.captureIncomplete, CaptureDiagnostics: state.captureDiagnostics,
		TestCase: artifact.TestCase{Identity: state.identity, ID: state.id, Name: state.name, StartedAt: state.startedAt, FinishedAt: finishedAt, Status: status, Tags: state.tags, Meta: state.meta},
		Env:      env,
		Schemas:  append([]artifact.SchemaRef{}, state.schemas...),
		Traffic:  flows,
		Meta:     map[string]any{},
	}
	if state.root.IsValid() {
		art.TestCase.TraceID = state.root.TraceID().String()
		art.TestCase.RootSpanID = state.root.SpanID().String()
	}

	applySchemaValidation(art)

	path, err := s.sink.Commit(art)
	if err != nil {
		s.mu.Lock()
		s.pending = art
		s.mu.Unlock()
		return art, "", err
	}

	return art, path, nil
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

func (s *TestCaseStore) RecordHTTP(exchange proxy.HTTPExchange) {
	flow := s.flowFromProxy(exchange)
	if flow.ID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || (exchange.CaseID != "" && exchange.CaseID != s.active.id) {
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
	if s.active == nil || (exchange.CaseID != "" && exchange.CaseID != s.active.id) {
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
		ID:            id,
		Protocol:      "http",
		Channel:       channel,
		AssetID:       exchange.AssetID,
		TraceID:       exchange.TraceID,
		RequestSpanID: exchange.RequestSpanID, TraceContextSource: exchange.TraceContextSource, TestCorrelation: exchange.TestCorrelation,
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

// linkCausality reports inference strength; a shared trace is not an exact parent span.
func linkCausality(flows []artifact.Flow) {
	for i := range flows {
		dep := &flows[i]
		if dep.Channel != "sut_to_dependency" {
			continue
		}
		dep.ParentFlowID, dep.ParentCorrelation = "", "unmatched"
		var candidates, overlapping []string
		for _, parent := range flows {
			if parent.Channel != "test_to_sut" || parent.StartedAt.After(dep.StartedAt) {
				continue
			}
			if dep.TraceID != "" && (parent.TraceID == "" || parent.TraceID != dep.TraceID) {
				continue
			}
			candidates = append(candidates, parent.ID)
			if !dep.StartedAt.After(parent.StartedAt.Add(time.Duration(parent.DurationMS+1) * time.Millisecond)) {
				overlapping = append(overlapping, parent.ID)
			}
		}
		if dep.TestCorrelation == "trace" && dep.TraceContextSource == "received" && dep.TraceID != "" && len(candidates) == 1 {
			dep.ParentFlowID, dep.ParentCorrelation = candidates[0], "trace_unique"
		} else if len(overlapping) == 1 {
			dep.ParentFlowID, dep.ParentCorrelation = overlapping[0], "timing"
		} else if len(overlapping) > 1 || (dep.TraceID != "" && len(candidates) > 1) {
			dep.ParentCorrelation = "ambiguous"
		}
	}
}

func (s *TestCaseStore) SetTraceContext(root trace.SpanContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		s.active.root = root
	}
}
func (s *TestCaseStore) AddCaptureDiagnostic(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return
	}
	for _, existing := range s.active.captureDiagnostics {
		if existing == message {
			return
		}
	}
	s.active.captureDiagnostics = append(s.active.captureDiagnostics, message)
}
func (s *TestCaseStore) MarkCaptureIncomplete(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		s.active.captureIncomplete = true
		s.active.captureDiagnostics = append(s.active.captureDiagnostics, message)
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
	contentDecoded := false
	if !raw.Truncated && encoding != "" && encoding != "identity" {
		if out, ok := decompressBody(encoding, raw.Bytes, maxBodyBytes); ok {
			decoded = out
			contentDecoded = true
		}
	}
	body := bodyFromBytes(decoded.Bytes)
	if body == nil && contentDecoded {
		body = &artifact.Body{Encoding: "utf8"}
	}
	if body != nil && contentDecoded {
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

// SetTestIdentity binds a harness-owned logical identity before registration.
func (s *TestCaseStore) SetTestIdentity(identity *artifact.TestIdentity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || identity == nil {
		return
	}
	copy := *identity
	if identity.AttemptOrdinal != nil {
		ordinal := *identity.AttemptOrdinal
		copy.AttemptOrdinal = &ordinal
	}
	s.active.identity = &copy
}
