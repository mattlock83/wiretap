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
