# Basic REST Example

Framework test: Node `node:test`

The shell runner starts and configures the demo SUT before starting the test
process. The test uses `createNodeTestSuite`, which owns only Phrond's capture
runtime and automatically records each `phrond.test(...)` name. The SUT's
dependency is configured externally with the deterministic URL:

```text
http://<PHROND_MOCK_LISTEN>/__phrond/dep/basic-rest-dependency
```

The runner includes a bounded-capture scenario. Phrond retains the first 64
bytes of a 512-byte response while the test receives and verifies the complete
response. The resulting body records:

```yaml
truncated: true
original_bytes: 512
```

## Run

```bash
./examples/run-basic-rest-e2e.sh
```

The runner sets `PHROND_MAX_CAPTURE_BYTES=64` for the demonstration. Override
it with another value below 512, or run the test against an already-started
core:

```bash
PHROND_BASE_URL=http://127.0.0.1:18000 \
PHROND_MAX_CAPTURE_BYTES=64 \
  node --test ./examples/basic-rest/basic-test.js
```

Mock responses use the SDK's `response.json` shorthand, which produces the
same literal response body and JSON content type without a templating engine.
