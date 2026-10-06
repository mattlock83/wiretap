"""An ordinary stateful HTTP mock; Wiretap only records calls to this server."""

import json
import math
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from fixtures import ACCOUNT_ID, INITIAL_BALANCE

lock = threading.Lock()
state = {"user_id": None, "balance": INITIAL_BALANCE, "calls": []}


class Accounts(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def reply(self, status, value=None):
        raw = b"" if value is None else json.dumps(value).encode()
        self.send_response(status)
        if raw:
            self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def read_json(self):
        # RestTemplate can send chunked bodies; BaseHTTPRequestHandler does not
        # decode that framing for us. Both ordinary HTTP body forms are supported.
        if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
            data = bytearray()
            while True:
                size = int(self.rfile.readline(128).split(b";", 1)[0], 16)
                if size == 0:
                    while self.rfile.readline(8192) not in (b"\r\n", b""):
                        pass
                    break
                if size < 0 or len(data) + size > 65536:
                    raise ValueError("body too large")
                data.extend(self.rfile.read(size))
                if self.rfile.read(2) != b"\r\n":
                    raise ValueError("invalid chunk")
        else:
            size = int(self.headers.get("Content-Length", 0))
            if size < 0 or size > 65536:
                raise ValueError("body too large")
            data = self.rfile.read(size)
        return json.loads(data)

    def handle_request(self):
        if self.path == "/healthz":
            return self.reply(200, {"status": "ok"})
        with lock:
            if self.path == "/__admin/state" and self.command == "GET":
                return self.reply(200, state)
            if self.path == "/__admin/reset" and self.command in ("POST", "DELETE"):
                user_id = None
                if self.command == "POST":
                    user_id = self.read_json()["user_id"]
                state.update(user_id=user_id, balance=INITIAL_BALANCE, calls=[])
                return self.reply(200, {"status": "ok"})
            state["calls"].append(f"{self.command} {self.path}")
            if not self.headers.get("Authorization", "").startswith("Bearer "):
                return self.reply(401, {"error": "missing bearer token"})
            account = f"/api/accounts/{ACCOUNT_ID}"
            if state["user_id"] is None or self.path not in (account, account + "/balance"):
                return self.reply(404, {"error": "account not found"})
            if self.command == "GET" and self.path == account:
                return self.reply(200, {"id": ACCOUNT_ID, "userId": state["user_id"],
                                       "accountNumber": "WIRETAP-1001", "accountType": "CHECKING", "balance": state["balance"]})
            if self.command == "GET" and self.path == account + "/balance":
                return self.reply(200, {"accountNumber": "WIRETAP-1001", "balance": state["balance"]})
            if self.command == "PUT" and self.path == account + "/balance":
                body = self.read_json()
                value = body.get("balance")
                if type(value) not in (float, int) or not math.isfinite(value):
                    return self.reply(400, {"error": "numeric balance required"})
                state["balance"] = value
                return self.reply(200)
            self.reply(405)

    do_GET = handle_request
    do_POST = handle_request
    do_PUT = handle_request
    do_DELETE = handle_request


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8080), Accounts).serve_forever()
