"""Standard-library control client and explicit per-test trace context."""

import contextvars
import json
import math
import os
import secrets
import urllib.error
import urllib.parse
import urllib.request


_current = contextvars.ContextVar("wiretap_attempt", default=None)
_MAX_RESPONSE = 16 * 1024 * 1024


class WiretapError(RuntimeError):
    """A control, capture or cleanup failure, retaining any partial receipt."""

    def __init__(self, message, *, status=None, body=None):
        super().__init__(message)
        self.status = status
        self.body = body


def _secondary(primary, secondary):
    """Keep every cleanup failure without replacing the original exception."""
    errors = getattr(primary, "wiretap_cleanup_errors", None)
    if errors is None:
        errors = primary.wiretap_cleanup_errors = []
    errors.append(secondary)
    primary.wiretap_cleanup_error = secondary
    if hasattr(primary, "add_note"):
        primary.add_note("Wiretap cleanup also failed: %s" % secondary)
    return primary


def _duration(value, name):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value <= 0:
        raise ValueError("%s must be a positive finite number of seconds" % name)
    return value


def _url(value, *, local=False):
    if not isinstance(value, str):
        raise WiretapError("Wiretap returned an invalid URL")
    try:
        url = urllib.parse.urlsplit(value)
        port = url.port
    except ValueError as error:
        raise WiretapError("Wiretap returned an invalid URL") from error
    if (url.scheme not in ("http", "https") or not url.hostname or url.username is not None
            or url.password is not None or url.query or url.fragment or url.path not in ("", "/")):
        raise WiretapError("Wiretap URL must be an HTTP(S) origin without credentials, path, query or fragment")
    if local and (url.hostname not in ("127.0.0.1", "::1", "localhost") or not port):
        raise WiretapError("Wiretap readiness must identify a loopback origin with an explicit port")
    return value.rstrip("/")


def _same_path(left, right):
    return isinstance(left, str) and isinstance(right, str) and os.path.normcase(os.path.abspath(left)) == os.path.normcase(os.path.abspath(right))


def trace_headers():
    """Copy the current attempt's W3C headers; no global client is modified."""
    attempt = _current.get()
    return dict(attempt.headers) if attempt else {}


class Runtime:
    """Connect to a harness-owned runtime; start and finish one capture run.

    Options use the HTTP API's snake_case fields. Omitted lists inherit project
    defaults; explicitly empty lists clear them. Control calls bypass proxy
    environment variables so control traffic is never captured accidentally.
    """

    def __init__(self, base_url=None, *, timeout=30):
        self.base_url = _url(base_url or os.environ.get("WIRETAP_BASE_URL", "http://127.0.0.1:18000"))
        self.timeout = _duration(timeout, "timeout")
        self.info = None
        self.capabilities = None
        self.receipt = None
        self._active = set()
        self._closed = False
        self._close_error = None
        self._opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def _request(self, path, payload=None, *, timeout=None):
        data = None if payload is None else json.dumps(payload, allow_nan=False).encode("utf-8")
        request = urllib.request.Request(self.base_url + path, data=data,
                                         headers={"Content-Type": "application/json"},
                                         method="GET" if payload is None else "POST")
        try:
            response = self._opener.open(request, timeout=timeout or self.timeout)
        except urllib.error.HTTPError as error:
            response = error
        except (OSError, urllib.error.URLError) as error:
            raise WiretapError("Wiretap control request %s failed: %s" % (path, error)) from error
        with response:
            try:
                raw = response.read(_MAX_RESPONSE + 1)
                if len(raw) > _MAX_RESPONSE:
                    raise WiretapError("Wiretap control response exceeds 16 MiB")
                body = json.loads(raw)
            except (ValueError, UnicodeError) as error:
                raise WiretapError("Wiretap control request %s returned invalid JSON (%s)" % (path, response.code), status=response.code) from error
            except OSError as error:
                raise WiretapError("Wiretap control request %s could not read its response: %s" % (path, error)) from error
            if not isinstance(body, dict):
                raise WiretapError("Wiretap control response must be a JSON object", status=response.code)
            if not 200 <= response.code < 300:
                raise WiretapError("Wiretap %s failed (%s): %s" % (path, response.code, body.get("error", "request failed")), status=response.code, body=body)
            return body

    def start(self, **options):
        if self._closed:
            raise WiretapError("This Wiretap run is already closed")
        if self.info is not None:
            if options:
                raise WiretapError("Start options cannot change an active Wiretap run")
            return self
        try:
            capabilities = self._request("/v1/capabilities")
        except WiretapError as error:
            # Older v1 runtimes have no handshake endpoint. Their normal control
            # responses still undergo the same run/session/receipt checks.
            if error.status != 404:
                raise
        else:
            advertised = capabilities.get("capabilities")
            if (capabilities.get("api_version") != "1.0" or not isinstance(advertised, list)
                    or not all(isinstance(value, str) for value in advertised)
                    or not {"test.identity", "run.finalize"}.issubset(advertised)):
                raise WiretapError("Wiretap runtime does not support this SDK's v1 lifecycle")
            self.capabilities = capabilities
        info = self._request("/v1/startRun", options)
        if not isinstance(info.get("run_id"), str) or not info["run_id"].strip():
            raise WiretapError("Wiretap startup returned no run identity", body=info)
        if not isinstance(info.get("bundle_path"), str) or not os.path.isabs(info["bundle_path"]):
            raise WiretapError("Wiretap startup returned no absolute archive path", body=info)
        _url(info.get("sut_url"))
        self.info = info
        return self

    def test(self, name, *, identity=None, drain_timeout=5, **options):
        """Start an attempt. Pass its url/dependencies/headers to existing clients."""
        if not isinstance(name, str) or not name.strip():
            raise ValueError("name must be nonempty")
        if isinstance(drain_timeout, bool) or not isinstance(drain_timeout, (int, float)) or not math.isfinite(drain_timeout) or not 0 <= drain_timeout <= 60:
            raise ValueError("drain_timeout must be between zero and 60 seconds")
        self.start()
        trace = {"traceparent": "00-%s-%s-01" % (secrets.token_hex(16), secrets.token_hex(8))}
        payload = dict(options, name=name, trace_context=trace)
        if identity is not None:
            payload["test_identity"] = identity
        elif len(name.encode("utf-8")) <= 256:
            payload["test_identity"] = {"id": name}
        response = self._request("/v1/setupTest", payload)
        if not isinstance(response.get("session_id"), str) or not response["session_id"]:
            raise WiretapError("Wiretap setup returned no session identity", body=response)
        if response.get("run_id") != self.info["run_id"] or not _same_path(response.get("bundle_path"), self.info["bundle_path"]):
            raise WiretapError("Wiretap setup returned a different run", body=response)
        _url(response.get("sut_url"))
        dependencies = response.get("dependencies", {})
        if not isinstance(dependencies, dict) or any(not isinstance(key, str) or not isinstance(value, str) for key, value in dependencies.items()):
            raise WiretapError("Wiretap setup returned invalid dependency addresses", body=response)
        attempt = Attempt(self, response, trace, drain_timeout)
        self._active.add(attempt)
        return attempt

    def report(self):
        self.start()
        return self._request("/v1/report", {"bundle_path": self.info["bundle_path"]})

    def _validate_receipt(self, receipt):
        if (receipt.get("status") != "complete" or receipt.get("run_id") != self.info["run_id"]
                or not _same_path(receipt.get("bundle_path"), self.info["bundle_path"])):
            raise WiretapError("Wiretap finalization did not return a complete receipt for the owned run", body=receipt)
        outputs = receipt.get("outputs")
        if outputs is not None and (not isinstance(outputs, dict)
                                    or not _same_path(outputs.get("bundle_path"), self.info["bundle_path"])):
            raise WiretapError("Wiretap output receipt refers to a different archive", body=receipt)
        if isinstance(outputs, dict) and outputs.get("errors"):
            raise WiretapError("Wiretap finalization reported export errors", body=receipt)

    def finish(self, *, outputs=None):
        """Finalize once after all attempts. Partial exports remain on error.body."""
        if self._closed:
            if self._close_error:
                raise self._close_error
            return self.receipt
        if self._active:
            raise WiretapError("Finish every Wiretap attempt before finalizing the run")
        if self.info is None:
            self.start()
        self._closed = True
        try:
            try:
                self.receipt = self._request("/v1/finishRun", {} if outputs is None else {"outputs": outputs})
            except WiretapError as error:
                self.receipt = error.body
                raise
            self._validate_receipt(self.receipt)
            return self.receipt
        except BaseException as error:
            self._close_error = error
            raise

    def close(self):
        return self.finish()


class Attempt:
    """One test attempt and its explicit trace headers."""

    def __init__(self, runtime, response, trace, drain_timeout):
        self.runtime = runtime
        self.session_id = response["session_id"]
        self.url = response["sut_url"].rstrip("/")
        self.dependencies = dict(response.get("dependencies", {}))
        self.headers = dict(trace)
        self.info = response
        self.drain_timeout = drain_timeout
        self.result = None
        self._finished = False
        self._error = None
        self._token = None

    def __enter__(self):
        if self._finished or self._token is not None:
            raise WiretapError("A Wiretap attempt cannot be entered twice")
        self._token = _current.set(self)
        return self

    def finish(self, status="pass"):
        if status not in ("pass", "fail", "skip"):
            raise ValueError("status must be pass, fail or skip")
        if self._finished:
            if self._error:
                raise self._error
            return self.result
        self._finished = True
        try:
            self.result = self.runtime._request("/v1/teardownTest", {
                "session_id": self.session_id, "status": status,
                "drain_timeout_ms": int(self.drain_timeout * 1000),
            }, timeout=max(self.runtime.timeout, self.drain_timeout + 10))
            capture = self.result.get("capture")
            if not isinstance(capture, dict):
                raise WiretapError("Wiretap teardown returned no capture", body=self.result)
            if capture.get("capture_incomplete"):
                raise WiretapError("Wiretap capture is incomplete", body=self.result)
            return self.result
        except BaseException as error:
            self._error = error
            raise
        finally:
            self.runtime._active.discard(self)
            if self._token is not None:
                _current.reset(self._token)
                self._token = None

    def __exit__(self, kind, error, traceback):
        try:
            self.finish("fail" if kind else "pass")
        except BaseException as cleanup_error:
            if error is None:
                raise
            _secondary(error, cleanup_error)
        return False
