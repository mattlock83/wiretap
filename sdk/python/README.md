# Python SDK and pytest

Use your existing Python HTTP/gRPC clients, fixtures and assertions. The core
SDK uses only the Python 3.9+ standard library. The optional pytest plugin starts
Wiretap, records setup/test/teardown outcomes, exports coverage and Postman
collections, and stops the owned runtime. It downloads no executable.

From the Wiretap checkout, install locally into your project's virtual environment:

```sh
python -m pip install './sdk/python[pytest]'
```

Put a built `wiretap` on `PATH`, or set `WIRETAP_BINARY` to its absolute path.
The [runnable example](../../examples/pytest-suite/README.md) includes a real HTTP
service, an OpenAPI contract and ordinary parametrized pytest tests.
See the [support matrix](../../docs/support.md) for runtime and runner versions.

## pytest

Put `.wiretap.yaml` at the pytest project root:

```yaml
version: 1
application: {id: orders}
output_dir: artifacts
outputs: {coverage: [json, text], postman: true}
sut:
  id: orders-http
  url: http://127.0.0.1:8080
  kind: service
  schema: orders.openapi.yaml
```

Add the `wiretap` fixture to tests or a shared fixture. It starts capture through
pytest's native fixture lifecycle and finishes after fixture teardown has
reported its outcome. Fixtures that make API calls should depend on `wiretap`
to guarantee capture is ready before their setup. Route your existing client
through `wiretap.url` and add its headers:

```python
from urllib.request import Request, urlopen

def test_orders(wiretap):
    request = Request(wiretap.url + '/orders', headers=wiretap.headers)
    with urlopen(request) as response:
        assert response.status == 200
```

For Requests, keep the existing session and use
`client.get(wiretap.url + '/orders', headers=wiretap.headers)`.
`wiretap.dependencies['inventory']` provides a declared dependency's capture
address. Your application fixture must use that address for its outbound calls.
Declaring the original mock URL alone does not redirect application traffic.

Run `pytest` normally. `pytest --wiretap` captures every selected test, including
tests using the shared `wiretap` fixture indirectly. Without that flag, only
tests requesting the fixture are captured. A plain pytest run with no Wiretap
fixtures has no capture side effects.

Arguments include `--wiretap-config`, `--wiretap-binary`, `--wiretap-output-dir`,
`--wiretap-timeout` (seconds, default 30) and `--wiretap-drain-timeout` (0–60,
default 5). Paths resolve from pytest's project root. Runtime environment
overrides, including `WIRETAP_OUTPUT_DIR`, remain available.

Test identity is pytest's node ID, including parameter IDs. Very long identities
use a stable SHA-256 representation. Repeated executions receive separate
attempt ordinals. Failures in setup, assertions or fixture cleanup are recorded
as failures; skipped execution is recorded as skipped. Expected assertion
failures remain failed executions in evidence while pytest keeps its xfail
result. Explicit `pytest.xfail()` is a skipped execution. Collection errors and
deselected tests create no attempts. Contract validation stays separate from
pytest's result, so an intentional invalid request can pass its test assertions.
Static skip marks evaluated before fixture setup also create no attempt.

For an application started by an existing fixture, override the session fixture
to return a started owner:

```python
import pytest
from wiretap import ManagedRuntime

@pytest.fixture(scope='session')
def wiretap_runtime(api_server):
    return ManagedRuntime(config='.wiretap.yaml').start()
```

Have `api_server` set the target environment variable used in your YAML before
the runtime starts. The plugin closes the owner after the final teardown report;
do not close it from a fixture finalizer. Suite-scoped application setup is
outside individual attempts. Function fixtures can depend on `wiretap` to make
their setup/cleanup requests through the current capture session.

Each pytest process owns an independent runtime and archive. With pytest-xdist,
each worker therefore owns its run; there is no merged run. Give each worker its
own application/mock fixtures when dependency URLs or mutable fixtures differ.
Sharing an application requires correct W3C propagation and capture routing;
headers do not automatically instrument its outbound clients. Do not share an
untraced application that routes dependencies through another worker's runtime.
Threaded/async work must finish before test teardown. The SDK never monkeypatches
HTTP clients or installs a tracing provider.

## A custom harness

```python
from wiretap import ManagedRuntime

with ManagedRuntime(config='.wiretap.yaml') as capture:
    with capture.test('lists orders') as test:
        # Use test.url, test.dependencies and test.headers with existing clients.
        pass
    # A custom runner can instead call attempt.finish('pass'/'fail'/'skip')
    # after it knows the final outcome, including fixture cleanup.
```

`trace_headers()` copies the current attempt's headers inside its context; pass
them explicitly to existing HTTP clients or gRPC metadata. Context variables
follow ordinary Python async contexts; newly created threads need explicit
headers or `contextvars.copy_context()`.

`Runtime(base_url='http://127.0.0.1:18000').start()` connects to a runtime owned by
your harness. Its `test()`, `report()` and `finish()` use the same lifecycle.
Use the runtime API's snake_case keyword fields for per-test overrides, such as
`sut_base_url`, `schemas`, `dependencies` and `explicit_session`. Omitted lists
inherit project settings; empty lists clear them. Schema paths belong to the
runtime's filesystem. One suite owner calls `finish()` after every attempt.
An external runtime's `close()` finalizes capture and leaves that process alive.

Managed startup validates readiness against its child PID and local control
origin. The runtime advertises its API and capabilities at `/v1/capabilities`;
older v1 runtimes without that endpoint remain supported. Control requests have
bounded deadlines; increase `timeout` for slow or large gRPC reflection
inventories. Teardown allows its requested drain plus cleanup time.

## Keep the evidence

Each unique run directory contains `last-run.json`, a bounded `wiretap.log`, raw
traffic, frozen contracts and configured exports. This avoids receipts from
concurrent workers overwriting one another. Preserve the complete output root
in CI on success and failure. Receipt identities and completion are checked;
export errors retain available paths on `WiretapError.body` and still fail
cleanup. An original test exception is preserved if cleanup also fails, with
the secondary error on its `wiretap_cleanup_error` attribute.

Managed runtimes hold a stdin lifeline. If the Python owner dies, EOF asks
Wiretap to stop and retain an interrupted raw archive. Hard interruption cannot
write the normal completion receipt or complete exports. Normal shutdown is
bounded and force-kills a child that will not exit; a nonzero child exit also
fails cleanup. Use synthetic data and dummy secrets: captured traffic is retained
unchanged.

The configured Postman exports include captured mock boundaries. Keep their
raw archives and frozen contracts together, then use the normal offline CLI:

```sh
wiretap check consumer --consumer /path/to/this-run --artifacts /path/to/api-evidence
```

Declare `provider_mappings` in `.wiretap.yaml` and retain a selected provider's
complete passing evidence. A matching mock response alone does not establish
provider agreement. See [assumption checks](../../docs/assumption-checks.md).

## Validate locally

From the repository root:

```sh
python -m pip install -e './sdk/python[test]'
PYTHONPATH=sdk/python/src python -m unittest discover -s sdk/python/tests -p 'test_*.py'
go build -o dist/wiretap ./cmd/wiretap
WIRETAP_BINARY="$PWD/dist/wiretap" python -m pytest -q sdk/python/integration
WIRETAP_BINARY="$PWD/dist/wiretap" python -m pytest -q examples/pytest-suite
```

The integration tests run actual pytest subprocesses, including deliberate
failures, fixture failures, skips, parametrization, xdist and owner death. No
package is published.
