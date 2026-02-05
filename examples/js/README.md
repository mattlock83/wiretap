# JavaScript Example

This example spins up a simple SUT that calls two downstream endpoints and exercises the proxy + mock server.

## Run
1. Start the core service:

```sh
go run ./cmd/phrond
```

2. Run the example:

```sh
node examples/js/basic-test.js
```

## One-Command Run
You can run the full flow with:

```sh
./examples/run-e2e.sh
```

The script will:
- Start a test case and the proxy/mock servers
- Launch the SUT in `examples/system/sut`
- Send a POST through the proxy
- Write an artifact to `./artifacts`

### Environment Overrides
You can override ports and artifact output with:

```sh
PHROND_PORT=18001 SUT_PORT=18091 PROXY_PORT=18081 MOCK_PORT=19091 ARTIFACT_DIR=./tmp/artifacts ./examples/run-e2e.sh
```

## Polling Test Case Status
You can check progress with:

```sh
curl http://127.0.0.1:18000/v1/testcases/status
```

## Downloading the Artifact
The artifact can be downloaded directly from the core service:

```sh
curl -L http://127.0.0.1:18000/v1/artifact -o artifact.yaml
```

Or via the JS SDK:

```js
await harness.downloadArtifact("./artifact.yaml");
```
