# Native gRPC exploration inside a Go test

This example captures one happy-path `CreateOrder` call, then calls
`session.Explore` from the same Go test. Wiretap generates invalid inputs from
standard Protovalidate annotations, executes them over native gRPC, and records
all attempts in the same OKF bundle. The Go test framework owns the test result.

From the repository root:

```bash
./examples/grpc-exploration/run.sh
```

The script prints the bundle and `docs/viewer.html` paths. Output defaults to
`artifacts/grpc-exploration`; set `WIRETAP_GRPC_EXPLORATION_ARTIFACT_DIR` to change
it. Requirements are Go, Python 3 and curl. No protoc, generated client code,
OpenTelemetry collector or separate test runner is needed for this example.

Reflection is the default contract source. To demonstrate a service with
reflection disabled, use the same frozen local descriptor set instead:

```bash
WIRETAP_GRPC_CONTRACT_SOURCE=descriptor_set ./examples/grpc-exploration/run.sh
```

The script emits that descriptor set from the checked-in `fixture/orders.proto`,
including the imported validation definitions. Both modes exercise the same
contract and enforce it using the actual Protovalidate Go library.

## What the test checks

The application requires authentication and an existing user. Every fixture
reset creates a different user and token, so replaying the original request
without hooks would fail before reaching meaningful validation.

`exploration_test.go` keeps setup in ordinary test code:

1. Start the application and make a successful native grpc-go request.
2. Assert that the order exists and its response contains an ID.
3. Explore only `/wiretap.example.orders.v1.Orders/CreateOrder`.
4. Reset the fixture before the baseline and every variation; bind the new user
   into the request and provide fresh authorization metadata before sending.
5. Check that the baseline writes one row and rejected requests write none.
6. Clean up each case and fail the parent Go test if any check fails.

The declared rules cover a required nonempty user ID, SKU length and a quantity
range. Generated checks expect `INVALID_ARGUMENT`; authentication errors,
timeouts and internal failures do not count as validation success. There is no
default case cap.

The bundle includes the authored happy path, fresh baseline, individual generated
checks, rule targets, exact protobuf payloads and coverage. The script rebuilds
the static report after the application has stopped, demonstrating that the
bundle carries the contract and evidence needed for reporting. Authorization
metadata is redacted before evidence is written.

## Fixture reuse

The fixture also implements server streaming (`WatchOrders`), client streaming
(`CreateOrders`) and bidirectional streaming (`ChatOrders`) for Wiretap's runtime
integration tests. The example deliberately scopes exploration to `CreateOrder`;
the other declared methods remain visibly uncovered in its bundle.

The protobuf service uses dynamic messages to keep the example self-contained.
Applications can keep their normal generated clients and services: SDK calls
only manage the exploration lifecycle through Wiretap's HTTP interface. Hooks
remain project code and are never embedded in the bundle.
