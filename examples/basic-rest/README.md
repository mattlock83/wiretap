# Basic REST capture

The runner starts the demo SUT and an external inventory dependency separately.
The SUT is configured to reach that dependency through Phrond's existing capture
proxy; the test also calls the SUT through the same proxy. The test attempt
named `hello world happy flow` therefore contains both the inbound test request
and the correlated downstream inventory request.

```bash
./examples/run-basic-rest-e2e.sh
```

The run writes a bundle under `artifacts/<run-id>/`. Open `viewer/index.html` for
the offline HTML dashboard or `index.md` for the OKF pages. Inspect `evidence/` for the exact captured
attempts. The retained evidence includes the inventory response. Phrond never
starts, configures, or simulates the SUT or dependency.

## Ordinary clients and parallel test traces

Install dependencies once with `npm ci --prefix sdk/js`, then run the Jest
example against the same real SUT, Phrond process, and existing dependency:

```bash
PHROND_TEST_RUNNER=jest ./examples/run-basic-rest-e2e.sh
```

Two overlapping Jest tests use native `fetch` and one stable dependency URL.
The demo SUT uses standard `otelhttp` server/client instrumentation; it contains
no Phrond header forwarding. The tests check separate traces and captured
inbound and downstream payloads in their own test-attempt evidence.
The shell harness starts Phrond with [.phrond.yaml](.phrond.yaml) alongside the
demo services. Native Node suite hooks or Jest global teardown finalize capture
over HTTP; Jest workers only own their test attempts. The bundle and views are
written automatically, without a report command.

For an uninstrumented SUT with serial capture:

```bash
DEMO_DISABLE_OTEL=1 ./examples/run-basic-rest-e2e.sh
```

This deliberately disables the demo's OTel instrumentation. Inbound calls still
belong to the test; downstream calls use the single active session and timing
inference. Do not combine this mode with shared-SUT parallel Jest capture.
