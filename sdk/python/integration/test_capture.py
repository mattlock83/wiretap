"""Actual HTTP capture and pytest subprocesses. Requires WIRETAP_BINARY."""

import json
import os
from pathlib import Path
import subprocess
import sys
import textwrap
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import Request, urlopen

import pytest

from wiretap import ManagedRuntime


pytestmark = pytest.mark.skipif(not os.environ.get("WIRETAP_BINARY"), reason="set WIRETAP_BINARY to a built runtime")
SCHEMA = """openapi: 3.0.3
info: {title: Python orders, version: '1'}
paths:
  /orders/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        '200':
          description: An order
          content:
            application/json:
              schema:
                type: object
                required: [id]
                properties:
                  id: {type: string}
"""


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps({"id": self.path.rsplit("/", 1)[-1]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


@pytest.fixture
def project(tmp_path):
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    (tmp_path / "orders.yaml").write_text(SCHEMA)
    (tmp_path / ".wiretap.yaml").write_text("""version: 1
application: {id: python-test}
output_dir: artifacts
outputs: {coverage: [json, text], postman: true}
sut:
  id: orders
  kind: service
  url: http://127.0.0.1:%d
  schema: orders.yaml
""" % server.server_port)
    yield tmp_path
    server.shutdown()
    server.server_close()
    thread.join(timeout=5)


def archives(project):
    return [path.parent for path in (project / "artifacts").rglob("bundle.json")]


def attempts(directory):
    bundle = json.loads((directory / "bundle.json").read_text())
    return [json.loads((directory / test["path"]).read_text())["capture"] for test in bundle["tests"]]


def run_pytest(project, source, *args):
    (project / "test_orders.py").write_text(textwrap.dedent(source))
    # Explicit plugin selection also checks source installs and keeps unrelated
    # globally installed plugins from changing these isolated acceptance runs.
    env = dict(os.environ, PYTEST_DISABLE_PLUGIN_AUTOLOAD="1")
    command = [sys.executable, "-m", "pytest", "-p", "wiretap.pytest_plugin", "-q", *args]
    return subprocess.run(command, cwd=str(project), env=env, capture_output=True, text=True, timeout=90)


def assert_exports(directory):
    receipt = json.loads((directory / "last-run.json").read_text())
    assert receipt["status"] == "complete"
    assert receipt["bundle_path"] == str(directory)
    assert receipt["outputs"]["errors"] == []
    report = json.loads(Path(receipt["outputs"]["coverage"]["json"]).read_text())
    assert report["report_version"] == "1.0"
    assert "orders" in Path(receipt["outputs"]["coverage"]["text"]).read_text()
    assert receipt["outputs"]["postman"]
    for name in receipt["outputs"]["postman"]:
        collection = json.loads(Path(name).read_text())
        assert collection["info"]["schema"].endswith("/v2.1.0/collection.json")
    assert (directory / "wiretap.log").stat().st_size <= 65536


def test_plain_lifecycle_captures_http_and_exports(project):
    with ManagedRuntime(cwd=str(project)) as capture:
        with capture.test("lists an order") as test:
            with urlopen(Request(test.url + "/orders/42", headers=test.headers)) as response:
                assert json.load(response) == {"id": "42"}
        directory = Path(capture.info["bundle_path"])
    recorded = attempts(directory)
    assert recorded[0]["testcase"]["status"] == "pass"
    assert len(recorded[0]["traffic"]) == 1
    assert recorded[0]["schema_validation"]["status"] == "pass"
    assert_exports(directory)


def test_pytest_records_final_outcomes_and_parametrized_identities(project):
    result = run_pytest(project, '''
        import pytest
        from urllib.request import Request, urlopen

        @pytest.fixture
        def fixture(wiretap, request):
            with urlopen(Request(wiretap.url + '/orders/setup', headers=wiretap.headers)) as response:
                assert response.status == 200
            yield
            with urlopen(Request(wiretap.url + '/orders/cleanup', headers=wiretap.headers)) as response:
                assert response.status == 200
            if request.node.name == 'test_cleanup_failure':
                raise AssertionError('fixture cleanup failed')

        @pytest.mark.parametrize('order_id', ['one', 'two'])
        def test_order(wiretap, fixture, order_id):
            with urlopen(Request(wiretap.url + '/orders/' + order_id, headers=wiretap.headers)) as response:
                assert response.status == 200

        def test_assertion_failure(wiretap):
            assert False, 'ordinary assertion'

        def test_cleanup_failure(fixture):
            pass

        @pytest.fixture
        def broken(wiretap):
            raise AssertionError('fixture setup failed')

        def test_setup_failure(broken):
            pass

        def test_skip(wiretap):
            pytest.skip('runtime skip')

        @pytest.mark.xfail(reason='expected assertion')
        def test_xfail(wiretap):
            assert False

        def test_unselected():
            pass
    ''')
    assert result.returncode == 1, result.stdout + result.stderr
    directories = archives(project)
    assert len(directories) == 1
    records = attempts(directories[0])
    assert len(records) == 7
    statuses = {record["testcase"]["name"].split("::")[-1]: record["testcase"]["status"] for record in records}
    assert statuses == {"test_order[one]": "pass", "test_order[two]": "pass", "test_assertion_failure": "fail",
                        "test_cleanup_failure": "fail", "test_setup_failure": "fail", "test_skip": "skip", "test_xfail": "fail"}
    variants = {record["testcase"]["identity"].get("variant") for record in records if "test_order[" in record["testcase"]["name"]}
    assert variants == {"one", "two"}
    assert all(record["testcase"]["identity"]["attempt_ordinal"] == 1 for record in records)
    assert all(len(record["traffic"]) == 3 for record in records if "test_order[" in record["testcase"]["name"])
    assert_exports(directories[0])


def test_global_opt_in_and_default_no_capture(project):
    source = "def test_ordinary():\n    assert True\n"
    result = run_pytest(project, source)
    assert result.returncode == 0, result.stdout + result.stderr
    assert not archives(project)
    result = run_pytest(project, source, "--wiretap")
    assert result.returncode == 0, result.stdout + result.stderr
    assert len(attempts(archives(project)[0])) == 1


def test_xdist_workers_own_distinct_runs(project):
    pytest.importorskip("xdist")
    (project / "test_orders.py").write_text("""import pytest
from urllib.request import Request, urlopen
@pytest.mark.parametrize('order', list(range(6)))
def test_order(order, wiretap):
    with urlopen(Request(wiretap.url + '/orders/' + str(order), headers=wiretap.headers)) as response:
        assert response.status == 200
""")
    env = dict(os.environ, PYTEST_DISABLE_PLUGIN_AUTOLOAD="1")
    result = subprocess.run([sys.executable, "-m", "pytest", "-p", "wiretap.pytest_plugin", "-p", "xdist.plugin", "-n", "2", "-q"],
                            cwd=str(project), env=env, capture_output=True, text=True, timeout=90)
    assert result.returncode == 0, result.stdout + result.stderr
    directories = archives(project)
    assert len(directories) == 2
    assert sum(len(attempts(directory)) for directory in directories) == 6
    assert len({json.loads((directory / "last-run.json").read_text())["run_id"] for directory in directories}) == 2
    for directory in directories:
        assert_exports(directory)


def test_owner_death_stops_runtime_and_preserves_interrupted_archive(project):
    marker = project / "owner.json"
    script = project / "owner.py"
    script.write_text("""import json, pathlib, time
from wiretap import ManagedRuntime
owner = ManagedRuntime().start()
attempt = owner.test('interrupted')
pathlib.Path('owner.json').write_text(json.dumps({'pid': owner.process.pid, 'bundle': owner.info['bundle_path'], 'base_url': owner.base_url}))
while True:
    time.sleep(1)
""")
    parent = subprocess.Popen([sys.executable, str(script)], cwd=str(project), stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    try:
        deadline = time.monotonic() + 15
        while not marker.exists() and time.monotonic() < deadline:
            assert parent.poll() is None, parent.stderr.read().decode()
            time.sleep(0.025)
        assert marker.exists(), "parent did not become ready"
        info = json.loads(marker.read_text())
        parent.kill()
        parent.wait(timeout=10)
        deadline = time.monotonic() + 10
        bundle = Path(info["bundle"]) / "bundle.json"
        while time.monotonic() < deadline:
            state = json.loads(bundle.read_text())["run_state"]
            if state == "interrupted":
                break
            time.sleep(0.025)
        assert state == "interrupted"
        assert not (bundle.parent / "last-run.json").exists()
        with pytest.raises(OSError):
            urlopen(info["base_url"] + "/healthz", timeout=0.5)
    finally:
        if parent.poll() is None:
            parent.kill()
            parent.wait(timeout=10)
        parent.stderr.close()
