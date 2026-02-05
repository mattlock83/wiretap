package artifact

import (
	"sort"
	"time"
)

// Artifact is the top-level test artifact written per test case.
type Artifact struct {
	Version    int            `yaml:"version" json:"version"`
	TestCase   TestCase       `yaml:"testcase" json:"testcase"`
	Env        *Environment   `yaml:"environment,omitempty" json:"environment,omitempty"`
	Schemas    []SchemaRef    `yaml:"schemas,omitempty" json:"schemas,omitempty"`
	Traffic    []Flow         `yaml:"traffic" json:"traffic"`
	Assertions []Assertion    `yaml:"assertions,omitempty" json:"assertions,omitempty"`
	Redactions []Redaction    `yaml:"redactions,omitempty" json:"redactions,omitempty"`
	Meta       map[string]any `yaml:"meta,omitempty" json:"meta,omitempty"`
}

// Normalize ensures deterministic ordering for slices.
func (a *Artifact) Normalize() {
	if a == nil {
		return
	}
	if len(a.Schemas) > 1 {
		sort.SliceStable(a.Schemas, func(i, j int) bool {
			return a.Schemas[i].ID < a.Schemas[j].ID
		})
	}
	if len(a.Traffic) > 1 {
		sort.SliceStable(a.Traffic, func(i, j int) bool {
			return a.Traffic[i].ID < a.Traffic[j].ID
		})
	}
	if len(a.Assertions) > 1 {
		sort.SliceStable(a.Assertions, func(i, j int) bool {
			return a.Assertions[i].ID < a.Assertions[j].ID
		})
	}
	if len(a.Redactions) > 1 {
		sort.SliceStable(a.Redactions, func(i, j int) bool {
			return a.Redactions[i].Field < a.Redactions[j].Field
		})
	}
}

// TestCase metadata.
type TestCase struct {
	ID         string         `yaml:"id" json:"id"`
	Name       string         `yaml:"name" json:"name"`
	StartedAt  time.Time      `yaml:"started_at" json:"started_at"`
	FinishedAt time.Time      `yaml:"finished_at,omitempty" json:"finished_at,omitempty"`
	Status     string         `yaml:"status,omitempty" json:"status,omitempty"`
	Tags       []string       `yaml:"tags,omitempty" json:"tags,omitempty"`
	Meta       map[string]any `yaml:"meta,omitempty" json:"meta,omitempty"`
}

// Environment describes runtime addresses.
type Environment struct {
	SUTBaseURL      string `yaml:"sut_base_url,omitempty" json:"sut_base_url,omitempty"`
	ProxyListen     string `yaml:"proxy_listen,omitempty" json:"proxy_listen,omitempty"`
	MockBaseURL     string `yaml:"mock_base_url,omitempty" json:"mock_base_url,omitempty"`
	ProxyMode       string `yaml:"proxy_mode,omitempty" json:"proxy_mode,omitempty"`
	GRPCProxyListen string `yaml:"grpc_proxy_listen,omitempty" json:"grpc_proxy_listen,omitempty"`
	GRPCMockBaseURL string `yaml:"grpc_mock_base_url,omitempty" json:"grpc_mock_base_url,omitempty"`
}

// SchemaRef identifies schemas attached to the test case.
type SchemaRef struct {
	ID              string       `yaml:"id" json:"id"`
	Role            string       `yaml:"role" json:"role"`
	Type            string       `yaml:"type" json:"type"`
	Source          SchemaSource `yaml:"source" json:"source"`
	OpenAPIVersion  string       `yaml:"openapi_version,omitempty" json:"openapi_version,omitempty"`
	ProtobufPackage string       `yaml:"protobuf_package,omitempty" json:"protobuf_package,omitempty"`
	ProtobufService string       `yaml:"protobuf_service,omitempty" json:"protobuf_service,omitempty"`
}

// SchemaSource identifies where the schema came from.
type SchemaSource struct {
	Type    string `yaml:"type" json:"type"`
	Path    string `yaml:"path,omitempty" json:"path,omitempty"`
	URL     string `yaml:"url,omitempty" json:"url,omitempty"`
	Content string `yaml:"content,omitempty" json:"content,omitempty"`
	SHA256  string `yaml:"sha256,omitempty" json:"sha256,omitempty"`
}

// Flow captures one request/response exchange.
type Flow struct {
	ID            string    `yaml:"id" json:"id"`
	Protocol      string    `yaml:"protocol" json:"protocol"`
	Channel       string    `yaml:"channel" json:"channel"`
	StartedAt     time.Time `yaml:"started_at,omitempty" json:"started_at,omitempty"`
	DurationMS    int64     `yaml:"duration_ms,omitempty" json:"duration_ms,omitempty"`
	Request       Message   `yaml:"request" json:"request"`
	Response      Message   `yaml:"response" json:"response"`
	GRPC          *GrpcMeta `yaml:"grpc,omitempty" json:"grpc,omitempty"`
	AssertionIDs  []string  `yaml:"assertion_ids,omitempty" json:"assertion_ids,omitempty"`
	MatchedStubID string    `yaml:"matched_stub_id,omitempty" json:"matched_stub_id,omitempty"`
}

// Message wraps HTTP or gRPC message payloads.
type Message struct {
	HTTP *HttpMessage
	GRPC *GrpcMessage
}

// HttpMessage represents a request or response payload.
type HttpMessage struct {
	Method  string  `yaml:"method,omitempty" json:"method,omitempty"`
	URL     string  `yaml:"url,omitempty" json:"url,omitempty"`
	Status  int     `yaml:"status,omitempty" json:"status,omitempty"`
	Headers Headers `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body    *Body   `yaml:"body,omitempty" json:"body,omitempty"`
}

// GrpcMeta holds gRPC metadata about the call.
type GrpcMeta struct {
	Service       string `yaml:"service,omitempty" json:"service,omitempty"`
	Method        string `yaml:"method,omitempty" json:"method,omitempty"`
	Authority     string `yaml:"authority,omitempty" json:"authority,omitempty"`
	Status        int    `yaml:"status,omitempty" json:"status,omitempty"`
	StatusMessage string `yaml:"status_message,omitempty" json:"status_message,omitempty"`
}

// GrpcMessage stores gRPC request or response body and metadata.
type GrpcMessage struct {
	ContentType string  `yaml:"content_type,omitempty" json:"content_type,omitempty"`
	Headers     Headers `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body        *Body   `yaml:"body,omitempty" json:"body,omitempty"`
	Trailers    Headers `yaml:"trailers,omitempty" json:"trailers,omitempty"`
}

// Headers stores multi-valued headers.
type Headers map[string][]string

// Body stores raw and decoded payloads.
type Body struct {
	Encoding string       `yaml:"encoding,omitempty" json:"encoding,omitempty"`
	Text     string       `yaml:"text,omitempty" json:"text,omitempty"`
	Bytes    string       `yaml:"bytes,omitempty" json:"bytes,omitempty"`
	Decoded  *DecodedBody `yaml:"decoded,omitempty" json:"decoded,omitempty"`
}

// DecodedBody stores decoded payloads.
type DecodedBody struct {
	Encoding  string `yaml:"encoding,omitempty" json:"encoding,omitempty"`
	Text      string `yaml:"text,omitempty" json:"text,omitempty"`
	SchemaRef string `yaml:"schema_ref,omitempty" json:"schema_ref,omitempty"`
}

// Assertion captures a field-level assertion.
type Assertion struct {
	ID       string `yaml:"id" json:"id"`
	Channel  string `yaml:"channel" json:"channel"`
	Phase    string `yaml:"phase" json:"phase"`
	FlowID   string `yaml:"flow_id,omitempty" json:"flow_id,omitempty"`
	Field    string `yaml:"field,omitempty" json:"field,omitempty"`
	PathLang string `yaml:"path_lang,omitempty" json:"path_lang,omitempty"`
	Operator string `yaml:"operator" json:"operator"`
	Expected any    `yaml:"expected,omitempty" json:"expected,omitempty"`
	Actual   any    `yaml:"actual,omitempty" json:"actual,omitempty"`
	Result   string `yaml:"result" json:"result"`
	Message  string `yaml:"message,omitempty" json:"message,omitempty"`
}

// Redaction defines a redaction rule applied before export.
type Redaction struct {
	Field       string `yaml:"field" json:"field"`
	Rule        string `yaml:"rule" json:"rule"`
	Replacement string `yaml:"replacement,omitempty" json:"replacement,omitempty"`
}
