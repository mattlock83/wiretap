# Native gRPC streaming

Run from the repository root:

```sh
./examples/grpc-streaming/run.sh
```

Requires Go, Python 3 and curl. The script builds and starts Wiretap, a provider
and its existing mock dependency, runs ordinary Go tests, finishes the bundle,
and stops the processes. No protoc, reflection or collector is required.

The checked-in `fixture/stream.proto` declares all four native gRPC shapes:

| Method | Shape | Example |
| --- | --- | --- |
| `Echo` | Unary | One request and response |
| `Watch` | Server stream | One request and several responses |
| `Collect` | Client stream | Several requests and one response |
| `Chat` | Bidirectional | Each response arrives before the next request |

Tests also include gzip and empty streams. Every provider call makes an ordinary
bidirectional `Chat` call to the mock through its Wiretap capture address, so the
bundle contains both tested provider behavior and dependency assumptions. The
SDK only handles test lifecycle; traffic uses ordinary grpc-go clients.

Bundles are written to `artifacts/grpc-streaming/run-*/`. Open
`docs/viewer.html` in the latest run to inspect messages, raw `.bin` payloads,
readable Protobuf JSON, message timing, half-close and terminal metadata.
`bundle.json` and `tests/*.json` hold canonical evidence; the viewer is a derived
view of the same bundle. Override the destination with:

```sh
WIRETAP_GRPC_STREAMING_ARTIFACT_DIR=/tmp/wiretap-streaming ./examples/grpc-streaming/run.sh
```

Complete passing streams can cover methods and the structural Protobuf field
conditions they actually carry. An empty stream covers its method but contributes
no payload conditions for its empty direction. Stream length combinations and
cross-message business rules are not invented coverage targets. Omitted,
interrupted or undecodable evidence cannot establish complete stream coverage.

The repeatable integration suite also exercises HTTP provider → gRPC dependency,
gRPC provider → HTTP dependency, TLS on capture and upstream listeners,
concurrent W3C trace ownership, cancellation,
drain snapshots, retention limits, and rebuilding reports from frozen contracts:

```sh
go test ./internal/app -run 'TestNativeGRPC(AllShapes|EmptyStreams|StreamOmissions|BidiDrain|StreamCancellation|ParallelStreams|StreamingTLS)' -count=1
```
