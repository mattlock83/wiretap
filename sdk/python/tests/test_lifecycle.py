"""Lifecycle conformance without pytest or a Wiretap executable."""

import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

from wiretap import ManagedRuntime, Runtime, WiretapError, trace_headers


CONTRACT = json.loads((Path(__file__).resolve().parents[2] / "conformance/scenarios.json").read_text())


class Responses:
    def __init__(self, directory):
        self.directory = str(directory)
        self.calls = []
        self.overrides = {}
        self.info = {"run_id": "run-1", "bundle_path": self.directory, "sut_url": "http://127.0.0.1:12345"}

    def __call__(self, path, payload=None, **options):
        self.calls.append((path, payload))
        if path in self.overrides:
            value = self.overrides[path]
            if isinstance(value, BaseException):
                raise value
            return value
        if path == "/v1/capabilities":
            return CONTRACT["capability_probe"]
        if path == "/v1/startRun":
            return dict(self.info)
        if path == "/v1/setupTest":
            return dict(self.info, session_id="session-%d" % len(self.calls), dependencies={"inventory": self.info["sut_url"] + "/__wiretap/dep/inventory"})
        if path == "/v1/teardownTest":
            return {"capture": {"testcase": {"status": payload["status"]}}}
        if path == "/v1/finishRun":
            return {"status": "complete", "run_id": "run-1", "bundle_path": self.directory,
                    "outputs": {"bundle_path": self.directory, "coverage": {}, "postman": [], "errors": []}}
        raise AssertionError(path)


class Child:
    pid = 777

    def __init__(self, code=0):
        self.stdin = io.BytesIO()
        self.stdout = io.BytesIO(b"runtime startup\n")
        self.code = code
        self.returncode = None
        self.killed = False

    def poll(self):
        return self.returncode

    def wait(self, timeout):
        self.returncode = self.code
        return self.code

    def kill(self):
        self.killed = True


class LifecycleTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="wiretap-python-unit-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.responses = Responses(self.root)

    def runtime(self):
        runtime = Runtime()
        runtime._request = mock.Mock(side_effect=self.responses)
        return runtime

    def manager(self, *, readiness=None, child=None, **options):
        child = child or Child()
        owner = ManagedRuntime(**options)
        owner._request = mock.Mock(side_effect=self.responses)

        def launch(args, **kwargs):
            path = Path(kwargs["env"]["WIRETAP_READY_FILE"])
            body = dict(pid=child.pid, control_url="http://127.0.0.1:12346")
            if readiness:
                body.update(readiness)
            path.write_text(json.dumps(body))
            self.assertEqual(kwargs["env"]["WIRETAP_EXIT_ON_STDIN_EOF"], "1")
            return child

        self.addCleanup(mock.patch.stopall)
        mock.patch("wiretap.managed.subprocess.Popen", side_effect=launch).start()
        return owner, child

    def test_attempt_context_identity_and_explicit_overrides(self):
        runtime = self.runtime().start()
        with runtime.test("lists orders", schemas=[], dependencies=[], schema_required=False) as attempt:
            self.assertEqual(trace_headers(), attempt.headers)
            self.assertTrue(attempt.headers["traceparent"].startswith("00-"))
            trace_headers().clear()
            self.assertTrue(trace_headers())
        self.assertEqual(trace_headers(), {})
        payload = next(payload for path, payload in self.responses.calls if path == "/v1/setupTest")
        self.assertEqual(payload["schemas"], [])
        self.assertEqual(payload["dependencies"], [])
        self.assertIs(payload["schema_required"], False)
        self.assertEqual(payload["test_identity"], {"id": "lists orders"})
        receipt = runtime.finish()
        self.assertIs(runtime.finish(), receipt)
        self.assertEqual(sum(path == "/v1/finishRun" for path, _ in self.responses.calls), 1)

    def test_omitted_lists_stay_omitted_and_traces_are_fresh(self):
        runtime = self.runtime()
        first = runtime.test("one")
        first.finish()
        second = runtime.test("two")
        second.finish("skip")
        payloads = [payload for path, payload in self.responses.calls if path == "/v1/setupTest"]
        self.assertNotIn("schemas", payloads[0])
        self.assertNotIn("dependencies", payloads[0])
        self.assertNotEqual(first.headers, second.headers)

    def test_active_attempt_blocks_finalization(self):
        runtime = self.runtime()
        attempt = runtime.test("active")
        with self.assertRaisesRegex(WiretapError, "Finish every"):
            runtime.finish()
        attempt.finish("fail")
        runtime.finish()

    def test_context_preserves_primary_and_secondary_cleanup(self):
        runtime = self.runtime()
        self.responses.overrides["/v1/teardownTest"] = WiretapError("drain failed")
        primary = AssertionError("original assertion")
        with self.assertRaises(AssertionError) as raised:
            with runtime.test("fails"):
                raise primary
        self.assertIs(raised.exception, primary)
        self.assertIn("drain failed", str(primary.wiretap_cleanup_errors[0]))
        self.assertEqual(trace_headers(), {})

    def test_incomplete_capture_fails_and_attempt_finishes_once(self):
        runtime = self.runtime()
        self.responses.overrides["/v1/teardownTest"] = {"capture": {"capture_incomplete": True}}
        attempt = runtime.test("incomplete")
        for _ in range(2):
            with self.assertRaisesRegex(WiretapError, "incomplete"):
                attempt.finish()
        self.assertEqual(sum(path == "/v1/teardownTest" for path, _ in self.responses.calls), 1)

    def test_invalid_receipts_follow_shared_vectors(self):
        for vector in CONTRACT["invalid_receipts"]["finish"]:
            with self.subTest(vector["id"]):
                runtime = self.runtime().start()
                receipt = self.responses("/v1/finishRun", {})
                receipt.update(vector["set"])
                self.responses.overrides["/v1/finishRun"] = receipt
                with self.assertRaisesRegex(WiretapError, "complete receipt"):
                    runtime.finish()
                self.responses.overrides.clear()

    def test_partial_export_retains_paths_and_failure(self):
        runtime = self.runtime().start()
        receipt = self.responses("/v1/finishRun", {})
        receipt["outputs"]["coverage"] = {"json": "/tmp/available-coverage.json"}
        receipt["outputs"]["errors"] = [{"format": "postman", "message": "unsupported"}]
        self.responses.overrides["/v1/finishRun"] = WiretapError("export failed", status=409, body=receipt)
        with self.assertRaises(WiretapError) as raised:
            runtime.finish()
        self.assertEqual(raised.exception.body["outputs"]["coverage"]["json"], "/tmp/available-coverage.json")
        self.assertIs(runtime.receipt, receipt)

    def test_capability_mismatch_and_legacy_runtime(self):
        self.responses.overrides["/v1/capabilities"] = {"api_version": "2.0", "capabilities": []}
        with self.assertRaisesRegex(WiretapError, "v1 lifecycle"):
            self.runtime().start()
        self.responses.overrides["/v1/capabilities"] = WiretapError("not found", status=404)
        runtime = self.runtime().start()
        self.assertIsNone(runtime.capabilities)

    def test_managed_receipt_and_log_belong_to_unique_bundle(self):
        owner, child = self.manager()
        owner.start()
        receipt = owner.close()
        self.assertIs(owner.close(), receipt)
        self.assertTrue(child.stdin.closed)
        self.assertEqual(json.loads((self.root / "last-run.json").read_text())["run_id"], "run-1")
        self.assertEqual((self.root / "wiretap.log").read_bytes(), b"runtime startup\n")
        self.assertFalse(Path(owner._directory).exists())

    def test_bad_readiness_stops_child_without_trusting_output(self):
        for vector in CONTRACT["invalid_receipts"]["readiness"]:
            with self.subTest(vector["id"]):
                owner, child = self.manager(readiness=vector["set"])
                with self.assertRaises(WiretapError):
                    owner.start()
                self.assertTrue(child.stdin.closed)
                self.assertFalse((self.root / "last-run.json").exists())
                self.assertFalse(any(path == "/v1/finishRun" for path, _ in self.responses.calls))

    def test_bad_start_identity_never_writes_receipt(self):
        vectors = CONTRACT["invalid_receipts"]["start"] + [{"id": "missing_archive", "set": {"bundle_path": str(self.root / "missing")}}]
        for vector in vectors:
            with self.subTest(vector["id"]):
                self.responses.overrides["/v1/startRun"] = dict(self.responses.info, **vector["set"])
                owner, child = self.manager()
                with self.assertRaises(WiretapError):
                    owner.start()
                self.assertTrue(child.stdin.closed)
                self.assertFalse((self.root / "last-run.json").exists())
                self.assertFalse(any(path == "/v1/finishRun" for path, _ in self.responses.calls))

    def test_nonzero_child_exit_and_secondary_errors_are_reported(self):
        owner, _ = self.manager(child=Child(code=3))
        owner.start()
        self.responses.overrides["/v1/finishRun"] = WiretapError("primary export error")
        with mock.patch.object(owner, "_write_evidence", side_effect=OSError("disk error")):
            with self.assertRaisesRegex(WiretapError, "primary export error") as raised:
                owner.close()
        self.assertEqual(len(raised.exception.wiretap_cleanup_errors), 2)
        self.assertIn("status 3", str(raised.exception.wiretap_cleanup_errors[0]))
        self.assertIn("disk error", str(raised.exception.wiretap_cleanup_errors[1]))

    def test_forced_kill_is_a_cleanup_failure(self):
        owner, child = self.manager()
        owner.start()
        child.wait = mock.Mock(side_effect=[subprocess.TimeoutExpired("wiretap", 5), -9])
        with self.assertRaisesRegex(WiretapError, "shutdown timeout"):
            owner.close()
        self.assertTrue(child.killed)

    def test_log_retention_is_bounded(self):
        owner = ManagedRuntime()
        owner.process = Child()
        owner.process.stdout = io.BytesIO(b"a" * 70000 + b"final log\n")
        owner._read_logs()
        self.assertEqual(len(owner._logs), 65536)
        self.assertTrue(owner._logs.endswith(b"final log\n"))

    def test_readiness_size_is_bounded(self):
        owner, child = self.manager(readiness={"extra": "x" * 65536})
        with self.assertRaisesRegex(WiretapError, "64 KiB"):
            owner.start()
        self.assertTrue(child.stdin.closed)
        self.assertFalse((self.root / "last-run.json").exists())

    def test_startup_deadline_stops_unready_child(self):
        owner, child = self.manager(startup_timeout=0.03)
        with mock.patch("wiretap.managed.json.loads", side_effect=ValueError("partial file")):
            with self.assertRaisesRegex(WiretapError, "did not become ready"):
                owner.start()
        self.assertTrue(child.stdin.closed)

    def test_control_response_is_json_object_and_bounded(self):
        runtime = Runtime()
        for body, message in [(b"not JSON", "invalid JSON"), (b"[]", "JSON object"),
                              (b"x" * (16 * 1024 * 1024 + 1), "16 MiB")]:
            with self.subTest(message):
                response = io.BytesIO(body)
                response.code = 200
                runtime._opener.open = mock.Mock(return_value=response)
                with self.assertRaisesRegex(WiretapError, message):
                    runtime._request("/v1/report", {})

    def test_duration_validation(self):
        for timeout in [0, -1, float("nan"), float("inf"), True, "30"]:
            with self.subTest(timeout):
                with self.assertRaises(ValueError):
                    Runtime(timeout=timeout)


if __name__ == "__main__":
    unittest.main()
