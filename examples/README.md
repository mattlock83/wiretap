# Examples

`basic-rest` demonstrates capture followed by provider exploration with
Node's built-in test runner:

- the runner starts the SUT and an external inventory dependency independently;
- the runner points the SUT's dependency URL at Wiretap's existing proxy;
- the harness starts Wiretap with `.wiretap.yaml` as another service;
- native test hooks call the SDK to start capture and finalize the run;
- ordinary `test` callbacks call `runTest` and use ordinary fetch;
- test HTTP calls and downstream dependency calls go through the same proxy;
- the bundle correlates `test_to_sut` and `sut_to_dependency` traffic;
- Wiretap writes one portable bundle with JSON evidence, an offline HTML
  dashboard, and OKF API, coverage, dependency-assumption, and test pages;
- the harness reconnects the SUT directly to the mock after capture finishes;
- `wiretap explore` checks generated invalid requests from the happy flow and
  writes a separate bundle with rejection coverage.

Run it with:

```bash
./examples/run-basic-rest-e2e.sh
```

The example validates traffic against supplied OpenAPI schemas.

For a real application with state and downstream HTTP calls, see
[Microsvc Transactions](microsvc-transactions/README.md):

```bash
./examples/run-microsvc-transactions.sh
```

It reuses local Speedscale Microsvc Docker images, captures withdrawal tests
against an Accounts mock, then explores invalid requests with authentication
and fixture hooks. The database and example containers are disposable.
