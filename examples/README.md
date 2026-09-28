# Examples

`basic-rest` demonstrates the complete capture-only consumer flow with
Node's built-in test runner:

- the runner starts the SUT and an external inventory dependency independently;
- the runner points the SUT's dependency URL at Phrond's existing proxy;
- `createNodeTestSuite` starts Phrond's proxy and names each capture after its test;
- test HTTP calls and downstream dependency calls go through the same proxy;
- the bundle correlates `test_to_sut` and `sut_to_dependency` traffic;
- Phrond writes one portable bundle with JSON evidence, an offline HTML
  dashboard, and OKF API, coverage, dependency-assumption, and test pages.

Run it with:

```bash
./examples/run-basic-rest-e2e.sh
```

The default path demonstrates strict validation with an existing schema. To
exercise the explicit schema-bootstrap path instead:

```bash
PHROND_GENERATE_SCHEMA=1 ./examples/run-basic-rest-e2e.sh
```

That run also writes `basic-rest.generated.openapi.yaml` as an explicit schema
export.
