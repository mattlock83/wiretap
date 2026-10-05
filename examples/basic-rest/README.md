# Basic REST capture and exploration

The runner starts the demo SUT and an external inventory dependency separately.
The SUT is configured to reach that dependency through Wiretap's existing capture
proxy; the test also calls the SUT through the same proxy. The test attempt
named `hello world happy flow` therefore contains both the inbound test request
and the correlated downstream inventory request.

Run from the repository root with Go 1.24+, Node.js 20.12+, Bash and curl:

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

The capture writes a bundle under `artifacts/<run-id>/`. Open
`docs/viewer.html` for the offline dashboard or `index.md` for the OKF pages.
Inspect `tests/` for the exact captured attempts, including the inventory
response. The harness starts and configures the SUT and dependency. The Dependencies page
and HTML dashboard also link to an inferred OpenAPI draft for `inventory-service`.
It is generated from observed mock traffic alongside the supplied spec, which
continues to control validation.

After capture succeeds, the script runs `wiretap explore` using the happy flow
as its seed. It checks that missing, null and incorrectly typed inputs receive
a documented HTTP 400 response, then verifies the generated rejection coverage.
Exploration writes a separate bundle and leaves the capture unchanged. Both
bundle paths are printed; open the exploration dashboard's **Contract coverage →
Invalid-input coverage** to see the results.

Finalizing capture stops its proxy, so the harness restarts this stateless demo
SUT with a direct connection to the same inventory mock before exploration.
Exploration does not replay or capture downstream mock traffic. For providers
that need authentication or database fixtures, see the
[external hooks example](../provider-exploration/demo.py) and
[exploration guide](../../docs/exploration.md).

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
over HTTP; Jest workers only own their test attempts. The bundle and views are
written automatically, without a report command.
The harness then explores the passing `processes 123` test from that capture.

For an uninstrumented SUT with serial capture:

```bash
DEMO_DISABLE_OTEL=1 ./examples/run-basic-rest-e2e.sh
```

This deliberately disables the demo's OTel instrumentation. Inbound calls still
belong to the test; downstream calls use the single active session and timing
inference. Do not combine this mode with shared-SUT parallel Jest capture.
