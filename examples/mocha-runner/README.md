# Mocha API suite

Wrap the existing Mocha configuration and point the shared API client at
`process.env.WIRETAP_SUT_URL`. This example keeps ordinary `describe`, `it`,
`beforeEach`, `afterEach`, assertions and native `fetch` requests. The adapter
hosts the exported HTTP handler, captures each test and writes the configured
coverage and Postman exports.

From the repository root with Go 1.26+ and Node.js 22.12+:

```sh
npm ci --prefix sdk/js
go build -o dist/wiretap ./cmd/wiretap
WIRETAP_BINARY="$PWD/dist/wiretap" node sdk/js/node_modules/mocha/bin/mocha.js --config examples/mocha-runner/.mocharc.cjs
```

On PowerShell:

```powershell
npm ci --prefix sdk/js
go build -o dist/wiretap.exe ./cmd/wiretap
$env:WIRETAP_BINARY = (Resolve-Path .\dist\wiretap.exe).Path
node sdk/js/node_modules/mocha/bin/mocha.js --config examples/mocha-runner/.mocharc.cjs
```

Both tests pass. The invalid request remains a schema mismatch in the artifact
while its rejection assertion passes. Setup and cleanup requests belong to
their respective test attempts. Find exact paths in
`examples/mocha-runner/artifacts/last-run.json`; preserve that entire artifacts
directory in CI, including failures.

To check failure handling, set `WIRETAP_EXAMPLE_FAIL=1` before the command. A user
cleanup hook fails, Mocha returns nonzero, and Wiretap still finalizes the failed
attempt and exports. Mocha can stop the affected suite after a hook failure.

For another project, install the [packed SDK](../../docs/local-release.md), use
`require('@wiretap/sdk/mocha')` in the wrapper, and retain the project's existing
Mocha command. Paths in adapter options are relative to `cwd`, which this example
sets to its own directory. Mocha 11.7.5+ within 11.x and Mocha 12 are supported;
the adapter runs serially.
See the [Mocha integration guide](../../sdk/js/docs/mocha.md).
