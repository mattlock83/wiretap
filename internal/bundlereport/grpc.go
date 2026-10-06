package bundlereport

import (
	"fmt"
)

func grpcStatusLabel(code int) string {
	names := [...]string{"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED"}
	if code < 0 || code >= len(names) {
		return "unavailable"
	}
	return fmt.Sprintf("%s (%d)", names[code], code)
}
