# Basic REST capture

The runner starts the demo SUT and an external inventory dependency separately.
The SUT is configured to reach that dependency through Wiretap's existing capture
proxy; the test also calls the SUT through the same proxy. The test attempt
named `hello world happy flow` therefore contains both the inbound test request
and the correlated downstream inventory request.

Run from the repository root with Go 1.26+, Node.js 22+, Bash and curl:

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

The capture writes raw artifacts under `artifacts/<run-id>/`. Inspect
`bundle.json` and `tests/` for the exact captured attempts, including the inventory
response. The harness starts and configures the SUT and dependency, then writes
a text summary to `exports/coverage.txt`, JSON coverage to `exports/coverage.json`
and Postman collections to `exports/postman/` from the captured traffic and supplied contracts.

## Ordinary clients and parallel test traces

Run the Jest variant with the same harness, which starts the demo application,
Wiretap and dependency:

```bash
WIRETAP_TEST_RUNNER=jest ./examples/run-basic-rest-e2e.sh
```

Two overlapping Jest tests use native `fetch` and one stable dependency URL.
The demo SUT uses standard `otelhttp` server/client instrumentation; it contains
no Wiretap header forwarding. The tests check separate traces and captured
inbound and downstream payloads in their own test-attempt evidence.
The shell harness starts Wiretap with [.wiretap.yaml](.wiretap.yaml) alongside the
demo services. Native Node suite hooks or Jest global teardown finalize capture
over HTTP; Jest workers only own their test attempts. Raw artifacts are written
automatically. Finalization generates the selected exports and returns their exact paths to the harness.
The default Node variant uses sequential capture.

For an uninstrumented SUT with serial capture:

```bash
DEMO_DISABLE_OTEL=1 ./examples/run-basic-rest-e2e.sh
```

This deliberately disables the demo's OTel instrumentation. Inbound calls still
belong to the test; downstream calls use the single active session and timing
inference. Do not combine this mode with shared-SUT parallel Jest capture.
