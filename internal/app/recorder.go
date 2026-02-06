package app

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/grpcmock"
	"github.com/phrond/evermock/internal/grpcproxy"
	"github.com/phrond/evermock/internal/mock"
	"github.com/phrond/evermock/internal/proxy"
	"github.com/phrond/evermock/internal/redact"
)

type TestCaseStore struct {
	mu        sync.Mutex
	outputDir string
	active    *testCaseState
	last      *artifact.Artifact
	flowSeq   int
	assertSeq int
	schemaSeq int
	expectSeq int
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
	assertions  []artifact.Assertion
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
	s.assertSeq = 0
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

func (s *TestCaseStore) FinishCase(status string, env *artifact.Environment, mockExchanges []mock.Exchange, grpcExchanges []grpcmock.Exchange) (*artifact.Artifact, string, error) {
	s.mu.Lock()
	state := s.active
	if state == nil {
		s.mu.Unlock()
		return nil, "", errors.New("no active test case")
	}
	s.active = nil
	s.mu.Unlock()

	finishedAt := time.Now().UTC()
	state.finishedAt = finishedAt
	state.status = status

	flows := append([]artifact.Flow{}, state.flows...)
	for _, exchange := range mockExchanges {
		flow := s.flowFromMock(exchange)
		flows = append(flows, flow)
	}
	for _, exchange := range grpcExchanges {
		flow := s.flowFromGrpcMock(exchange)
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
		Assertions: append([]artifact.Assertion{}, state.assertions...),
		Redactions: append([]artifact.Redaction{}, state.redactions...),
		Meta:       map[string]any{},
	}

	if len(state.expectation) > 0 {
		art.Meta["expectations"] = state.expectation
	}

	evaluateAssertions(art)
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

func (s *TestCaseStore) AddAssertion(assertion artifact.Assertion) artifact.Assertion {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return assertion
	}
	if assertion.ID == "" {
		s.assertSeq++
		assertion.ID = fmt.Sprintf("assertion-%d", s.assertSeq)
	}
	if assertion.Result == "" {
		assertion.Result = "recorded"
	}
	s.active.assertions = append(s.active.assertions, assertion)
	return assertion
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

func (s *TestCaseStore) RecordGRPC(exchange grpcproxy.Exchange) {
	flow := s.flowFromGrpcProxy(exchange)
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
	return artifact.Flow{
		ID:         id,
		Protocol:   "http",
		Channel:    "test_to_sut",
		StartedAt:  exchange.StartedAt,
		DurationMS: exchange.Duration.Milliseconds(),
		Request: artifact.Message{HTTP: &artifact.HttpMessage{
			Method:  exchange.Method,
			URL:     exchange.URL,
			Headers: cloneHeaders(exchange.RequestHeaders),
			Body:    bodyFromBytes(exchange.RequestBody),
		}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{
			Status:  exchange.Status,
			Headers: cloneHeaders(exchange.ResponseHeaders),
			Body:    bodyFromBytes(exchange.ResponseBody),
		}},
	}
}

func (s *TestCaseStore) flowFromMock(exchange mock.Exchange) artifact.Flow {
	s.mu.Lock()
	id := s.nextFlowID()
	s.mu.Unlock()

	return artifact.Flow{
		ID:         id,
		Protocol:   "http",
		Channel:    "sut_to_mock",
		StartedAt:  exchange.StartedAt,
		DurationMS: exchange.FinishedAt.Sub(exchange.StartedAt).Milliseconds(),
		Request: artifact.Message{HTTP: &artifact.HttpMessage{
			Method:  exchange.Request.Method,
			URL:     exchange.Request.URL,
			Headers: cloneHeaders(exchange.Request.Headers),
			Body:    bodyFromString(exchange.Request.Body),
		}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{
			Status:  exchange.Response.Status,
			Headers: cloneHeaders(exchange.Response.Headers),
			Body:    bodyFromString(exchange.Response.Body),
		}},
		MatchedStubID: exchange.MatchedStubID,
	}
}

func (s *TestCaseStore) flowFromGrpcProxy(exchange grpcproxy.Exchange) artifact.Flow {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return artifact.Flow{}
	}
	id := s.nextFlowID()
	return artifact.Flow{
		ID:         id,
		Protocol:   "grpc",
		Channel:    "test_to_sut",
		StartedAt:  exchange.StartedAt,
		DurationMS: exchange.Duration.Milliseconds(),
		Request: artifact.Message{GRPC: &artifact.GrpcMessage{
			ContentType: exchange.RequestContentType,
			Headers:     cloneHeaders(exchange.RequestHeaders),
			Body:        bodyFromBytesBinary(exchange.RequestBody),
		}},
		Response: artifact.Message{GRPC: &artifact.GrpcMessage{
			ContentType: exchange.ResponseContentType,
			Headers:     cloneHeaders(exchange.ResponseHeaders),
			Body:        bodyFromBytesBinary(exchange.ResponseBody),
			Trailers:    cloneHeaders(exchange.ResponseTrailers),
		}},
		GRPC: &artifact.GrpcMeta{
			Service:       exchange.Service,
			Method:        exchange.Method,
			Authority:     exchange.Authority,
			Status:        exchange.Status,
			StatusMessage: exchange.StatusMessage,
		},
	}
}

func (s *TestCaseStore) flowFromGrpcMock(exchange grpcmock.Exchange) artifact.Flow {
	s.mu.Lock()
	id := s.nextFlowID()
	s.mu.Unlock()

	return artifact.Flow{
		ID:         id,
		Protocol:   "grpc",
		Channel:    "sut_to_mock",
		StartedAt:  exchange.StartedAt,
		DurationMS: exchange.FinishedAt.Sub(exchange.StartedAt).Milliseconds(),
		Request: artifact.Message{GRPC: &artifact.GrpcMessage{
			ContentType: exchange.Request.ContentType,
			Headers:     cloneHeaders(exchange.Request.Headers),
			Body:        bodyFromBytesBinary(exchange.Request.Body),
		}},
		Response: artifact.Message{GRPC: &artifact.GrpcMessage{
			ContentType: exchange.Response.ContentType,
			Headers:     cloneHeaders(exchange.Response.Headers),
			Body:        bodyFromBytesBinary(exchange.Response.Body),
			Trailers:    cloneHeaders(exchange.Response.Trailers),
		}},
		GRPC: &artifact.GrpcMeta{
			Service:       exchange.Request.Service,
			Method:        exchange.Request.Method,
			Authority:     exchange.Request.Authority,
			Status:        exchange.Response.Status,
			StatusMessage: exchange.Response.StatusMessage,
		},
		MatchedStubID: exchange.MatchedStubID,
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

func bodyFromBytesBinary(raw []byte) *artifact.Body {
	if len(raw) == 0 {
		return nil
	}
	return &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(raw)}
}

func bodyFromString(raw string) *artifact.Body {
	if raw == "" {
		return nil
	}
	return &artifact.Body{Encoding: "utf8", Text: raw}
}
