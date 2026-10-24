# Native gRPC with an external runtime

This advanced fixture keeps the shell as the sole runtime owner. It starts
capture first so the SUT can use the dynamically allocated gRPC dependency
listener. The Go test therefore uses `Runtime.StartTest`; the shell finalizes
after the test command, including normal test failures. Do not add `wiretap.Run`
to this fixture. For the usual standard-testing integration, use
[`Run` and `StartTest`](../../sdk/go/README.md).

For native Node clients and mocks, traced parallel tests, mixed HTTP/gRPC paths
and TLS, use the [Node companion example](node/README.md). The Go example below
remains independent of Node.

Requires Go 1.26+, Bash, Python 3 and curl.

Run `./examples/grpc-boundaries/run.sh` from the repository root. It builds
Wiretap and two small grpc-go servers, runs an existing Go test through the
capture listeners, finalizes the run, and prints the raw capture directory and
`exports/coverage.json` path. JSON and text coverage are selected in the generated
project configuration and written during finalization.
`last-run.json` inside the archive records the exact output paths. Set
`WIRETAP_EXAMPLE_FAIL=1` to exercise a failing user cleanup: the command returns
nonzero and still writes the completed archive and coverage.

The test keeps its generated `HealthClient`; the SUT keeps its generated client
for the mock. Both servers advertise reflection, so the Wiretap configuration
only selects the service. The setup call freezes both contracts before the test
RPC begins. The raw attempts in `tests/` should show one `test_to_sut` RPC, one
`sut_to_dependency` RPC, their Protobuf payloads, and the selected methods
including methods this test did not call.

This example runs one test at a time. Shared parallel tests require standard
OpenTelemetry gRPC context propagation through the SUT, or separate runtimes.
See the [gRPC guide](../../docs/grpc-user-guide.md).

To run the same test with reflection **disabled on both servers**, use:

```bash
WIRETAP_GRPC_CONTRACT_SOURCE=descriptor_set ./examples/grpc-boundaries/run.sh
```

The script builds a binary descriptor set from the generated test service
definition and supplies it as each boundary's local contract. Wiretap neither
queries reflection nor needs protoc or Buf installed for this mode.

To compile the checked-in source files inside Wiretap, with reflection disabled
on both servers:

```bash
WIRETAP_GRPC_CONTRACT_SOURCE=proto ./examples/grpc-boundaries/run.sh
```

The local `health.proto` imports `health-types.proto`; no network lookup or
external compiler is involved. It describes the existing generated client’s
wire types. Its split file layout differs from the generated descriptor set,
so its contract revision differs even though the RPCs are compatible.

To mix source types, with reflection on the SUT and local source files for the
mock whose reflection is disabled:

```bash
WIRETAP_GRPC_CONTRACT_SOURCE=mixed ./examples/grpc-boundaries/run.sh
```

All modes allocate dynamic capture ports at `startRun`, then use the returned
dependency origin to start the SUT. First-test setup resolves both upstreams
after they are ready. This startup order is the reason for the explicit external
runtime ownership. Ordinary generated clients and assertions are unchanged.
