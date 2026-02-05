package grpcmock

import "time"

// Stub defines a gRPC stubbed response.
type Stub struct {
	ID      string       `json:"id"`
	Path    string       `json:"path,omitempty"`
	Service string       `json:"service,omitempty"`
	Method  string       `json:"method,omitempty"`
	Match   Match        `json:"match,omitempty"`
	Reply   GrpcResponse `json:"response"`
}

// Match defines how incoming requests are matched.
type Match struct {
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBase64 string              `json:"body_base64,omitempty"`
}

// GrpcResponse defines the response for a stub.
type GrpcResponse struct {
	Status        int                 `json:"status,omitempty"`
	StatusMessage string              `json:"status_message,omitempty"`
	Headers       map[string][]string `json:"headers,omitempty"`
	Trailers      map[string][]string `json:"trailers,omitempty"`
	BodyBase64    string              `json:"body_base64,omitempty"`
}

// RecordedRequest stores captured gRPC request data.
type RecordedRequest struct {
	Path        string
	Service     string
	Method      string
	Authority   string
	ContentType string
	Headers     map[string][]string
	Body        []byte
}

// RecordedResponse stores captured gRPC response data.
type RecordedResponse struct {
	Status        int
	StatusMessage string
	ContentType   string
	Headers       map[string][]string
	Trailers      map[string][]string
	Body          []byte
}

// Exchange represents a request/response capture.
type Exchange struct {
	Request       RecordedRequest
	Response      RecordedResponse
	MatchedStubID string
	Matched       bool
	StartedAt     time.Time
	FinishedAt    time.Time
	Error         string
}
