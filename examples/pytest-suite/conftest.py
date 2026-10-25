"""Existing application fixtures stay in the test project."""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import threading

import pytest
from wiretap import ManagedRuntime


class ItemsHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        try:
            identifier = int(self.path.removeprefix("/items/"))
            if identifier < 1:
                raise ValueError("positive identifier required")
        except ValueError:
            status, body = 400, {"error": "positive identifier required"}
        else:
            status, body = 200, {"id": identifier, "name": "item-%d" % identifier}
        payload = json.dumps(body).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *_args):
        pass


@pytest.fixture(scope="session")
def api_server():
    server = ThreadingHTTPServer(("127.0.0.1", 0), ItemsHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    with pytest.MonkeyPatch.context() as environment:
        environment.setenv("PYTEST_SUT_URL", "http://127.0.0.1:%d" % server.server_port)
        try:
            yield server
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)


@pytest.fixture(scope="session")
def wiretap_runtime(api_server):
    # pytest-xdist gives each process its own application and Wiretap owner.
    # The plugin closes this owner after the final teardown outcome is known.
    return ManagedRuntime(config=".wiretap.yaml", cwd=str(Path(__file__).parent)).start()
