# Contributing

For vulnerability reports, follow the [security contact instructions](SECURITY.md).

Use Go 1.26.10, Node 22 or 24, and Python 3.9+ for the Python SDK. Keep the existing
test framework and clients when adding integration examples; Wiretap captures
traffic and test outcomes.

Before submitting a change, run the checks appropriate to its scope:

```sh
go test ./...
npm ci --prefix sdk/js
npm test --prefix sdk/js
node scripts/package-smoke.mjs
node scripts/runner-smoke.mjs
node scripts/go-sdk-smoke.mjs
```

Changes to capture, correlation or session lifecycle also require
`go test -race ./internal/proxy ./internal/app ./sdk/go`. Postman compatibility checks live
under `tests/postman-compat`. The CI matrix runs Go and SDK/package checks on
Linux, macOS and Windows with Node 22 and 24. See the
[support matrix](docs/support.md) for configured Python versions.

For Python SDK changes, install the local test extra in a virtual environment
and use the locally built runtime:

```sh
python -m pip install "./sdk/python[test]"
go build -o ./dist/wiretap ./cmd/wiretap
export WIRETAP_BINARY="$PWD/dist/wiretap"
python -m unittest discover -s sdk/python/tests -v
python -m pytest -q sdk/python/integration
python -m pytest -q examples/pytest-suite
python -m pytest -q -n 2 examples/pytest-suite
```

On Windows, build `dist/wiretap.exe` and set `WIRETAP_BINARY` to its absolute path
using the shell's environment-variable syntax.

Lead onboarding examples with the managed Jest, Playwright Test and Mocha
adapters, Python's `wiretap` fixture or Go's `Run`/`StartTest` hooks. Keep
external-runtime examples clearly labelled and assign one suite owner to
finalization. For custom JavaScript
harnesses, use `startManagedRuntime` instead of duplicating process startup.

Document changes to exported formats, coverage rules or supported integrations.
Documentation and website copy describe implemented behavior and runnable
examples. Keep proposed features and deferred work only in [the roadmap](docs/roadmap.md).
Add regression tests for behavior changes and preserve incomplete/unknown
evidence explicitly. Do not include real credentials or sensitive recordings in
fixtures. Local release preparation is described in
[the release guide](docs/local-release.md); package and image publication are
separate actions.

Contributions are provided under the repository's [MIT license](LICENSE).
