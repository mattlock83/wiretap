# Complex REST Example

Framework test: Node `node:test`

This is a runnable mixed-mode example:

- the payment SUT calls a small user-owned account mock through an
  `external` Phrond dependency;
- the transaction service calls Phrond's built-in account mock;
- both dependency flows are captured and attributed in the same test
  artifact.

The in-process HTTP server in `test-helpers.js` stands in for WireMock,
MockServer, or another mock framework. It deliberately owns its own response
setup and request verification; Phrond only gateways and records its traffic.

## Run

```bash
./examples/run-complex-rest-e2e.sh
```

The runner starts Phrond and the two demo services, runs the tests, and emits
the captured artifacts plus Workflow YAML, Postman, and HTML coverage reports.
To run the test file against services you already started:

```bash
PHROND_BASE_URL=http://127.0.0.1:18000 \
  node --test ./examples/complex-rest/payment-test.js
```
