package artifact

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// Artifact is the capture model for one test attempt, persisted as bundle evidence.
type Artifact struct {
	CaptureIncomplete  bool              `json:"capture_incomplete,omitempty"`
	CaptureDiagnostics []string          `json:"capture_diagnostics,omitempty"`
	Version            int               `json:"version"`
	TestCase           TestCase          `json:"testcase"`
	Env                *Environment      `json:"environment,omitempty"`
	Schemas            []SchemaRef       `json:"schemas,omitempty"`
	Traffic            []Flow            `json:"traffic"`
	Assertions         []Assertion       `json:"assertions,omitempty"`
	SchemaValidation   *SchemaValidation `json:"schema_validation,omitempty"`
	// Redactions is legacy read metadata. New captures never populate it.
	Redactions []Redaction    `json:"redactions,omitempty"`
	Meta       map[string]any `json:"meta,omitempty"`
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
// TestIdentity is stable across executions; ID is local to the application.
// A nil AttemptOrdinal means the harness did not supply retry ordering.
type TestIdentity struct {
	ID             string `json:"id" yaml:"id"`
	Variant        string `json:"variant,omitempty" yaml:"variant,omitempty"`
	AttemptOrdinal *int   `json:"attempt_ordinal,omitempty" yaml:"attempt_ordinal,omitempty"`
}

func (identity *TestIdentity) Validate() error {
	if identity == nil {
		return nil
	}
	if strings.TrimSpace(identity.ID) == "" || len(identity.ID) > 256 || (identity.AttemptOrdinal != nil && *identity.AttemptOrdinal < 0) {
		return errors.New("test_identity requires an id up to 256 bytes and nonnegative attempt_ordinal")
	}
	return nil
}

type TestCase struct {
	Identity   *TestIdentity  `json:"identity,omitempty" yaml:"identity,omitempty"`
	TraceID    string         `json:"trace_id,omitempty" yaml:"trace_id,omitempty"`
	RootSpanID string         `json:"root_span_id,omitempty" yaml:"root_span_id,omitempty"`
	ID         string         `json:"id" yaml:"id"`
	Name       string         `json:"name" yaml:"name"`
	StartedAt  time.Time      `json:"started_at" yaml:"started_at"`
	FinishedAt time.Time      `json:"finished_at,omitempty" yaml:"finished_at,omitempty"`
	Status     string         `json:"status,omitempty" yaml:"status,omitempty"`
	Tags       []string       `json:"tags,omitempty" yaml:"tags,omitempty"`
	Meta       map[string]any `json:"meta,omitempty" yaml:"meta,omitempty"`
}

// Environment describes runtime addresses.
type Environment struct {
	SUTBaseURL  string `json:"sut_base_url,omitempty"`
	ProxyListen string `json:"proxy_listen,omitempty"`
	ProxyMode   string `json:"proxy_mode,omitempty"`
}

// SchemaRef identifies schemas attached to the test case.
type SchemaRef struct {
	ID             string       `json:"id"`
	Role           string       `json:"role"`
	Type           string       `json:"type"`
	Protocol       string       `json:"protocol,omitempty"`
	Source         SchemaSource `json:"source"`
	OpenAPIVersion string       `json:"openapi_version,omitempty"`
	AssetName      string       `json:"asset_name,omitempty"`
	AssetID        string       `json:"asset_id,omitempty"`
	AssetSHA       string       `json:"asset_sha,omitempty"`
}

// SchemaSource identifies where the schema came from.
type SchemaSource struct {
	Type    string `json:"type"`
	Path    string `json:"path,omitempty"`
	URL     string `json:"url,omitempty"`
	Content string `json:"content,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Format  string `json:"format,omitempty"`
}

// Flow captures one request/response exchange.
type Flow struct {
	ID         string    `json:"id"`
	Protocol   string    `json:"protocol"`
	Channel    string    `json:"channel"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Request    Message   `json:"request"`
	Response   Message   `json:"response"`
	// AssetID identifies the externally owned dependency receiving this flow.
	AssetID string `json:"asset_id,omitempty"`
	// TraceID correlates the captured exchange with application traces and logs.
	TraceID            string `json:"trace_id,omitempty"`
	RequestSpanID      string `json:"request_span_id,omitempty"`
	TraceContextSource string `json:"trace_context_source,omitempty"`
	TestCorrelation    string `json:"test_correlation,omitempty"`
	ParentCorrelation  string `json:"parent_correlation,omitempty"`
	// ParentFlowID identifies the inbound test request that caused a dependency call.
	ParentFlowID string `json:"parent_flow_id,omitempty"`
	// TransportError preserves an upstream connection or TLS failure alongside
	// the recorded 502 response.
	TransportError string                `json:"transport_error,omitempty"`
	Assumption     *DependencyAssumption `json:"assumption,omitempty"`
}

// DependencyAssumption qualifies an observed outbound exchange. Presence of a
// field in a mock response is not evidence that the consumer required it.
type DependencyAssumption struct {
	EvidenceBasis  string `json:"evidence_basis"`
	ConsumedFields string `json:"consumed_fields"`
	ProviderCheck  struct {
		Status string `json:"status"`
	} `json:"provider_check"`
}

// Message wraps HTTP message payloads.
type Message struct {
	HTTP *HttpMessage
}

// HttpMessage represents a request or response payload.
type HttpMessage struct {
	Method   string  `json:"method,omitempty"`
	URL      string  `json:"url,omitempty"`
	Status   int     `json:"status,omitempty"`
	Headers  Headers `json:"headers,omitempty"`
	Trailers Headers `json:"trailers,omitempty"`
	Body     *Body   `json:"body,omitempty"`
}

// Headers stores multi-valued headers.
type Headers map[string][]string

// Body stores raw and decoded payloads.
type Body struct {
	Encoding string `json:"encoding,omitempty"`
	Text     string `json:"text,omitempty"`
	Bytes    string `json:"bytes,omitempty"`
	// Omitted means no retained representation is available, independently of
	// whether capture was truncated. Redacted is legacy metadata identifying a
	// changed representation; new captures never set it.
	Omitted  bool `json:"omitted,omitempty"`
	Redacted bool `json:"redacted,omitempty"`
	// Truncated is true when only a bounded prefix was retained.
	Truncated bool `json:"truncated,omitempty"`
	// OriginalBytes is the complete observed size before capture truncation.
	OriginalBytes int64 `json:"original_bytes,omitempty"`
	// ContentEncoding records the original on-the-wire Content-Encoding (e.g.
	// "gzip") when the payload was transparently decompressed before storage.
	// Empty means the body was not compressed.
	ContentEncoding string       `json:"content_encoding,omitempty"`
	Decoded         *DecodedBody `json:"decoded,omitempty"`
}

// DecodedBody stores decoded payloads.
type DecodedBody struct {
	Encoding  string `json:"encoding,omitempty"`
	Text      string `json:"text,omitempty"`
	SchemaRef string `json:"schema_ref,omitempty"`
}

// Assertion captures a field-level assertion.
type Assertion struct {
	ID       string `json:"id"`
	Channel  string `json:"channel"`
	Phase    string `json:"phase"`
	FlowID   string `json:"flow_id,omitempty"`
	Field    string `json:"field,omitempty"`
	PathLang string `json:"path_lang,omitempty"`
	Operator string `json:"operator"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Result   string `json:"result"`
	Message  string `json:"message,omitempty"`
}

// SchemaValidation captures schema validation results.
type SchemaValidation struct {
	Unavailable []SchemaValidationError `json:"unavailable,omitempty"`
	Skipped     []string                `json:"skipped,omitempty"`
	Status      string                  `json:"status,omitempty"`
	Errors      []SchemaValidationError `json:"errors,omitempty"`
}

// SchemaValidationError describes a single schema validation failure.
type SchemaValidationError struct {
	FlowID   string `json:"flow_id,omitempty"`
	Channel  string `json:"channel,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Phase    string `json:"phase,omitempty"`
	SchemaID string `json:"schema_id,omitempty"`
	Message  string `json:"message,omitempty"`
}

// Redaction describes a rule recorded by older producers. It exists only to
// interpret legacy archives conservatively; runtime redaction is unsupported.
type Redaction struct {
	Field       string `json:"field"`
	Rule        string `json:"rule"`
	Replacement string `json:"replacement,omitempty"`
}
