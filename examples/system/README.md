# Example System Under Test (SUT)

This SUT exposes a single `POST /process` endpoint. When invoked, it makes two downstream calls (`/alpha` and `/beta`) to a dependency base URL. In tests, the dependency is faked by the Phrond mock server.

## Run the SUT

```sh
SUT_LISTEN=127.0.0.1:18090 \
DEPENDENCY_BASE_URL=http://127.0.0.1:19090 \
go run ./examples/system/sut
```

## Endpoint
- `POST /process`
  - Body is forwarded to the dependency calls.
  - Response includes the downstream responses.

## Health
- `GET /healthz`
