# Basic REST capture

The runner starts the demo SUT and an external inventory dependency separately.
The SUT is configured to reach that dependency through Phrond's existing capture
proxy; the test also calls the SUT through the same proxy. One artifact named
`hello world happy flow` therefore contains both the inbound test request and
the correlated downstream inventory request.

```bash
./examples/run-basic-rest-e2e.sh
```

The run writes the per-test artifact plus HTML, Postman, and Open Workflow YAML
reports. Phrond never starts, configures, or simulates the SUT or dependency.
