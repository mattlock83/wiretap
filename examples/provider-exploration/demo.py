"""Explore inside an ordinary test using local SQLite fixture callbacks.

Requires Python 3 and Go. No third-party Python packages or Wiretap SDK needed.
Run from the repository: python3 examples/provider-exploration/demo.py
"""

import argparse
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import URLError
from urllib.request import Request, urlopen
from uuid import uuid4


HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent
sys.path.insert(0, str(HERE.parent))
from explore_http import explore


def database():
    return sqlite3.connect(os.environ["WIRETAP_DEMO_DATABASE"])


def prepare():
    user, token = str(uuid4()), str(uuid4())
    with database() as db:
        db.execute("INSERT INTO users VALUES (?, ?)", (user, token))
    return {"bindings": {"user_id": user}, "headers": {"Authorization": "Bearer " + token}}


def cleanup(user):
    with database() as db:
        db.execute("DELETE FROM orders WHERE user_id = ?", (user,))
        db.execute("DELETE FROM users WHERE id = ?", (user,))


def check_case(event):
    with database() as db:
        rows = db.execute("SELECT count(*) FROM orders WHERE user_id = ?",
                          (event["bindings"]["user_id"],)).fetchone()[0]
    expected = 1 if event["mode"] == "baseline" else 0
    return {"checks": [{"name": "expected database effect", "passed": rows == expected}]}


class Provider(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def reply(self, status, value):
        raw = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_POST(self):
        if self.path != "/orders":
            return self.reply(404, {"error": "unknown route"})
        token = self.headers.get("Authorization", "").removeprefix("Bearer ")
        with database() as db:
            user = db.execute("SELECT id FROM users WHERE token = ?", (token,)).fetchone()
        if not user:
            return self.reply(401, {"error": "unauthorized"})
        try:
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
        except (ValueError, TypeError):
            return self.reply(400, {"error": "JSON body required"})
        valid = (isinstance(body, dict)
                 and isinstance(body.get("user_id"), str) and bool(body["user_id"])
                 and type(body.get("quantity")) is int and 1 <= body["quantity"] <= 3)
        if not valid:
            return self.reply(400, {"error": "invalid order"})
        if body["user_id"] != user[0]:
            return self.reply(401, {"error": "wrong user"})
        order = str(uuid4())
        with database() as db:
            db.execute("INSERT INTO orders VALUES (?, ?)", (order, user[0]))
        self.reply(201, {"id": order})


def post(url, data, headers=None):
    req = Request(url, json.dumps(data).encode(),
                  {"Content-Type": "application/json", **(headers or {})})
    with urlopen(req, timeout=15) as response:
        return json.load(response)


def run(output):
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="wiretap-explore-demo-") as work:
        work = Path(work)
        os.environ["WIRETAP_DEMO_DATABASE"] = str(work / "fixtures.sqlite")
        with database() as db:
            db.execute("CREATE TABLE users (id TEXT PRIMARY KEY, token TEXT)")
            db.execute("CREATE TABLE orders (id TEXT PRIMARY KEY, user_id TEXT)")
        sut = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
        threading.Thread(target=sut.serve_forever, daemon=True).start()
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            control_port = probe.getsockname()[1]
        target = f"http://127.0.0.1:{sut.server_port}"
        control = f"http://127.0.0.1:{control_port}"
        # JSON is also valid YAML. Configuration declares boundaries, not hook code.
        config = {
            "version": 1, "http_listen": f"127.0.0.1:{control_port}",
            "proxy_listen": "127.0.0.1:0", "output_dir": str(output),
            "sut": {"id": "orders", "url": target, "kind": "service",
                    "schema": str(HERE / "openapi.yaml")},
        }
        config_path = work / ".wiretap.yaml"
        config_path.write_text(json.dumps(config))
        binary = work / ("wiretap.exe" if os.name == "nt" else "wiretap")
        subprocess.run(["go", "build", "-o", str(binary), "./cmd/wiretap"], cwd=ROOT, check=True)
        environment = {k: v for k, v in os.environ.items()
                       if k not in {"WIRETAP_HTTP_LISTEN", "WIRETAP_PROXY_LISTEN", "WIRETAP_OUTPUT_DIR"}}
        runtime = subprocess.Popen([str(binary), "--config", str(config_path)],
                                   stdout=subprocess.DEVNULL, stderr=sys.stderr, env=environment)
        try:
            for _ in range(100):
                try:
                    with urlopen(control + "/healthz", timeout=.2):
                        break
                except (URLError, TimeoutError):
                    time.sleep(.1)
            else:
                raise RuntimeError("Wiretap did not start")
            api = lambda path, body: post(control + "/v1/" + path, body)
            capture = api("startRun", {})
            session = post(control + "/v1/setupTest", {
                "name": "creates an order",
                "redactions": [{"field": "request.headers.Authorization", "rule": "remove"}],
            })
            fixture = prepare()
            outcome = "fail"
            try:
                response = post(session["sut_url"] + "/orders",
                                {"user_id": fixture["bindings"]["user_id"], "quantity": 2},
                                fixture["headers"])
                assert response["id"]
                cleanup(fixture["bindings"]["user_id"])
                generated = explore(api, session["session_id"],
                    operations=["POST /orders"], expected_statuses=[400],
                    bindings=[{"name": "user_id", "in": "body", "path": "/user_id"}],
                    hooks={
                        "before_case": lambda event: prepare(),
                        "before_request": {
                            "operations": ["POST /orders"],
                            "handler": lambda event: {"headers": event["headers"]},
                        },
                        "check_case": check_case,
                        "after_case": lambda event: cleanup(event.get("bindings", {}).get("user_id", "")),
                    })
                assert generated["executed"] > 0
                assert generated["failed"] == 0
                outcome = "pass"
            finally:
                cleanup(fixture["bindings"]["user_id"])
                post(control + "/v1/teardownTest", {"session_id": session["session_id"], "status": outcome})
                post(control + "/v1/finishRun", {})
            print("Authored and generated checks share one bundle:", capture["bundle_path"], flush=True)
            with database() as db:
                assert db.execute("SELECT count(*) FROM users").fetchone()[0] == 0
                assert db.execute("SELECT count(*) FROM orders").fetchone()[0] == 0
            print("All generated rejection checks passed; database fixtures cleaned up.")
        finally:
            runtime.terminate()
            try:
                runtime.wait(timeout=5)
            except subprocess.TimeoutExpired:
                runtime.kill()
                runtime.wait()
            sut.shutdown()
            sut.server_close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=ROOT / "artifacts" / "provider-exploration")
    args = parser.parse_args()
    run(args.output.resolve())
