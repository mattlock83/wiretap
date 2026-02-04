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
- Ensure the SUT is configured to use the proxy for outbound requests.
- Verify downstream dependencies are pointed at the mock server.
- Check that the proxy is in `mitm` mode and the CA is trusted for HTTPS traffic.
