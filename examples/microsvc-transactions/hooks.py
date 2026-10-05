"""Project-owned fixtures and assertions, shared by unittest and exploration."""

import base64
import hashlib
import hmac
import json
import os
import secrets
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen

ACCOUNTS = os.environ.get("ACCOUNTS_URL", "http://accounts:8080")
TRANSACTIONS = os.environ.get("TRANSACTIONS_URL", "http://transactions:8080")
ACCOUNT_ID = 1001
INITIAL_BALANCE = 100
WITHDRAWAL = 25


def request(method, url, body=None, headers=None):
    data = None if body is None else json.dumps(body).encode()
    req = Request(url, data, {"Content-Type": "application/json", **(headers or {})}, method=method)
    try:
        response = urlopen(req, timeout=30)
    except HTTPError as error:
        response = error
    with response:
        raw = response.read()
        return response.status, json.loads(raw) if raw else None


def token(user_id):
    # This isolated demo trusts a fresh signing key supplied to its SUT container.
    # No user-service/database user is needed: Transactions validates JWT claims.
    def encode(value):
        return base64.urlsafe_b64encode(json.dumps(value).encode()).rstrip(b"=")
    now = int(time.time())
    message = encode({"alg": "HS256", "typ": "JWT"}) + b"." + encode({
        "sub": f"wiretap-{user_id}", "userId": user_id, "roles": "USER",
        "iat": now, "exp": now + 3600,
    })
    signature = hmac.new(os.environ["DEMO_JWT_SECRET"].encode(), message, hashlib.sha256).digest()
    return (message + b"." + base64.urlsafe_b64encode(signature).rstrip(b"=")).decode()


def auth_headers(user_id):
    return {"Authorization": "Bearer " + token(user_id)}


def verify_authentication():
    # Fail once, before generating cases, if credentials or SUT configuration
    # are wrong. Probe without credentials too: the demo must still enforce auth.
    url = TRANSACTIONS + "/api/transactions"
    status, _ = request("GET", url)
    if status != 401:
        raise RuntimeError(f"Expected unauthenticated request to return 401, got {status}")
    status, _ = request("GET", url, headers=auth_headers(secrets.randbelow(2**48) + 1))
    if status != 200:
        raise RuntimeError(f"Authentication probe returned {status}; check DEMO_JWT_SECRET matches the SUT JWT_SECRET")


def prepare():
    user_id = secrets.randbelow(2**48) + 1
    status, _ = request("POST", ACCOUNTS + "/__admin/reset", {"user_id": user_id})
    if status != 200:
        raise RuntimeError("Accounts fixture setup failed")
    return {"state": {"user_id": user_id}, "headers": auth_headers(user_id)}


def checks(user_id, success, expected_calls):
    status, transactions = request("GET", TRANSACTIONS + "/api/transactions",
                                   headers=auth_headers(user_id))
    mock_status, mock = request("GET", ACCOUNTS + "/__admin/state")
    if status != 200 or mock_status != 200:
        raise RuntimeError("Unable to inspect transaction/account fixtures")
    rows_ok = isinstance(transactions, list) and len(transactions) == (1 if success else 0)
    if success and rows_ok:
        row = transactions[0]
        rows_ok = (row["amount"] == WITHDRAWAL and row["fromAccountId"] == ACCOUNT_ID
                   and row["status"] == "COMPLETED" and row["type"] == "WITHDRAWAL")
    return [
        {"name": "expected transaction rows", "passed": rows_ok},
        {"name": "expected account balance", "passed": mock["balance"] == INITIAL_BALANCE - (WITHDRAWAL if success else 0)},
        {"name": "expected Accounts calls", "passed": mock["calls"] == expected_calls},
    ]


def cleanup():
    status, _ = request("DELETE", ACCOUNTS + "/__admin/reset")
    if status != 200:
        raise RuntimeError("Accounts fixture cleanup failed")


HAPPY_CALLS = ["GET /api/accounts/1001", "GET /api/accounts/1001/balance", "PUT /api/accounts/1001/balance"]
