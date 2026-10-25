import json
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import pytest


def get_item(capture, identifier):
    request = Request(capture.url + "/items/%s" % identifier, headers=capture.headers)
    try:
        response = urlopen(request, timeout=5)
    except HTTPError as error:
        response = error
    with response:
        return response.status, json.load(response)


@pytest.mark.parametrize("identifier", [1, 2], ids=["first", "second"])
def test_reads_item(wiretap, identifier):
    status, body = get_item(wiretap, identifier)
    assert status == 200
    assert body == {"id": identifier, "name": "item-%d" % identifier}


def test_rejects_invalid_identifier(wiretap):
    # The invalid request remains validation evidence; this assertion passes.
    status, body = get_item(wiretap, 0)
    assert status == 400
    assert "error" in body


@pytest.fixture
def cleanup_request(wiretap):
    yield
    assert get_item(wiretap, 3)[0] == 200


def test_captures_fixture_cleanup(wiretap, cleanup_request):
    assert get_item(wiretap, 4)[0] == 200
