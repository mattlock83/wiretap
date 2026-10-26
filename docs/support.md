# Supported integrations and compatibility

This branch supports Jest HTTP API capture, Arazzo workflows and consumer/provider checks. Existing assertions, fixtures, clients and mocks stay with the test suite.

| Integration | Supported surface | Ownership |
| --- | --- | --- |
| Jest | Node 22+; Jest 29.7 or 30, Node test environment | One managed suite runtime; automatic attempt capture and finalization. |
| Runtime control API | HTTP lifecycle endpoints | Underlying service interface used by the adapter. |

The Jest adapter hosts an existing Node HTTP handler or routes to an already running API. It defaults to one worker. Shared concurrent applications require explicit sessions, W3C propagation and fixture isolation. See [Jest details](../sdk/js/docs/jest.md).

| Protocol/contract | Scope |
| --- | --- |
| HTTP/HTTPS + OpenAPI 3.0 | Boundary capture, supported condition coverage and exercised-rule checks. |
| JSON/REST Arazzo | Strict workflow export, portable OpenAPI companions and archive-backed assumption checks. |
| OpenAPI 3.1 and unsupported constructs | Explicit measurement/check limits. |

Only HTTP/HTTPS capture is supported. See [HTTPS trust](https-trust.md), [coverage rules](contract-coverage-rules.md) and [check limits](assumption-checks.md#what-the-check-will-not-establish). Coverage percentages alone do not prove compatibility.

## Format compatibility

| Contract | Identifier | Reader behavior |
| --- | --- | --- |
| Control API | `api_version: "1.0"` | Check version and required capabilities. |
| Raw archive | `format_version: "3.1"` | Enforce supported capabilities and integrity. |
| Coverage/check reports and receipts | `report_version: "1.0"` | Check envelope and measurement rules before decisions. |
| Run sets | `run_set_version: "1.0"` | Verify pinned member hashes and same-build identity. |
| Measurement | Per-report/per-spec `rules_version` | Compare matching known rule identities. |

This experimental branch supports HTTP/OpenAPI archives only. Removed options and evidence are rejected. Preserve complete archives and use this branch's runtime and Jest package together. Producer identity describes the tool, independently of the application revision in `sut_source`.

Schemas: [archive](bundle-evidence.schema.json), [coverage](coverage-report.schema.json), [checks](check-report.schema.json), [run sets](run-set.schema.json), [control/export responses](control-api.schema.json). Successful responses and errors have separate shapes. Check reports support text and JSON.
