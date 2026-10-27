# Add Wiretap to existing Jest tests

Wiretap records the HTTP traffic your Jest tests already send, retains their
outcomes and portable evidence, and produces API coverage, an offline HTML
dashboard and eligible JSON/REST Arazzo workflows. Static checks compare captured
mocks with provider test evidence. Keep your assertions, clients and mocks.
Use synthetic data and dummy credentials; retained traffic is unchanged.

## Try the external Jest example

With Git, Go 1.26.10+ and Node.js 22+ installed, run from this checkout:

```sh
node examples/existing-jest/run.mjs artifacts/existing-jest
```

The script clones a pinned external Express/Jest suite, runs its unchanged tests,
builds Wiretap, installs the local SDK and reruns with capture enabled. It checks
all 120 original outcomes, HTML/JSON coverage, and Arazzo conformance and fidelity
to raw archive records. Existing failing tests are intentional; the harness
succeeds when capture preserves those results. See the
[example guide](../examples/existing-jest/README.md) for integration changes,
output layout and retained evidence.

## Add capture to your project

The [Jest guide](../sdk/js/docs/jest.md), also included in the SDK package, is the
complete integration guide:

1. [Install the runtime and SDK](../sdk/js/docs/jest.md#install-the-runtime-and-sdk).
2. [Configure the API](../sdk/js/docs/jest.md#configure-the-api) and
   [wrap your Jest configuration](../sdk/js/docs/jest.md#wrap-the-jest-configuration).
3. Choose [an existing API](../sdk/js/docs/jest.md#an-already-running-api) or
   [dynamic fixtures](../sdk/js/docs/jest.md#dynamic-fixtures-and-session-settings),
   and configure [tracing before concurrency](../sdk/js/docs/jest.md#tracing-and-concurrency).
4. [Retain and inspect the evidence](../sdk/js/docs/jest.md#retain-and-inspect-the-evidence)
   and [check captured mocks against provider tests](../sdk/js/docs/jest.md#check-captured-mocks-against-provider-tests).

See [configuration](configuration.md) for project fields, [CI](ci-example.md)
for retention and gates, or the [documentation index](README.md) for references.
