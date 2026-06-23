# Persistent Mock Server

A single-file mock server built directly on the phrond `mock` package, for when
you want a **long-lived** mock rather than the per-test SDK lifecycle — for
example, a persistent dependency fake pointed at a running post-merge dev
environment.

It uses the built-in matcher with static `Default` routes (registered once, never
consumed, persistent for the life of the process). Responses are literal — there
is no templating.

## Run

```bash
# Fully mocked: every defined route returns its static response.
go run ./examples/persistent-mock -addr 127.0.0.1:8080

# Override-and-forward: defined routes are mocked, everything else is forwarded
# to a real upstream (e.g. your dev environment).
go run ./examples/persistent-mock -addr 127.0.0.1:8080 -upstream https://dev.internal.example.com
```

Point your SUT's dependency base URL at `http://127.0.0.1:8080`.

## Customize

Edit the `routes()` function in `main.go` to define your own method/path →
status/headers/body responses. Each route is a `mock.Stub` with `Default: true`.

## Notes

- Recording is disabled (`SetRecordingEnabled(false)`) so a long-running process
  does not accumulate exchanges in memory. This server does not write artifacts;
  use the SDK lifecycle when you want recorded, schema-validated test artifacts.
- With `-upstream`, unmatched requests are forwarded and the response is returned
  as-is.
