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
