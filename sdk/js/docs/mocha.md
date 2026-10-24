# Mocha

Use Mocha 11 (11.7.5+) or 12 with Node.js 22.12+. First
[install the local SDK and runtime](../README.md#install-and-configure-once).
Keep your existing `describe`,
`it`, assertions, hooks and reporter. Wiretap manages its runtime and captures
each executed test, including setup and cleanup requests.

Wrap your existing `.mocharc.cjs` configuration:

```js
const { withWiretap } = require('@wiretap/sdk/mocha');

module.exports = withWiretap({
  spec: ['test/**/*.test.js'],
}, {
  app: './app.js',
});
```

`app.js` exports an Express app or another Node HTTP request handler. Wiretap
loads it, listens on an available local port and closes it after your suite and
global teardown fixtures. ESM default exports also work. Paths are relative to
the test command's working directory; set `cwd` if your project uses a different
directory. An already imported request handler is also accepted.

Change the shared API client's target once:

```js
const request = require('supertest');
const api = request(process.env.WIRETAP_SUT_URL);
```

The proxy URL is ready before Mocha loads test files. Continue using the same
`mocha` command. Wiretap reads `.wiretap.yaml` for the API contract and outputs;
the executable comes from `WIRETAP_BINARY` or `wiretap` on your PATH. You can
also set `binary`, `config` and `outputDir` in the wrapper options.

The main HTTP API requires OpenAPI by default. HTTP dependencies and mocks need
no spec or opt-out flag; add a dependency schema to enable validation and
contract coverage. See [HTTP dependency schema options](javascript.md#http-dependency-schema-options)
for strict `schemaRequired: true` overrides and provider-check assurance.

If your existing suite already starts its server, omit `app` and configure its
address in `.wiretap.yaml`. You can add capture without a configuration wrapper:

```sh
mocha --require @wiretap/sdk/mocha
```

This uses the same default project configuration and managed runtime. Existing
root-hook plugins remain supported; with the wrapper their order is preserved.
Wiretap starts capture before per-test hooks and finalizes after their cleanup.

## Outcomes and exports

Failed assertions, timeouts and failing `beforeEach` or `afterEach` hooks mark
the attempt failed. Mocha's retries create separate attempts with the same test
identity. Runtime `this.skip()` is recorded as skipped; statically skipped or
filtered tests do not create attempts. An empty filtered run still finalizes
the configured API contracts and exports.

The runner keeps its own pass/fail result. A negative API test can pass while
its observed response violates the API contract; both results remain in the
artifact. Capture and export errors fail the command, and cleanup still stops
the owned server and Wiretap runtime. On Linux and macOS, SIGINT and SIGTERM
finalize active capture and use the normal interrupted exit codes. Forced
process termination cannot run asynchronous cleanup.

The managed adapter writes the latest `last-run.json` and `wiretap.log` beside
the unique run archive directories in the output directory. The receipt gives
the exact archive, coverage report and Postman export paths. Keep the complete
output directory in CI and use a different `outputDir` for each independent
concurrent run. User
`after` hooks and global teardown fixtures complete before Wiretap shuts down
the owned server and finalizes the run, including when a fixture fails.

## Advanced capture

Normal usage inherits API settings from `.wiretap.yaml`. `capture` accepts the
shared SDK capture options when a suite needs extra metadata or fixture setup:

```js
module.exports = withWiretap(existingConfig, {
  app: './app.js',
  capture: {
    testDefaults: {
      tags: ['integration'],
      onSession(session) {
        // Configure a client that needs the test's explicit trace context.
      },
    },
  },
});
```

The adapter runs one Mocha process serially. Parallel and watch modes are
rejected, including command-line overrides, so unrelated requests cannot be
silently assigned to the wrong test. Use the lower-level JavaScript SDK when
you need a custom process arrangement. Keep Mocha global teardown enabled.

Suite-wide `before` and `after` requests are outside individual test attempts.
Put API setup and cleanup that belong to a test in `beforeEach` and `afterEach`.
Use synthetic data and dummy secrets: Wiretap retains captured traffic.
