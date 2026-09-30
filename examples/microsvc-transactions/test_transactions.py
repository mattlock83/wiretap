"""Existing-style unittest lifecycle + ordinary HTTP clients, using Wiretap's API."""

from contextlib import contextmanager
import os
from pathlib import Path
import secrets
import unittest

from hooks import ACCOUNT_ID, HAPPY_CALLS, WITHDRAWAL, checks, cleanup, prepare, request

CONTROL = os.environ["WIRETAP_BASE_URL"]
PROXY = os.environ["WIRETAP_SUT_URL"]
HAPPY_TEST = "withdrawal updates the account balance"


def control(path, body):
    status, response = request("POST", CONTROL + "/v1/" + path, body)
    if status != 200:
        raise RuntimeError(f"Wiretap {path} failed: {status}: {response}")
    return response


class Transactions(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        capture = control("startRun", {})
        cls.addClassCleanup(control, "finishRun", {})
        Path(os.environ["CAPTURE_PATH_FILE"]).write_text(capture["bundle_path"])

    @contextmanager
    def capture(self, name):
        fixture = prepare()
        self.addCleanup(cleanup)
        traceparent = "00-" + secrets.token_hex(16) + "-" + secrets.token_hex(8) + "-01"
        session = control("setupTest", {
            "name": name, "trace_context": {"traceparent": traceparent},
            "redactions": [{"field": "request.headers.Authorization", "rule": "remove"}],
            "meta": {"example": "speedscale/microsvc", "image": os.environ["TRANSACTIONS_IMAGE"],
                     "image_id": os.environ["TRANSACTIONS_IMAGE_ID"]},
        })
        self.headers = {**fixture["headers"], "traceparent": traceparent}
        outcome = "fail"
        try:
            yield fixture["state"]["user_id"]
            outcome = "pass"
        finally:
            control("teardownTest", {"session_id": session["session_id"], "status": outcome})

    def get_rows(self):
        status, rows = request("GET", PROXY + "/api/transactions", headers=self.headers)
        self.assertEqual(status, 200)
        return rows

    def test_withdrawal(self):
        with self.capture(HAPPY_TEST) as user_id:
            self.assertEqual(self.get_rows(), [])
            status, row = request("POST", PROXY + "/api/transactions/withdraw",
                                  {"accountId": ACCOUNT_ID, "amount": WITHDRAWAL}, self.headers)
            self.assertEqual(status, 201, row)
            self.assertEqual(row["amount"], WITHDRAWAL)
            self.assertEqual(row["status"], "COMPLETED")
            self.assertEqual([r["id"] for r in self.get_rows()], [row["id"]])
            for check in checks(user_id, True, HAPPY_CALLS):
                self.assertTrue(check["passed"], check["name"])

    def test_insufficient_funds(self):
        with self.capture("insufficient funds leave the account unchanged") as user_id:
            status, body = request("POST", PROXY + "/api/transactions/withdraw",
                                   {"accountId": ACCOUNT_ID, "amount": 150}, self.headers)
            self.assertEqual(status, 400, body)
            self.assertEqual(self.get_rows(), [])
            for check in checks(user_id, False, HAPPY_CALLS[:2]):
                self.assertTrue(check["passed"], check["name"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
