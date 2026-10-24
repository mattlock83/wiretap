# Wiretap product scope

Wiretap makes existing HTTP and native gRPC API tests portable and comparable:
capture evidence, export Postman collections and API-level coverage, and verify
consumer mock examples against provider tests. It runs locally
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
  so checks and downstream tools can compare runs without the original environment.
- Produce Postman HTTP v2.1 and gRPC v3 exports with automated compatibility
  checks and clear replay limitations.
- Check selected consumer mock requests and paired responses against complete
  passing provider service tests, with explicit uncovered and unverified results.
- Compare supported frozen HTTP validation rules on exercised operations and
  selected statuses/media, even when the recorded values satisfy both contracts.
- Compare exercised Protobuf method input/output declarations, including full
  reachable enum sets, alongside mandatory observed wire cross-decoding.
- Verify supplied Postman collections against their source archives before
  accepting them as part of an assumption check.
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

The offline [assumption checker](assumption-checks.md) requires provider test
evidence for each selected concrete mock request and paired response. It fails
for incompatible, uncovered or unverified assumptions. A passing result supports
those examples and the recorded contract-check assurance, not arbitrary business
behavior or a live deployment. HTTP rule equality is conservative: enum
additions or relaxed limits can fail without proving a runtime break. Only
exercised operations and selected statuses/media receive that HTTP comparison.
gRPC checks the selected method's reachable declarations and observed wire
compatibility, without executing custom validation annotations. Both omitted
HTTP schemas allow an explicitly marked example-only match.
Test generation, dependency simulation, hosted registries and managed test
execution are outside Wiretap's scope. Capturing a mock response alone does not
establish provider agreement.

Supported integrations are Jest, Playwright Test, Mocha, plain JavaScript and
Go's standard `testing` package. Other runners and languages can use the
[runtime HTTP API](runtime-api.md). Each integration keeps the framework's own
assertions and final result while recording capture and export failures.
