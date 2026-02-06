# gRPC Example

This example drives a gRPC-only SUT through the recorder core and captures gRPC traffic.
Proto definition lives in `examples/grpc/examplepb/example.proto`.
For complex proto imports, pass `source.import_paths` or use a descriptor set (set `source.format = "descriptor"` and point `source.path` or `source.url` at the `.desc` file).

## Descriptor set helper
If you have `protoc` installed, you can generate a descriptor set for this example:

```bash
./examples/grpc/gen-descriptor.sh
```

Then set `PROTO_DESCRIPTOR` when running the example to use the `.desc` file:

```bash
PROTO_DESCRIPTOR=examples/grpc/examplepb/example.desc ./examples/run-grpc-e2e.sh
```

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
go run ./examples/grpc/client
```

Artifacts are written to `./artifacts` by default.
