package artifact

import "strings"

// UnsupportedRPCProtocol identifies removed native RPC protocol names while
// leaving unrelated custom event protocols available for opaque recording.
func UnsupportedRPCProtocol(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "grpc", "grpcs", "grpc-web", "grpcweb", "grpc_web", "grpc-web-text", "grpc+proto", "grpc+json", "grpc-web+proto", "grpc-web+json", "grpc-web-text+proto", "grpc-web-text+json":
		return true
	default:
		return false
	}
}
