package artifact

import (
	"sort"
	"time"
)

// Artifact is the top-level test artifact written per test case.
type Artifact struct {
	Version          int               `yaml:"version" json:"version"`
	Producer         *Producer         `yaml:"producer,omitempty" json:"producer,omitempty"`
	TestCase         TestCase          `yaml:"testcase" json:"testcase"`
	Env              *Environment      `yaml:"environment,omitempty" json:"environment,omitempty"`
	Schemas          []SchemaRef       `yaml:"schemas,omitempty" json:"schemas,omitempty"`
	Traffic          []Flow            `yaml:"traffic" json:"traffic"`
	Assertions       []Assertion       `yaml:"assertions,omitempty" json:"assertions,omitempty"`
	SchemaValidation *SchemaValidation `yaml:"schema_validation,omitempty" json:"schema_validation,omitempty"`
	Redactions       []Redaction       `yaml:"redactions,omitempty" json:"redactions,omitempty"`
	Meta             map[string]any    `yaml:"meta,omitempty" json:"meta,omitempty"`
}

// Producer identifies the repository and build that produced the artifact.
// It is the consumer-side join key a central broker uses to attribute
// recorded assumptions back to the team that made them.
type Producer struct {
	Repo      string `yaml:"repo,omitempty" json:"repo,omitempty"`
	CommitSHA string `yaml:"commit_sha,omitempty" json:"commit_sha,omitempty"`
	Branch    string `yaml:"branch,omitempty" json:"branch,omitempty"`
	Build     string `yaml:"build,omitempty" json:"build,omitempty"`
	Source    string `yaml:"source,omitempty" json:"source,omitempty"`
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
	SUTBaseURL  string `yaml:"sut_base_url,omitempty" json:"sut_base_url,omitempty"`
	ProxyListen string `yaml:"proxy_listen,omitempty" json:"proxy_listen,omitempty"`
	ProxyMode   string `yaml:"proxy_mode,omitempty" json:"proxy_mode,omitempty"`
}

// SchemaRef identifies schemas attached to the test case.
type SchemaRef struct {
	ID             string       `yaml:"id" json:"id"`
	Role           string       `yaml:"role" json:"role"`
	Type           string       `yaml:"type" json:"type"`
	Source         SchemaSource `yaml:"source" json:"source"`
	OpenAPIVersion string       `yaml:"openapi_version,omitempty" json:"openapi_version,omitempty"`
}

// SchemaSource identifies where the schema came from.
type SchemaSource struct {
	Type        string   `yaml:"type" json:"type"`
	Path        string   `yaml:"path,omitempty" json:"path,omitempty"`
	URL         string   `yaml:"url,omitempty" json:"url,omitempty"`
	Content     string   `yaml:"content,omitempty" json:"content,omitempty"`
	SHA256      string   `yaml:"sha256,omitempty" json:"sha256,omitempty"`
	Format      string   `yaml:"format,omitempty" json:"format,omitempty"`
	ImportPaths []string `yaml:"import_paths,omitempty" json:"import_paths,omitempty"`
}

// Flow captures one request/response exchange.
type Flow struct {
	ID           string    `yaml:"id" json:"id"`
	Protocol     string    `yaml:"protocol" json:"protocol"`
	Channel      string    `yaml:"channel" json:"channel"`
	StartedAt    time.Time `yaml:"started_at,omitempty" json:"started_at,omitempty"`
	DurationMS   int64     `yaml:"duration_ms,omitempty" json:"duration_ms,omitempty"`
	Request      Message   `yaml:"request" json:"request"`
	Response     Message   `yaml:"response" json:"response"`
	AssertionIDs []string  `yaml:"assertion_ids,omitempty" json:"assertion_ids,omitempty"`
	// TraceID correlates the captured exchange with application traces and logs.
	TraceID string `yaml:"trace_id,omitempty" json:"trace_id,omitempty"`
	// Fingerprint is a stable hash of the interaction shape (channel, method,
	// path, status, JSON body key/type structure) for cross-run dedup.
	Fingerprint string `yaml:"fingerprint,omitempty" json:"fingerprint,omitempty"`
	// TransportError preserves an upstream connection or TLS failure alongside
	// the recorded 502 response.
	TransportError string `yaml:"transport_error,omitempty" json:"transport_error,omitempty"`
}

// Message wraps HTTP message payloads.
type Message struct {
	HTTP *HttpMessage
}

// HttpMessage represents a request or response payload.
type HttpMessage struct {
	Method   string  `yaml:"method,omitempty" json:"method,omitempty"`
	URL      string  `yaml:"url,omitempty" json:"url,omitempty"`
	Status   int     `yaml:"status,omitempty" json:"status,omitempty"`
	Headers  Headers `yaml:"headers,omitempty" json:"headers,omitempty"`
	Trailers Headers `yaml:"trailers,omitempty" json:"trailers,omitempty"`
	Body     *Body   `yaml:"body,omitempty" json:"body,omitempty"`
}

// Headers stores multi-valued headers.
type Headers map[string][]string

// Body stores raw and decoded payloads.
type Body struct {
	Encoding string `yaml:"encoding,omitempty" json:"encoding,omitempty"`
	Text     string `yaml:"text,omitempty" json:"text,omitempty"`
	Bytes    string `yaml:"bytes,omitempty" json:"bytes,omitempty"`
	// Truncated is true when only a bounded prefix was retained.
	Truncated bool `yaml:"truncated,omitempty" json:"truncated,omitempty"`
	// OriginalBytes is the complete observed size before capture truncation.
	OriginalBytes int64 `yaml:"original_bytes,omitempty" json:"original_bytes,omitempty"`
	// ContentEncoding records the original on-the-wire Content-Encoding (e.g.
	// "gzip") when the payload was transparently decompressed before storage.
	// Empty means the body was not compressed.
	ContentEncoding string       `yaml:"content_encoding,omitempty" json:"content_encoding,omitempty"`
	Decoded         *DecodedBody `yaml:"decoded,omitempty" json:"decoded,omitempty"`
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

// SchemaValidation captures schema validation results.
type SchemaValidation struct {
	Status string                  `yaml:"status,omitempty" json:"status,omitempty"`
	Errors []SchemaValidationError `yaml:"errors,omitempty" json:"errors,omitempty"`
}

// SchemaValidationError describes a single schema validation failure.
type SchemaValidationError struct {
	FlowID   string `yaml:"flow_id,omitempty" json:"flow_id,omitempty"`
	Channel  string `yaml:"channel,omitempty" json:"channel,omitempty"`
	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	Phase    string `yaml:"phase,omitempty" json:"phase,omitempty"`
	SchemaID string `yaml:"schema_id,omitempty" json:"schema_id,omitempty"`
	Message  string `yaml:"message,omitempty" json:"message,omitempty"`
}

// Redaction defines a redaction rule applied before export.
type Redaction struct {
	Field       string `yaml:"field" json:"field"`
	Rule        string `yaml:"rule" json:"rule"`
	Replacement string `yaml:"replacement,omitempty" json:"replacement,omitempty"`
}
