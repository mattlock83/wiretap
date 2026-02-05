# JavaScript Example

This example shows how to use the JS SDK to call the recorder core and generate an artifact.

## Run
1. Start the core service:

```sh
go run ./cmd/phrond
```

2. Run the example:

```sh
node examples/js/basic-test.js
```

3. The artifact will be written to `./artifacts` by default. Override with:

```sh
PHROND_ARTIFACT_DIR=./tmp/artifacts go run ./cmd/phrond
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
