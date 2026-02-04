package mock

import "time"

// Stub defines a match rule and the response to return.
type Stub struct {
	ID       string
	Match    StubMatch
	Response StubResponse
}

// StubMatch defines exact-match criteria. Empty values are ignored.
type StubMatch struct {
	Method  string
	Path    string
	Headers map[string]string
	Query   map[string]string
	Body    string
}

// StubResponse defines the HTTP response returned for a matched stub.
type StubResponse struct {
	Status  int
	Headers map[string]string
	Body    string
}

// RecordedRequest captures the inbound request to the mock server.
type RecordedRequest struct {
	Method  string
	Path    string
	URL     string
	Headers map[string][]string
	Query   map[string][]string
	Body    string
}

// RecordedResponse captures the response written by the mock server.
type RecordedResponse struct {
	Status  int
	Headers map[string][]string
	Body    string
}

// Exchange records the request/response pair and match metadata.
type Exchange struct {
	Request       RecordedRequest
	Response      RecordedResponse
	MatchedStubID string
	Matched       bool
	Error         string
	StartedAt     time.Time
	FinishedAt    time.Time
}
