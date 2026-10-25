# Existing Python tests with pytest

This suite keeps an ordinary stdlib HTTP server, `urllib` client and pytest
assertions. Wiretap adds the capture fixture, explicit client routing and final
test outcomes. No application or HTTP client instrumentation is installed.

From the repository root, using Python 3.9+ and Go 1.26+:

```sh
go build -o dist/wiretap ./cmd/wiretap
python -m venv .venv
. .venv/bin/activate
python -m pip install './sdk/python[test]'
WIRETAP_BINARY="$PWD/dist/wiretap" python -m pytest examples/pytest-suite -q
```

On PowerShell, activate `.venv\Scripts\Activate.ps1`, build
`dist/wiretap.exe`, set `$env:WIRETAP_BINARY` to its absolute path and run the
same `python -m pytest` command.

The application fixture supplies `PYTEST_SUT_URL` before the custom
`wiretap_runtime` fixture starts the runtime. The plugin owns finalization and
shutdown; the fixture does not close the owner itself. Test and fixture requests
use `wiretap.url` and `wiretap.headers`. Cleanup requests happen before the final
attempt outcome is reported. An intentionally invalid request is recorded as
validation evidence while its successful rejection assertion still passes.

For isolated parallel workers:

```sh
WIRETAP_BINARY="$PWD/dist/wiretap" python -m pytest examples/pytest-suite -q -n 2
```

Each worker starts its own application and runtime. Each completed archive under
`examples/pytest-suite/artifacts/<run-id>/` contains `last-run.json`, bounded
`wiretap.log`, raw evidence, coverage and executable Postman collections. Use
the exact printed archive path. Independent archives may be grouped using an
explicit [run set](../../docs/run-sets.md); the shared example build ID only
identifies this unchanged demo fixture. Real projects should record their tested
application revision or CI build ID.

The [Python SDK guide](../../sdk/python/README.md) covers custom harnesses,
dependencies, skips/errors, configuration and trace propagation.
