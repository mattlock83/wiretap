# Basic REST capture

The runner starts the demo SUT and an external inventory dependency separately.
The SUT is configured to reach that dependency through Phrond's existing capture
proxy; the test also calls the SUT through the same proxy. One artifact named
`hello world happy flow` therefore contains both the inbound test request and
the correlated downstream inventory request.

```bash
./examples/run-basic-rest-e2e.sh
```

The test also asserts `$.available` on the captured inventory response and
declares the `product is available` Pact provider state. The run writes the
per-test artifact plus HTML, Postman, Open Workflow YAML, and Pact V3 reports.
The Pact contract includes only the asserted inventory response field while
the artifact retains the complete response. Phrond never starts, configures,
or simulates the SUT or dependency.

## Ordinary clients and parallel test traces

Install dependencies once with `npm ci --prefix sdk/js`, then run the Jest
example against the same real SUT, Phrond process, and existing dependency:

```bash
PHROND_TEST_RUNNER=jest ./examples/run-basic-rest-e2e.sh
```

Two overlapping Jest tests use native `fetch` and one stable dependency URL.
The demo SUT uses standard `otelhttp` server/client instrumentation; it contains
no Phrond header forwarding. The tests check separate traces, captured inbound
and downstream payloads, and dependency assertions in their own artifacts.
The shell harness owns all process lifecycles; Jest workers do not stop Phrond.

For an uninstrumented SUT with serial capture:

```bash
DEMO_DISABLE_OTEL=1 ./examples/run-basic-rest-e2e.sh
```

This deliberately disables the demo's OTel instrumentation. Inbound calls still
belong to the test; downstream calls use the single active session and timing
inference. Do not combine this mode with shared-SUT parallel Jest capture.
