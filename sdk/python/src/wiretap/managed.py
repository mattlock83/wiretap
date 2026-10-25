"""One owned process, a stdin lifeline, and receipts inside its unique archive."""

import atexit
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile
import threading
import time
import urllib.parse

from .client import Runtime, WiretapError, _duration, _same_path, _secondary, _url


class ManagedRuntime(Runtime):
    """Start a local runtime; the caller keeps ownership of application fixtures.

    A closed stdin stops Wiretap even when Python dies before cleanup. Abrupt
    death preserves an interrupted raw archive, without promising exports.
    """

    def __init__(self, *, binary=None, config=None, cwd=None, output_dir=None,
                 timeout=30, startup_timeout=30, shutdown_timeout=5, outputs=None):
        super().__init__(timeout=timeout)
        self.cwd = Path(cwd or os.getcwd()).resolve()
        self.binary = binary or os.environ.get("WIRETAP_BINARY", "wiretap")
        if not isinstance(self.binary, str) or not self.binary.strip():
            raise ValueError("binary must be an executable path or command")
        # Relative binary paths belong to cwd; bare names use PATH.
        if os.sep in self.binary or (os.altsep and os.altsep in self.binary):
            self.binary = str((self.cwd / self.binary).resolve())
        self.config = str((self.cwd / config).resolve()) if config else None
        self.output_dir = str((self.cwd / output_dir).resolve()) if output_dir else None
        self.startup_timeout = _duration(startup_timeout, "startup_timeout")
        self.shutdown_timeout = _duration(shutdown_timeout, "shutdown_timeout")
        self.outputs = outputs
        self.process = None
        self._directory = None
        self._reader = None
        self._logs = bytearray()
        self._log_lock = threading.Lock()
        self._managed_closed = False
        self._managed_error = None

    def _read_logs(self):
        try:
            while True:
                block = self.process.stdout.read(4096)
                if not block:
                    break
                with self._log_lock:
                    self._logs.extend(block)
                    del self._logs[:-65536]
        finally:
            self.process.stdout.close()

    def _close_stdin(self):
        if self.process is not None and self.process.stdin is not None:
            try:
                self.process.stdin.close()
            except OSError:
                pass

    def _stop(self):
        self._close_stdin()
        if self.process is None:
            return
        try:
            code = self.process.wait(timeout=self.shutdown_timeout)
        except subprocess.TimeoutExpired:
            self.process.kill()
            try:
                self.process.wait(timeout=self.shutdown_timeout)
            except subprocess.TimeoutExpired as error:
                raise WiretapError("Wiretap did not stop after it was killed") from error
            raise WiretapError("Wiretap exceeded its shutdown timeout and was killed")
        finally:
            if self._reader is not None:
                self._reader.join(timeout=self.shutdown_timeout)
        if code != 0:
            raise WiretapError("Wiretap process exited with status %s" % code)

    def start(self, **options):
        if self._managed_closed:
            raise WiretapError("This managed Wiretap runtime is closed")
        if self.process is not None:
            return super().start(**options)
        self._directory = tempfile.mkdtemp(prefix="wiretap-python-")
        ready = Path(self._directory) / "ready.json"
        env = dict(os.environ, WIRETAP_READY_FILE=str(ready), WIRETAP_EXIT_ON_STDIN_EOF="1",
                   WIRETAP_HTTP_LISTEN="127.0.0.1:0", WIRETAP_PROXY_LISTEN="127.0.0.1:0")
        if self.output_dir:
            env["WIRETAP_OUTPUT_DIR"] = self.output_dir
        args = [self.binary] + (["--config", self.config] if self.config else [])
        try:
            self.process = subprocess.Popen(args, cwd=str(self.cwd), env=env,
                                            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                            stderr=subprocess.STDOUT, close_fds=True)
            atexit.register(self._close_stdin)
            self._reader = threading.Thread(target=self._read_logs, name="wiretap-log", daemon=True)
            self._reader.start()
            deadline = time.monotonic() + self.startup_timeout
            while True:
                code = self.process.poll()
                if code is not None:
                    raise WiretapError("Wiretap exited before readiness (status %s): %s" % (code, bytes(self._logs).decode("utf-8", "replace")))
                try:
                    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0)
                    descriptor = os.open(str(ready), flags)
                    with os.fdopen(descriptor, "rb") as stream:
                        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
                            raise WiretapError("Wiretap readiness must be a regular file")
                        raw = stream.read(65537)
                        if len(raw) > 65536:
                            raise WiretapError("Wiretap readiness exceeds 64 KiB")
                        info = json.loads(raw)
                except (FileNotFoundError, ValueError):
                    pass
                else:
                    if not isinstance(info, dict) or info.get("pid") != self.process.pid:
                        raise WiretapError("Wiretap readiness did not identify the owned process")
                    self.base_url = _url(info.get("control_url"), local=True)
                    if urllib.parse.urlsplit(self.base_url).scheme != "http":
                        raise WiretapError("Wiretap local control readiness must use HTTP")
                    break
                if time.monotonic() >= deadline:
                    raise WiretapError("Wiretap did not become ready within %s seconds" % self.startup_timeout)
                time.sleep(0.025)
            super().start(**options)
            try:
                if not Path(self.info["bundle_path"]).is_dir():
                    raise WiretapError("Wiretap startup archive directory does not exist")
                _url(self.info["sut_url"], local=True)
            except BaseException:
                # The owner must not finalize or write to an untrusted startup
                # destination, even when the base control response was valid.
                self.info = None
                raise
            return self
        except BaseException as error:
            try:
                self.close()
            except BaseException as cleanup_error:
                _secondary(error, cleanup_error)
            raise

    def _write_evidence(self):
        if self.info is None:
            return
        directory = Path(self.info["bundle_path"])
        # Save only this owner's receipt, never a reused latest receipt. Invalid
        # identities still fail cleanup and are not written as valid receipts.
        if isinstance(self.receipt, dict):
            if (self.receipt.get("status") == "complete" and self.receipt.get("run_id") == self.info["run_id"]
                    and _same_path(self.receipt.get("bundle_path"), self.info["bundle_path"])):
                with (directory / "last-run.json").open("x", encoding="utf-8") as stream:
                    json.dump(self.receipt, stream, indent=2)
                    stream.write("\n")
        with self._log_lock:
            log = bytes(self._logs)
        with (directory / "wiretap.log").open("xb") as stream:
            stream.write(log)

    def close(self):
        if self._managed_closed:
            if self._managed_error:
                raise self._managed_error
            return self.receipt
        self._managed_closed = True
        failure = None
        if self.info is not None:
            try:
                super().finish(outputs=self.outputs)
            except BaseException as error:
                failure = error
        try:
            self._stop()
        except BaseException as error:
            failure = _secondary(failure, error) if failure else error
        try:
            self._write_evidence()
        except BaseException as error:
            failure = _secondary(failure, error) if failure else error
        atexit.unregister(self._close_stdin)
        if self._directory is not None:
            shutil.rmtree(self._directory, ignore_errors=True)
        self._managed_error = failure
        if failure:
            raise failure
        return self.receipt

    def __enter__(self):
        return self.start()

    def __exit__(self, kind, error, traceback):
        try:
            self.close()
        except BaseException as cleanup_error:
            if error is None:
                raise
            _secondary(error, cleanup_error)
        return False
