package mock

import "time"

// Stub defines a match rule and the response to return.
type Stub struct {
	ID        string
	SessionID string
	// AssetID scopes the stub to one declared dependency. A stub with an
	// AssetID only matches requests routed to that dependency; a stub without
	// one only matches requests that were not routed to any dependency.
	AssetID string
	Default bool
	// Source records how the stub came to exist: "queued" (registered by a
	// test) or "recorded" (captured from a fallback upstream).
	Source   string
	Match    StubMatch
	Response StubResponse
}

// StubMatch defines exact-match criteria. Empty values are ignored.
type StubMatch struct {
	Method       string
	Path         string
	Headers      map[string]string
	Query        map[string]string
	Body         string
	BodyContains string
}

// StubResponse defines the HTTP response returned for a matched stub.
type StubResponse struct {
	Status  int
	DelayMS int
	Headers map[string]string
	Body    string
	Fault   string
}

// RecordedRequest captures the inbound request to the mock server.
type RecordedRequest struct {
	Method   string
	Path     string
	URL      string
	Headers  map[string][]string
	Trailers map[string][]string
	Query    map[string][]string
	Body     string
}

// RecordedResponse captures the response written by the mock server.
type RecordedResponse struct {
	Status   int
	DelayMS  int
	Headers  map[string][]string
	Trailers map[string][]string
	Body     string
	Fault    string
}

// Exchange records the request/response pair and match metadata.
type Exchange struct {
	ID        string
	SessionID string
	// AssetID identifies which declared dependency this request was routed to
	// (empty when the request hit the mock without dependency routing).
	AssetID string
	// TraceID is the phrond trace id propagated by the SUT (when it forwards
	// the X-Phrond-Trace-Id header injected by the capture proxy). Links this
	// downstream call to the test_to_sut request that caused it.
	TraceID       string
	Request       RecordedRequest
	Response      RecordedResponse
	MatchedStubID string
	Matched       bool
	// MatchedStub is a snapshot of the stub that served this exchange,
	// including its unrendered response template. It is captured at match
	// time because matched stubs are consumed (removed) from the registry.
	MatchedStub *Stub
	// MockMode identifies whether Phrond served a built-in stub or observed a
	// response from a configured external mock gateway.
	MockMode   string
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}
