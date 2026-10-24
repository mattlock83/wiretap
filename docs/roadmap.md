# Wiretap product scope

Wiretap has one job: capture existing HTTP and native gRPC API tests and produce
portable evidence, Postman collections and API-level coverage. It runs locally
in isolated test environments with synthetic data and dummy secrets.

## Completion criteria

- Keep existing test frameworks, clients, assertions and mocks. Framework
  outcomes and contract validation are separate evidence.
- Make one ordinary suite command handle startup, readiness, all test workers,
  finalization, exports and cleanup, including failed tests.
- Capture inbound and dependency traffic with explicit test ownership. Support
  native unary and streaming gRPC alongside HTTP.
- Freeze declared contracts and report untested operations even in empty runs.
- Retain raw evidence, stable identities, build provenance and explicit limits
  so another tool can compare runs without access to the original environment.
- Produce Postman HTTP v2.1 and gRPC v3 exports with automated compatibility
  checks and clear replay limitations.
- Keep installation local, document the supported setup and test it across
  Linux, macOS and Windows.

The [quickstart](getting-started-existing-tests.md) and [examples](../examples/README.md)
show the supported workflow. The [machine interface](machine-interface.md)
defines the outputs available to downstream tools. Current protocol and coverage
limits are documented in the [gRPC guide](grpc-user-guide.md) and
[coverage rules](contract-coverage-rules.md).

## Boundaries

Captures retain synthetic test data as sent. Contract mismatches
are recorded without replacing the framework's outcome. Capture failures and
invalid configuration still fail integration cleanup or setup.

Cross-repository assumption matching and regression comparison belong in a
separate future diff tool that consumes these files. Test generation, dependency
simulation, hosted registries and managed test execution are outside Wiretap's
scope. A mock's response is evidence of an assumption, not verified provider
agreement.

Supported integrations are Jest, Playwright Test, Mocha, plain JavaScript and
Go's standard `testing` package. Other runners and languages can use the
[runtime HTTP API](runtime-api.md). Each integration keeps the framework's own
assertions and final result while recording capture and export failures.
