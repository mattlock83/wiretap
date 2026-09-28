package coverage

import (
	"github.com/phrond/evermock/internal/artifact"
	"strings"
)

// Captured tracing context is evidence, never a reusable replay identity.
func replayHeaders(headers artifact.Headers) artifact.Headers {
	result := artifact.Headers{}
	for key, values := range headers {
		switch strings.ToLower(key) {
		case "traceparent", "tracestate", "x-phrond-session-id":
			continue
		}
		result[key] = append([]string(nil), values...)
	}
	return result
}
