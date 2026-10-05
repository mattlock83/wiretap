package bundlereport

import (
	"fmt"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
)

func grpcStatusLabel(code int) string {
	names := [...]string{"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED"}
	if code < 0 || code >= len(names) {
		return "unavailable"
	}
	return fmt.Sprintf("%s (%d)", names[code], code)
}

func rejectionStatusLabel(target artifact.RejectionTarget) string {
	if target.Protocol != "grpc" {
		return fmt.Sprintf("HTTP %v", target.ExpectedStatuses)
	}
	statuses := make([]string, 0, len(target.ExpectedStatuses))
	for _, status := range target.ExpectedStatuses {
		statuses = append(statuses, grpcStatusLabel(status))
	}
	return "gRPC " + strings.Join(statuses, " or ")
}
