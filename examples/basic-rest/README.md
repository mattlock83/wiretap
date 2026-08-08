# Basic REST capture

The runner starts the demo SUT separately. The test starts only Phrond's
capture proxy, calls the SUT through it, and automatically records an artifact
named `hello world happy flow`.

```bash
./examples/run-basic-rest-e2e.sh
```

The run writes the per-test artifact plus HTML, Postman, and Open Workflow YAML
reports. Phrond never starts or configures the SUT.
