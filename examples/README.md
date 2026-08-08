# Examples

`basic-rest` demonstrates the complete capture-only consumer flow with
Node's built-in test runner:

- the runner starts the SUT independently;
- `createNodeTestSuite` starts Phrond's proxy and names each capture after its test;
- test HTTP calls go through the reverse proxy;
- Phrond writes an artifact, HTML coverage, Postman JSON, and Open Workflow YAML.

Run it with:

```bash
./examples/run-basic-rest-e2e.sh
```
