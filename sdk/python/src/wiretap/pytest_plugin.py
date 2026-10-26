"""Optional pytest integration. Capture is enabled by --wiretap or its fixture."""

import hashlib
import pytest

from . import ManagedRuntime


def pytest_addoption(parser):
    group = parser.getgroup("wiretap", "Wiretap API capture")
    group.addoption("--wiretap", action="store_true", help="capture each selected test")
    group.addoption("--wiretap-binary", help="runtime executable (default: WIRETAP_BINARY or PATH)")
    group.addoption("--wiretap-config", help="project configuration, relative to pytest root")
    group.addoption("--wiretap-output-dir", help="artifact root, relative to pytest root")
    group.addoption("--wiretap-timeout", type=float, default=30, help="control request deadline in seconds")
    group.addoption("--wiretap-drain-timeout", type=float, default=5, help="attempt drain in seconds (0-60)")


def pytest_configure(config):
    config._wiretap_state = {"owners": set(), "active": {}, "ordinals": {}}


@pytest.fixture(scope="session")
def wiretap_runtime(pytestconfig):
    """Override to return a started Runtime/ManagedRuntime for existing fixtures.

    The plugin finalizes this object after the last teardown report. Do not add
    a fixture finalizer that closes it while that final test is still active.
    """
    runtime = ManagedRuntime(binary=pytestconfig.getoption("--wiretap-binary"),
                             config=pytestconfig.getoption("--wiretap-config"),
                             cwd=str(pytestconfig.rootpath),
                             output_dir=pytestconfig.getoption("--wiretap-output-dir"),
                             timeout=pytestconfig.getoption("--wiretap-timeout"))
    return runtime.start()


@pytest.fixture(autouse=True)
def _wiretap_attempt(request):
    """Enter through pytest's native fixture lifecycle, after session setup."""
    item = request.node
    if not item.config.getoption("--wiretap") and "wiretap" not in item.fixturenames:
        return None
    state = item.config._wiretap_state
    owner = request.getfixturevalue("wiretap_runtime")
    state["owners"].add(owner)
    ordinal = state["ordinals"].get(item.nodeid, 0) + 1
    state["ordinals"][item.nodeid] = ordinal
    identity = {"id": _identity(item.nodeid), "attempt_ordinal": ordinal}
    if hasattr(item, "callspec"):
        identity["variant"] = _identity(item.callspec.id)
    attempt = owner.test(item.nodeid, identity=identity,
                         drain_timeout=item.config.getoption("--wiretap-drain-timeout"))
    state["active"][item] = attempt
    item._wiretap_outcomes = []
    return attempt.__enter__()


@pytest.fixture
def wiretap(_wiretap_attempt):
    """The current attempt: url, dependencies and explicit W3C trace headers."""
    return _wiretap_attempt


def _identity(value):
    return value if len(value.encode("utf-8")) <= 256 else "sha256:" + hashlib.sha256(value.encode("utf-8")).hexdigest()


@pytest.hookimpl(hookwrapper=True, tryfirst=True)
def pytest_runtest_makereport(item, call):
    result = yield
    report = result.get_result()
    state = item.config._wiretap_state
    attempt = state["active"].get(item)
    if attempt is None:
        return
    outcome = "fail" if report.failed else "skip" if report.skipped else "pass"
    # An expected assertion failure still executed and failed; an explicit
    # pytest.xfail() is a skipped execution, as is an ordinary runtime skip.
    if (report.skipped and hasattr(report, "wasxfail") and call.excinfo is not None
            and not call.excinfo.errisinstance(pytest.xfail.Exception)):
        outcome = "fail"
    item._wiretap_outcomes.append(outcome)
    if report.when != "teardown":
        return
    outcomes = item._wiretap_outcomes
    status = "fail" if "fail" in outcomes else "skip" if "skip" in outcomes else "pass"
    try:
        attempt.finish(status)
    except Exception as error:
        if report.failed:
            report.sections.append(("Wiretap cleanup", str(error)))
        else:
            report.outcome = "failed"
            report.longrepr = "Wiretap cleanup failed: %s" % error
    finally:
        state["active"].pop(item, None)


def pytest_sessionfinish(session, exitstatus):
    state = session.config._wiretap_state
    errors = []
    for attempt in list(state["active"].values()):
        try:
            attempt.finish("fail")
        except Exception as error:
            errors.append(error)
    state["active"].clear()
    for owner in state["owners"]:
        try:
            receipt = owner.close()
            if receipt:
                lines = ["Wiretap archive: %s" % receipt["bundle_path"]]
                outputs = receipt.get("outputs") or {}
                for kind, path in sorted((outputs.get("coverage") or {}).items()):
                    lines.append("Wiretap coverage (%s): %s" % (kind, path))
                for path in outputs.get("postman") or []:
                    lines.append("Wiretap Postman: %s" % path)
                if outputs.get("arazzo"):
                    lines.append("Wiretap Arazzo: %s" % outputs["arazzo"])
                if hasattr(session.config, "workeroutput"):
                    session.config.workeroutput.setdefault("wiretap_summary", []).extend(lines)
                else:
                    _write_summary(session.config, lines)
        except Exception as error:
            errors.append(error)
    if errors:
        reporter = session.config.pluginmanager.getplugin("terminalreporter")
        if reporter:
            for error in errors:
                reporter.write_line("Wiretap finalization failed: %s" % error, red=True)
        if session.exitstatus == 0:
            session.exitstatus = pytest.ExitCode.TESTS_FAILED


def _write_summary(config, lines):
    reporter = config.pluginmanager.getplugin("terminalreporter")
    if reporter:
        for line in lines:
            reporter.write_line(line)


@pytest.hookimpl(optionalhook=True)
def pytest_testnodedown(node, error):
    """Relay completed worker exports through xdist's controller reporter."""
    _write_summary(node.config, getattr(node, "workeroutput", {}).get("wiretap_summary", []))
