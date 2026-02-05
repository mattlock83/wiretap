# gRPC Example

This example drives a gRPC-only SUT through the recorder core and captures gRPC traffic.

## What it does
- Starts the recorder core (Go)
- Starts a gRPC mock server (via SDK)
- Runs a gRPC SUT that calls two downstream methods
- Sends a gRPC request through the gRPC proxy
- Writes a YAML artifact with gRPC traffic

## Run
From the repo root:

```bash
./examples/run-grpc-e2e.sh
```

Or run manually:

```bash
go run ./cmd/phrond
node ./examples/grpc/basic-test.js
```

Artifacts are written to `./artifacts` by default.
