"""Real grpcio clients, protobuf contracts and native Postman gRPC exports."""

from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
from urllib.parse import urlsplit

import grpc
from google.protobuf import descriptor_pb2, descriptor_pool, message_factory
import pytest

from wiretap import ManagedRuntime


pytestmark = pytest.mark.skipif(not os.environ.get("WIRETAP_BINARY"), reason="set WIRETAP_BINARY to a built runtime")
SERVICE = "wiretap.python.v1.EchoService"


@pytest.fixture
def grpc_project(tmp_path):
    # One descriptor supplies both Python's message class and Wiretap's frozen
    # contract. No generated client code or protoc installation is needed.
    descriptor = descriptor_pb2.FileDescriptorProto(
        name="python.proto", package="wiretap.python.v1", syntax="proto3")
    message = descriptor.message_type.add(name="Message")
    message.field.add(name="value", number=1, type=descriptor_pb2.FieldDescriptorProto.TYPE_STRING,
                      label=descriptor_pb2.FieldDescriptorProto.LABEL_OPTIONAL)
    service = descriptor.service.add(name="EchoService")
    for method, streaming in [("Echo", False), ("Watch", True)]:
        service.method.add(name=method, input_type=".wiretap.python.v1.Message",
                           output_type=".wiretap.python.v1.Message", server_streaming=streaming)
    pool = descriptor_pool.DescriptorPool()
    pool.Add(descriptor)
    Message = message_factory.GetMessageClass(pool.FindMessageTypeByName("wiretap.python.v1.Message"))
    contract = descriptor_pb2.FileDescriptorSet(file=[descriptor])
    (tmp_path / "python.protoset").write_bytes(contract.SerializeToString())
    received = {}

    def echo(request, context):
        received[request.value] = dict(context.invocation_metadata())
        return Message(value="reply-" + request.value)

    def watch(request, context):
        received[request.value] = dict(context.invocation_metadata())
        for index in range(2):
            yield Message(value="reply-%s-%d" % (request.value, index + 1))

    with ThreadPoolExecutor(max_workers=2) as executor:
        server = grpc.server(executor)
        server.add_generic_rpc_handlers((grpc.method_handlers_generic_handler(SERVICE, {
            "Echo": grpc.unary_unary_rpc_method_handler(
                echo, request_deserializer=Message.FromString, response_serializer=Message.SerializeToString),
            "Watch": grpc.unary_stream_rpc_method_handler(
                watch, request_deserializer=Message.FromString, response_serializer=Message.SerializeToString),
        }),))
        port = server.add_insecure_port("127.0.0.1:0")
        assert port
        (tmp_path / ".wiretap.yaml").write_text("""version: 1
application: {id: python-grpc}
output_dir: artifacts
outputs: {coverage: [json, text], postman: true}
sut:
  id: echo-grpc
  kind: service
  protocol: grpc
  url: http://127.0.0.1:%d
  services: [wiretap.python.v1.EchoService]
  schema:
    source: descriptor_set
    path: python.protoset
""" % port)
        server.start()
        try:
            yield tmp_path, Message, received
        finally:
            server.stop(0).wait(timeout=5)


def projections(directory, attempt, flow, phase):
    bodies = sorted((body for body in attempt["bodies"]
                     if body["flow_id"] == flow["id"] and body["phase"] == phase and not body.get("kind")),
                    key=lambda body: body["message_index"])
    return [json.loads((directory / body["path"]).read_text()) for body in bodies]


def test_grpc_unary_and_server_stream_capture_identity_contract_and_export(grpc_project):
    project, Message, received = grpc_project
    expected = {"Echo": ["reply-Echo"], "Watch": ["reply-Watch-1", "reply-Watch-2"]}
    identities = {}
    traces = {}
    with ManagedRuntime(cwd=str(project)) as capture:
        # A regular reusable grpcio channel receives explicit per-attempt
        # metadata; Wiretap does not patch the client or install interceptors.
        with grpc.insecure_channel(urlsplit(capture.info["sut_url"]).netloc) as channel:
            for method, replies in expected.items():
                identity = {"id": "python-grpc", "variant": method, "attempt_ordinal": 1}
                identities[method] = identity
                with capture.test("Python " + method, identity=identity, explicit_session=True) as attempt:
                    factory = channel.unary_unary if method == "Echo" else channel.unary_stream
                    call = factory("/" + SERVICE + "/" + method,
                                   request_serializer=Message.SerializeToString,
                                   response_deserializer=Message.FromString)
                    response = call(Message(value=method), metadata=list(attempt.headers.items()), timeout=10)
                    values = [response.value] if method == "Echo" else [message.value for message in response]
                    assert values == replies
                    traces[method] = attempt.headers["traceparent"]
        directory = Path(capture.info["bundle_path"])

    bundle = json.loads((directory / "bundle.json").read_text())
    assert bundle["run_state"] == "complete"
    assert len(bundle["tests"]) == 2
    for test in bundle["tests"]:
        saved = json.loads((directory / test["path"]).read_text())
        recorded = saved["capture"]
        method = recorded["testcase"]["identity"]["variant"]
        assert recorded["testcase"]["identity"] == identities[method]
        assert recorded["testcase"]["status"] == "pass"
        assert not recorded.get("capture_incomplete")
        assert len(recorded["traffic"]) == 1
        flow = recorded["traffic"][0]
        assert flow["protocol"] == "grpc"
        assert flow["channel"] == "test_to_sut"
        assert flow["test_correlation"] == "trace"
        assert flow["trace_context_source"] == "received"
        assert flow["trace_id"] == recorded["testcase"]["trace_id"] == traces[method].split("-")[1]
        assert received[method]["traceparent"] == traces[method]
        assert flow["request"]["procedure"] == "/" + SERVICE + "/" + method
        assert flow["request"]["stream_kind"] == ("unary" if method == "Echo" else "server_streaming")
        assert flow["response"]["grpc_status"] == 0
        trailers = {name.lower(): values for name, values in flow["response"]["trailers"].items()}
        assert trailers["grpc-status"] == ["0"]
        assert projections(directory, saved, flow, "request") == [{"value": method}]
        assert projections(directory, saved, flow, "response") == [{"value": value} for value in expected[method]]

    receipt = json.loads((directory / "last-run.json").read_text())
    assert receipt["status"] == "complete"
    assert receipt["outputs"]["errors"] == []
    report = json.loads(Path(receipt["outputs"]["coverage"]["json"]).read_text())
    assert report["artifact_count"] == 2
    spec, = report["provided"]["specs"]
    assert spec["type"] == "protobuf"
    assert spec["denominator_known"]
    procedures = {"/" + SERVICE + "/" + method for method in expected}
    assert {operation["operation"] for operation in spec["exercised"]} == procedures
    assert not spec.get("not_covered")
    contract, = report["contract_coverage"]["specs"]
    assert contract["rules_version"] == "wiretap-protobuf-2"
    assert not contract.get("violations")
    operations = [target for target in contract["targets"] if target["family"] == "operation"]
    assert {target["operation"] for target in operations} == procedures
    assert all(target["state"] == "covered" for target in operations)

    export, = receipt["outputs"]["postman"]
    collection = json.loads(Path(export).read_text())
    assert collection["$kind"] == "collection"
    assert {folder["name"] for folder in collection["items"]} == {"Python " + method for method in expected}
    for folder in collection["items"]:
        request, = folder["items"]
        method = request["methodPath"].split("/")[-1]
        assert request["$kind"] == "grpc-request"
        assert request["methodPath"] == SERVICE + "/" + method
        assert json.loads(request["message"]["content"]) == {"value": method}
        example, = request["examples"]
        assert example["$kind"] == "grpc-example"
        assert example["response"]["code"] == 0
        messages = example["response"]["messages"]
        assert [item["type"] for item in messages] == ["outgoing"] + ["incoming"] * len(expected[method])
        assert [json.loads(item["content"]) for item in messages] == [
            {"value": value} for value in [method] + expected[method]]
