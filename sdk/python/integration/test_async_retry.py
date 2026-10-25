"""Existing asynchronous clients and pytest retries against a real runtime."""

import asyncio
import os
from pathlib import Path
from urllib.parse import urlsplit

import httpx
import pytest

from wiretap import ManagedRuntime, trace_headers

from test_capture import archives, assert_exports, attempts, project, run_pytest


pytestmark = pytest.mark.skipif(not os.environ.get("WIRETAP_BINARY"), reason="set WIRETAP_BINARY to a built runtime")


def test_overlapping_async_clients_keep_attempts_and_cleanup_isolated(project):
    contexts = {}

    async def exercise(capture):
        entered = asyncio.Event()
        proceed = asyncio.Event()

        async with httpx.AsyncClient(trust_env=False, timeout=10) as client:
            async def context_request(test, name):
                # A task created inside this attempt inherits its context, even
                # while the other attempt is using the same ordinary client.
                assert trace_headers() == contexts[name]
                response = await client.get(test.url + "/orders/" + name + "-context", headers=trace_headers())
                assert response.json() == {"id": name + "-context"}
                assert trace_headers() == contexts[name]

            async def worker(name):
                assert trace_headers() == {}
                try:
                    with capture.test(name, explicit_session=True) as test:
                        contexts[name] = dict(test.headers)
                        if len(contexts) == 2:
                            entered.set()
                        await proceed.wait()
                        assert trace_headers() == contexts[name]
                        response = await client.get(test.url + "/orders/" + name + "-explicit", headers=test.headers)
                        assert response.json() == {"id": name + "-explicit"}
                        await asyncio.create_task(context_request(test, name))
                        if name == "failed":
                            raise AssertionError("expected worker failure")
                finally:
                    assert trace_headers() == {}, "an ended attempt leaked its context"

            tasks = [asyncio.create_task(worker(name)) for name in ("passed", "failed")]
            ready = asyncio.create_task(entered.wait())
            try:
                done, _ = await asyncio.wait([ready, *tasks], timeout=10, return_when=asyncio.FIRST_COMPLETED)
                for task in tasks:
                    if task in done:
                        await task  # Surface setup failures before waiting for the other worker.
                assert ready in done, "both asynchronous attempts did not become ready"
                assert trace_headers() == {}, "child attempts changed their parent's context"
                assert contexts["passed"] != contexts["failed"]
                proceed.set()
                results = await asyncio.gather(*tasks, return_exceptions=True)
                assert results[0] is None
                assert isinstance(results[1], AssertionError)
                assert str(results[1]) == "expected worker failure"
            finally:
                for task in [ready, *tasks]:
                    if not task.done():
                        task.cancel()
                await asyncio.gather(ready, *tasks, return_exceptions=True)
            assert trace_headers() == {}

    with ManagedRuntime(cwd=str(project)) as capture:
        asyncio.run(exercise(capture))
        directory = Path(capture.info["bundle_path"])
    records = attempts(directory)
    assert len(records) == 2
    assert {record["testcase"]["name"]: record["testcase"]["status"] for record in records} == {
        "passed": "pass", "failed": "fail",
    }
    for record in records:
        name = record["testcase"]["name"]
        trace_id = contexts[name]["traceparent"].split("-")[1]
        assert record["testcase"]["trace_id"] == trace_id
        assert len(record["traffic"]) == 2
        assert {urlsplit(flow["request"]["url"]).path for flow in record["traffic"]} == {
            "/orders/" + name + "-explicit", "/orders/" + name + "-context",
        }
        assert all(flow["trace_id"] == trace_id for flow in record["traffic"])
        assert record["schema_validation"]["status"] == "pass"
    assert trace_headers() == {}
    assert_exports(directory)


@pytest.mark.parametrize("recovers", [True, False], ids=["fail-then-pass", "exhausted"])
def test_pytest_retries_preserve_every_attempt_and_runner_outcome(project, recovers):
    result = run_pytest(project, '''
        import pytest
        from urllib.request import Request, urlopen
        from wiretap import trace_headers

        executions = 0

        def request_order(wiretap, phase, ordinal):
            assert trace_headers() == wiretap.headers
            with urlopen(Request(wiretap.url + '/orders/' + phase + '-' + str(ordinal), headers=trace_headers())) as response:
                assert response.status == 200

        @pytest.fixture
        def lifecycle(wiretap):
            global executions
            executions += 1
            ordinal = executions
            request_order(wiretap, 'setup', ordinal)
            yield ordinal
            request_order(wiretap, 'cleanup', ordinal)

        def test_retried(wiretap, lifecycle):
            request_order(wiretap, 'test', lifecycle)
            assert RECOVERS and lifecycle == 2, 'intentional retry failure'
    '''.replace("RECOVERS", repr(recovers)), "-p", "pytest_rerunfailures", "--reruns", "1")
    assert result.returncode == (0 if recovers else 1), result.stdout + result.stderr
    directories = archives(project)
    assert len(directories) == 1, result.stdout + result.stderr
    records = sorted(attempts(directories[0]), key=lambda record: record["testcase"]["identity"]["attempt_ordinal"])
    assert len(records) == 2, result.stdout + result.stderr
    assert [record["testcase"]["identity"]["attempt_ordinal"] for record in records] == [1, 2]
    assert [record["testcase"]["status"] for record in records] == ["fail", "pass" if recovers else "fail"]
    assert len({record["testcase"]["identity"]["id"] for record in records}) == 1
    assert len({record["testcase"]["id"] for record in records}) == 2
    assert len({record["testcase"]["trace_id"] for record in records}) == 2
    for ordinal, record in enumerate(records, start=1):
        assert len(record["traffic"]) == 3
        assert {urlsplit(flow["request"]["url"]).path for flow in record["traffic"]} == {
            "/orders/" + phase + "-" + str(ordinal) for phase in ("setup", "test", "cleanup")
        }
        assert all(flow["trace_id"] == record["testcase"]["trace_id"] for flow in record["traffic"])
        assert record["schema_validation"]["status"] == "pass"
    assert_exports(directories[0], result.stdout + result.stderr)
