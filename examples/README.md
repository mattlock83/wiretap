# Examples

`basic-rest` demonstrates the complete capture-only consumer flow with
Node's built-in test runner:

- the runner starts the SUT and an external inventory dependency independently;
- the runner points the SUT's dependency URL at Phrond's existing proxy;
- `createNodeTestSuite` starts Phrond's proxy and names each capture after its test;
- test HTTP calls and downstream dependency calls go through the same proxy;
- artifacts correlate `test_to_sut` and `sut_to_dependency` traffic;
- the test asserts the consumed dependency field and declares its provider state;
- Phrond writes an artifact, HTML coverage, Postman JSON, Open Workflow YAML,
  and a standard Pact V3 consumer contract.

Run it with:

```bash
./examples/run-basic-rest-e2e.sh
```

The default path demonstrates strict validation with an existing schema. To
exercise the explicit schema-bootstrap path instead:

```bash
PHROND_GENERATE_SCHEMA=1 ./examples/run-basic-rest-e2e.sh
```

That run writes `basic-rest.generated.openapi.yaml` beside its other artifacts.

`pact-broker` demonstrates two independently owned orders and inventory
services exchanging a captured consumer contract through the real, official
Pact Broker. Its three named consumer tests produce two deduplicated Pact V3
interactions; Pact's official verifier configures the inventory provider
states, verifies the contract, and publishes the result back to the broker.

```bash
./examples/run-pact-broker-e2e.sh
```

The demo requires a running Docker daemon and uses one disposable,
SQLite-backed Broker container. See [the Pact Broker example](pact-broker/README.md).
