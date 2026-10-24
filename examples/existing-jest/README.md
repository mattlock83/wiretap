# Add Wiretap to an existing Express/Jest suite

This demo clones [Ziad Al-Moqamar's property-management API](https://github.com/ZiadAlMoqamar/Property-Management-API-Testing-Using-Jest)
at commit `77d7d017b948c3456889f199ab7d05070b33bd4d`. It has an Express API,
Jest 29, Supertest, an in-memory mock datastore and 120 tests in ten files. The
upstream repository retains its own MIT license and attribution. It does not
include an API specification; this overlay supplies an OpenAPI contract derived
from its routes and existing test expectations.

From the Wiretap checkout, with Git, Go 1.26+ and Node 22+:

```bash
node examples/existing-jest/run.mjs
```

An optional argument selects a new output directory. The script clones the demo,
runs its unchanged tests, builds Wiretap, packs and installs the SDK locally,
then applies the integration and runs the same tests. It publishes nothing.
It leaves the demo, binary, package, logs, baseline results and captured artifacts
in the printed directory. It refuses to clone over an existing demo checkout.

## The integration

Keep the existing Jest settings and wrap them:

```js
// jest.wiretap.config.cjs
const { withWiretap } = require('@wiretap/sdk/jest');
module.exports = withWiretap({
  ...require('./package.json').jest,
  setupFilesAfterEnv: ['<rootDir>/wiretap/setup.cjs'],
});
```

Register the existing Express app, which is isolated per Jest test file:

```js
// wiretap/setup.cjs
const { captureJest } = require('@wiretap/sdk/jest');
captureJest({ app: require('../app') });
```

Change one shared client line in `utils/apiUtility.js`:

```js
const api = supertest(process.env.WIRETAP_SUT_URL || app);
```

The [project configuration](overlay/.wiretap.yaml) selects the
[OpenAPI contract](overlay/wiretap/openapi.yaml) and outputs. The runtime URL is
assigned automatically. No assertions or test files change. The SDK starts and
stops Wiretap; the setup helper hosts the app. With an already running API, set
`sut.url` in the project configuration and omit the setup helper.

To rerun the installed demo from its directory:

```bash
WIRETAP_BINARY=../wiretap node node_modules/jest/bin/jest.js --config jest.wiretap.config.cjs --runInBand
```

On Windows, set `WIRETAP_BINARY` to the absolute `wiretap.exe` path using your
shell's environment-variable syntax.

## What is verified

The unchanged upstream suite has **63 passes and 57 failures**: its mock API
omits validations and returns some error messages that disagree with its tests.
The captured run must preserve all 120 outcomes and the original exit code.
The verification script succeeds when that comparison and the artifact checks
pass; the raw Jest command still exits with failure. The API is intentionally
left unchanged so capture cannot hide these existing issues.

The checks also require a finalized archive containing 120 distinct test
identities, all **10 API operations exercised**, coverage JSON/text and a Postman
collection with no export errors. The official `postman-collection` Node library
parses the exported requests and verifies their methods, URLs, bodies and saved
response data. Desktop import remains a separate manual check.

Read `demo/artifacts/wiretap/last-run.json` for exact output paths. Upload the
complete `demo/artifacts/wiretap/` directory in CI, including failing runs. API
operation coverage is distinct from contract-condition coverage: the latter
honestly includes violations, gaps and unsupported non-JSON body conditions.

The demo's datastore mocks are in-process, so there is no downstream HTTP API
to capture. For HTTP mock-boundary coverage, use [Basic REST](../basic-rest/README.md);
for native gRPC mocks, use [Native Node gRPC](../grpc-boundaries/node/README.md).
