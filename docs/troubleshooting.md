# Troubleshooting

## Proxy or Mock Server Will Not Start
- Check port conflicts. Override with:
  - `PHROND_HTTP_LISTEN`
  - `PHROND_PROXY_LISTEN`
  - `PHROND_MOCK_LISTEN`
- Verify the process has permissions to bind to the port.

## HTTPS Requests Fail With TLS Errors
- Ensure the proxy CA certificate is trusted.
- Confirm `PHROND_PROXY_MODE=mitm` for full capture.
- If you cannot trust the CA in CI, switch to `PHROND_PROXY_MODE=passthrough`.

## SDK Requests Return 404
- Ensure the recorder core is running and reachable.
- Confirm the SDK `baseUrl` matches the core HTTP listen address.

## Tests Fail With Localhost Permission Errors
- Some environments restrict local TCP listeners. Run tests with the required permissions or configure your test runner to allow localhost binds.

## No Traffic Captured
- Test→SUT: send test traffic to the proxy address (`sut_url` /
  `runtime.sutUrl`) — with reverse capture the proxy forwards to the SUT
  target configured at `startServer` (`sutBaseUrl` in the SDK).
- SUT→mock: point each downstream dependency at its stable URL from
  `runtime.dependencyUrl(assetId)` (or `dependency_urls` in the `setupTest`
  response).
- For HTTPS targets via CONNECT, check the proxy is in `mitm` mode and the CA
  is trusted; plain-HTTP reverse capture needs no CA.

## Truncated Bodies in Artifacts

- Phrond forwards complete request and response bodies but retains only the
  first 4 MiB per body by default.
- A bounded body has `truncated: true` and `original_bytes` in the artifact.
- Set `PHROND_MAX_CAPTURE_BYTES` to a non-negative byte count before starting
  Phrond. `0` explicitly enables unlimited capture and should be used
  cautiously in CI.
- Body schema validation and JSON-shape fingerprinting are skipped for a
  truncated body; method, path, headers, query, and response status are still
  validated and fingerprinted normally.
- Run `./examples/run-basic-rest-e2e.sh` for a complete example that delivers
  a 512-byte response while retaining a 64-byte artifact prefix.

## setupTest Returns 409 "zero-config session is already active"
- Only one zero-config session may be active at a time. Either run tests
  serially (make sure every test tears down its session — `bindTest` does this
  automatically), or set `explicit_session: true` for parallel suites.

## Mock Returns 501 "no matching stub"
- The flow is recorded with `unmatched: true` — the SUT made a dependency call
  no queued response models. Queue a response for it, or check the request was
  routed to the right dependency URL (asset-scoped stubs only match requests
  sent to that dependency's base URL).
- With multiple declared dependencies, `queue-response` needs `asset_id`.
- External dependencies do not use Phrond's stub matcher. Configure the route
  with `mock.mode: external`, point `mock.upstream_url` at the external server,
  and configure stubs through that server's API.
