# Examples

`basic-rest` demonstrates the complete capture-only consumer flow with
Node's built-in test runner:

- the runner starts the SUT and an external inventory dependency independently;
- the runner points the SUT's dependency URL at Phrond's existing proxy;
- the harness starts Phrond with `.phrond.yaml` as another service;
- native test hooks call the SDK to start capture and finalize the run;
- ordinary `test` callbacks call `runTest` and use ordinary fetch;
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
