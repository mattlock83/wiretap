# Native gRPC streaming with an external runtime

This advanced fixture keeps one shell harness as the runtime owner so it can
obtain the dynamic dependency listener before starting the provider. Tests use
`Runtime.StartTest`; the shell finalizes once after them. For a normal Go test
package, start with the managed [`Run` / `StartTest` guide](../../sdk/go/README.md).

Run from the repository root:

```sh
./examples/grpc-streaming/run.sh
```

Requires Go 1.26+, Bash, Python 3 and curl. The script builds and starts Wiretap, a provider
and its existing mock dependency, runs ordinary Go tests, finishes the bundle,
and stops the processes. Normal assertion failures still finalize coverage and
retain the Go test command's failure status. No protoc, reflection or collector
is required.

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

Raw captures are written to `artifacts/grpc-streaming/run-*/`. Inspect
`bundle.json` and `tests/*.json` for messages, raw `.bin` payload references,
readable Protobuf JSON, message timing, half-close and terminal metadata.
`exports/coverage.json` contains the exported JSON coverage and
`exports/coverage.txt` contains its text summary. Override the destination with:

```sh
WIRETAP_GRPC_STREAMING_ARTIFACT_DIR=/tmp/wiretap-streaming ./examples/grpc-streaming/run.sh
```

`last-run.json` inside each archive records its exact paths. Set
`WIRETAP_EXAMPLE_FAIL=1` to make the unary test's user cleanup fail and verify
that the command returns nonzero while retaining finalized evidence and coverage.

Complete passing streams can cover methods and the structural Protobuf field
conditions they actually carry. An empty stream covers its method but contributes
no payload conditions for its empty direction. Stream length combinations and
cross-message business rules are not invented coverage targets. Omitted,
interrupted or undecodable evidence cannot establish complete stream coverage.

The repeatable integration suite also exercises HTTP provider → gRPC dependency,
gRPC provider → HTTP dependency, TLS on capture and upstream listeners,
concurrent W3C trace ownership, cancellation,
drain snapshots, retention limits, and rebuilding coverage from frozen contracts:

```sh
go test ./internal/app -run 'TestNativeGRPC(AllShapes|EmptyStreams|StreamOmissions|BidiDrain|StreamCancellation|ParallelStreams|StreamingTLS)' -count=1
```
