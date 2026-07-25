# Examples

## Test Examples

- `basic-rest` (Node `node:test`) — one SUT with one declared dependency;
  shows the consumer-facing `createNodeTestSuite` helper, named tests, JSON
  mock shorthand, stable dependency URLs, `verify`, and bounded body capture
  without truncating delivery. Its runner owns the SUT process.
- `complex-rest` (Node `node:test`) — a mixed-mode suite where one service
  uses a user-owned external mock and another uses Phrond's built-in mock;
  shows BYO mock capture, multi-service correlation, and forward-proxy
  override. Its runner owns both application processes.

Each example folder has its own test and helper files.

## Persistent Mock

- `persistent-mock` — single-file long-lived mock server (static default
  routes + optional upstream fallback) for running against a dev environment.

## Demo Apps

Demo SUT/dependency binaries used by tests live in:

- `demo-apps/basic-rest`
- `demo-apps/complex-rest`

## Runners

- `./examples/run-basic-rest-e2e.sh`
- `./examples/run-complex-rest-e2e.sh`
