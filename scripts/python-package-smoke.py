#!/usr/bin/env python3
"""Exercise a locally built wheel in clean environments; never publish it."""

import argparse
from email.parser import BytesParser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import threading
import venv
import zipfile


ROOT = Path(__file__).resolve().parents[1]
INSTALLED_CHECK = """\
from pathlib import Path
import sys
import wiretap
Path(wiretap.__file__).resolve().relative_to(Path(sys.prefix).resolve())
"""
REQUEST = """\
from urllib.request import Request, urlopen
import json
with urlopen(Request(attempt.url + '/items/42', headers=attempt.headers), timeout=10) as response:
    assert json.load(response) == {'id': 42}
"""


def run(command, *, cwd, env):
    print("+ " + " ".join(map(str, command)), flush=True)
    subprocess.run(list(map(str, command)), cwd=str(cwd), env=env, check=True, timeout=300)


def environment():
    # Neither the invoking interpreter nor a developer's pytest configuration
    # may supply a source checkout, third-party plugin or editable installation.
    result = {key: value for key, value in os.environ.items()
              if not key.startswith(("PYTHON", "PYTEST", "WIRETAP")) and key != "VIRTUAL_ENV"}
    binary = shutil.which(os.environ.get("WIRETAP_BINARY", "wiretap"))
    if binary is None:
        raise SystemExit("Set WIRETAP_BINARY to a locally built Wiretap executable")
    result["WIRETAP_BINARY"] = str(Path(binary).resolve())
    result["PIP_DISABLE_PIP_VERSION_CHECK"] = "1"
    return result


def interpreter(directory):
    venv.EnvBuilder(with_pip=True, system_site_packages=False).create(str(directory))
    return directory / ("Scripts/python.exe" if os.name == "nt" else "bin/python")


def check_wheel(path):
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        metadata_names = [name for name in names if name.endswith(".dist-info/METADATA")]
        assert len(metadata_names) == 1, "wheel must have one package metadata record"
        metadata = BytesParser().parsebytes(archive.read(metadata_names[0]))
        assert metadata["Name"] == "wiretap-sdk"
        assert metadata["Version"] and metadata["Requires-Python"]
        assert (metadata["License-Expression"] or metadata["License"]) == "MIT"
        for requirement in metadata.get_all("Requires-Dist", []):
            assert re.search(r";\s*extra == ['\"][^'\"]+['\"]$", requirement), (
                "core installation must have no third-party dependencies: " + requirement)
        licenses = [name for name in names if name.endswith("/LICENSE")]
        expected = (ROOT / "sdk/python/LICENSE").read_bytes()
        assert any(archive.read(name) == expected for name in licenses), "wheel must include the MIT license"
        assert "wiretap/pytest_plugin.py" in names
        assert "wiretap/__init__.py" in names


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = b'{"id":42}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass


def project(directory, artifacts, port):
    directory.mkdir()
    schema = {"openapi": "3.0.3", "info": {"title": "Wheel acceptance", "version": "1"},
              "paths": {"/items/{id}": {"get": {
                  "parameters": [{"name": "id", "in": "path", "required": True,
                                  "schema": {"type": "integer"}}],
                  "responses": {"200": {"description": "Item", "content": {
                      "application/json": {"schema": {"type": "object", "required": ["id"],
                                                       "properties": {"id": {"type": "integer"}}}}}}}}}}}
    (directory / "openapi.json").write_text(json.dumps(schema), encoding="utf-8")
    (directory / ".wiretap.yaml").write_text("""version: 1
application: {id: wheel-acceptance}
output_dir: %s
outputs: {coverage: [json, text], postman: true}
sut:
  id: items
  kind: service
  url: http://127.0.0.1:%d
  schema: openapi.json
""" % (json.dumps(str(artifacts)), port), encoding="utf-8")


def check_capture(artifacts):
    bundles = list(artifacts.rglob("bundle.json"))
    assert len(bundles) == 1, "expected exactly one owned capture archive"
    directory = bundles[0].parent
    bundle = json.loads(bundles[0].read_text(encoding="utf-8"))
    assert len(bundle["tests"]) == 1
    capture = json.loads((directory / bundle["tests"][0]["path"]).read_text(encoding="utf-8"))["capture"]
    assert capture["testcase"]["status"] == "pass"
    assert len(capture["traffic"]) == 1
    assert capture["schema_validation"]["status"] == "pass"
    receipt = json.loads((directory / "last-run.json").read_text(encoding="utf-8"))
    assert receipt["status"] == "complete" and Path(receipt["bundle_path"]) == directory
    outputs = receipt["outputs"]
    assert outputs["errors"] == [] and outputs["postman"]
    assert json.loads(Path(outputs["coverage"]["json"]).read_text(encoding="utf-8"))["report_version"] == "1.0"
    assert Path(outputs["coverage"]["text"]).is_file()
    for name in outputs["postman"]:
        collection = json.loads(Path(name).read_text(encoding="utf-8"))
        assert collection["info"]["schema"].endswith("/v2.1.0/collection.json")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pytest", default="pytest>=7.4,<10", help="pytest compatibility constraint")
    parser.add_argument("--output-dir", type=Path, default=ROOT / "artifacts/python-package")
    args = parser.parse_args()
    env = environment()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    artifacts = Path(tempfile.mkdtemp(prefix="run-", dir=str(args.output_dir.resolve())))
    print("Python wheel acceptance evidence: " + str(artifacts), flush=True)
    with tempfile.TemporaryDirectory(prefix="wiretap-wheel-") as temporary:
        workspace = Path(temporary)
        build_python = interpreter(workspace / "build-env")
        # Older Python installers can bundle pip versions that misread project metadata.
        run([build_python, "-I", "-m", "pip", "install", "--upgrade", "pip"], cwd=workspace, env=env)
        run([build_python, "-I", "-m", "pip", "wheel", "--no-deps", "--wheel-dir", workspace / "wheels",
             ROOT / "sdk/python"], cwd=workspace, env=env)
        wheels = list((workspace / "wheels").glob("*.whl"))
        assert len(wheels) == 1
        wheel = wheels[0]
        check_wheel(wheel)
        core_python = interpreter(workspace / "core-env")
        run([core_python, "-I", "-m", "pip", "install", "--no-deps", wheel], cwd=workspace, env=env)
        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            core_project = workspace / "core-project"
            project(core_project, artifacts / "core", server.server_port)
            source = INSTALLED_CHECK + """\
import importlib.util
assert importlib.util.find_spec('pytest') is None, 'core install unexpectedly includes pytest'
from wiretap import ManagedRuntime
with ManagedRuntime() as runtime:
    with runtime.test('core wheel capture') as attempt:
""" + "".join("        " + line + "\n" for line in REQUEST.splitlines())
            run([core_python, "-I", "-c", source], cwd=core_project, env=env)
            check_capture(artifacts / "core")
            run([core_python, "-I", "-m", "unittest", "discover", "-s", ROOT / "sdk/python/tests", "-v"],
                cwd=workspace, env=env)

            pytest_python = interpreter(workspace / "pytest-env")
            run([pytest_python, "-I", "-m", "pip", "install", str(wheel) + "[pytest]", args.pytest],
                cwd=workspace, env=env)
            pytest_project = workspace / "pytest-project"
            project(pytest_project, artifacts / "pytest", server.server_port)
            source = INSTALLED_CHECK + "\ndef test_installed_fixture(wiretap, pytestconfig):\n"
            source += "    assert pytestconfig.pluginmanager.hasplugin('wiretap')\n    attempt = wiretap\n"
            source += "".join("    " + line + "\n" for line in REQUEST.splitlines())
            (pytest_project / "test_capture.py").write_text(source, encoding="utf-8")
            # No -p or PYTHONPATH: entry-point discovery must load the installed plugin.
            run([pytest_python, "-I", "-m", "pytest", "-q"], cwd=pytest_project, env=env)
            check_capture(artifacts / "pytest")
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)

        run([pytest_python, "-I", "-m", "pip", "install", str(wheel) + "[test]", args.pytest],
            cwd=workspace, env=env)
        run([pytest_python, "-I", "-m", "pip", "check"], cwd=workspace, env=env)
        run([pytest_python, "-I", "-m", "pytest", "-q", ROOT / "sdk/python/integration",
             "--basetemp", artifacts / "integration"], cwd=workspace, env=env)
        for name, options in [("example", []), ("example-xdist", ["-n", "2"])]:
            run([pytest_python, "-I", "-m", "pytest", "-q", *options, ROOT / "examples/pytest-suite"],
                cwd=workspace, env=dict(env, WIRETAP_OUTPUT_DIR=str(artifacts / name)))
    print("Python wheel acceptance passed: " + str(artifacts), flush=True)


if __name__ == "__main__":
    main()
