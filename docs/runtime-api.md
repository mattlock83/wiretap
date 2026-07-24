# Runtime API (HTTP)

This page documents the session-based runtime API used by the SDK.

Recommended flow:
1. `POST /v1/startServer` once per test suite (or worker)
2. `POST /v1/setupTest` once per test case
3. `POST /v1/queue-response` zero or more times
4. Run test traffic through the proxy
5. `POST /v1/teardownTest` once per test case (SDK method: `teardownTest()`)
6. `POST /v1/report` once after all tests
7. `POST /v1/killServer` once when done

Performance/mock-only flow:
1. `POST /v1/startServer` once
2. `POST /v1/setupTest` with `record_traffic: false`
3. Queue default responses with `POST /v1/queue-response`
4. Point the deployed SUT at `mock_base_url`
5. Run the load tool against the deployed SUT
6. `POST /v1/teardownTest` to clear queued responses

## `POST /v1/startServer`

Starts proxy and mock components if not already running.

Request:

```json
{
  "proxy": {
    "listen_addr": "127.0.0.1:19000",
    "target_url": "http://127.0.0.1:18090"
  },
  "mock": {
    "listen_addr": "127.0.0.1:19001"
  },
  "stack_size": 100,
  "record_responses": {
    "dependency_url": "https://real-dependency.internal"
  }
}
```

Response:

```json
{
  "proxy": {
    "listen_addr": "127.0.0.1:19000",
    "target_url": "http://127.0.0.1:18090"
  },
  "sut_url": "http://127.0.0.1:19000",
  "mock": {
    "listen_addr": "127.0.0.1:19001"
  },
  "stack_size": 100
}
```

Notes:
- `proxy.target_url` enables reverse capture: tests call `sut_url` (the proxy
  address) directly with any HTTP client and requests are forwarded to the
  target. No proxy configuration or CA trust is required for HTTP. Absolute-URI
  forward-proxy requests and CONNECT/MITM still work on the same listener.

## `POST /v1/setupTest`

Creates one isolated test session.

Request:

```json
{
  "name": "submit payment",
  "tags": ["payments", "critical"],
  "meta": {"suite": "complex-rest"},
  "sut_base_url": "http://127.0.0.1:18110",
  "record_traffic": true,
  "explicit_session": false,
  "dependencies": [
    {
      "asset_id": "account-service",
      "asset_name": "Account Service",
      "schema": {
        "id": "account-openapi",
        "type": "openapi",
        "source": {
          "type": "file",
          "path": "examples/complex-rest/schemas/account-openapi.yaml"
        }
      }
    }
  ],
  "sut_schemas": [
    {
      "id": "payment-openapi",
      "role": "sut",
      "type": "openapi",
      "source": {
        "type": "file",
        "path": "examples/complex-rest/schemas/payment-sut-openapi.yaml"
      }
    }
  ]
}
```

Set `record_traffic` to `false` for performance/mock-only sessions. In that mode, Phrond still queues and serves responses, but it does not capture traffic, write a test artifact, or run schema validation. `record_traffic` defaults to `true`.

Response:

```json
{
  "session_id": "session-1770801133915885000-1",
  "name": "submit payment",
  "record_traffic": true,
  "explicit": false,
  "proxy_listen": "http://127.0.0.1:37079",
  "sut_url": "http://127.0.0.1:37079",
  "mock_base_url": "http://127.0.0.1:40713",
  "dependency_urls": {
    "account-service": "http://127.0.0.1:40713/__phrond/dep/account-service"
  }
}
```

Notes:
- **Dependencies** declare the SUT's downstream services. Each gets its own
  stable base URL (`dependency_urls`); inject it into the SUT once at startup.
  Captured flows carry the dependency's `asset_id` as ground truth, so two
  dependencies with overlapping paths never collide.
- **Correlation** is zero-config by default: URLs carry no session component and
  unlabeled traffic is attributed to the single active session. Only one
  zero-config session may be active at a time (a second `setupTest` returns
  409). For parallel suites set `explicit_session: true` — URLs then carry a
  `/__phrond/session/<id>` prefix and coexist freely.
- **Schemas are optional.** Without any, the artifact's `schema_validation`
  status is `skipped` instead of failing — attach specs when you have them.
- A dependency `schema` is registered with `role: fake` and the dependency's
  asset identity; `sut_schemas`/`fake_schemas` still work for schemas not tied
  to a declared dependency.

## `POST /v1/queue-response`

Queues one mock response for a session. Matching uses stack semantics (LIFO).

Request:

```json
{
  "session_id": "session-1770801133915885000-1",
  "id": "account-acct-100",
  "default": false,
  "match": {
    "method": "GET",
    "path": "/accounts/acct-100",
    "headers": {"X-Tenant": "alpha"},
    "query": {"expand": "true"},
    "body": "",
    "body_contains": ""
  },
  "response": {
    "status": 200,
    "delay_ms": 25,
    "headers": {"Content-Type": "application/json"},
    "body": "{\"id\":\"acct-100\"}",
    "body_file": "examples/fixtures/account-balance.json",
    "connection_error": false,
    "ssl_error": false
  }
}
```

Response:

```json
{
  "status": "queued",
  "session_id": "session-1770801133915885000-1",
  "id": "account-acct-100",
  "default": false
}
```

Rules:
- `match.method` and `match.path` are required.
- `asset_id` scopes the response to a declared dependency. Optional when the
  session declares exactly one dependency (used automatically); required when
  it declares several.
- `connection_error` and `ssl_error` are mutually exclusive.
- `body` and `body_file` are literal: `body_file` is read verbatim from disk when queued. There is no templating/rendering.
- `default: true` marks a fallback response:
  - it only matches when no non-default queued response matches,
  - it is not consumed on match,
  - it stays queued until `teardownTest`,
  - it still counts toward `stack_size`.

## `POST /v1/verify`

Records which fields of a mocked response the test depends on. The fields are
attached to the matching flows in the artifact (`consumed_fields`), letting
later analysis distinguish breaking changes to consumed fields from harmless
changes to ignored ones.

Request:

```json
{
  "session_id": "session-1770801133915885000-1",
  "id": "account-acct-100",
  "fields": ["body.balance", "body.status"]
}
```

Response:

```json
{
  "status": "recorded",
  "session_id": "session-1770801133915885000-1",
  "id": "account-acct-100",
  "fields": 2
}
```

## `GET /v1/listQueued?session_id=...`

Lists queued responses that have not been consumed yet.

Response shape:

```json
{
  "session_id": "session-...",
  "items": [
    {
      "id": "account-acct-100",
      "session_id": "session-...",
      "created_at": "2026-02-11T09:12:13.915872Z",
      "source": "queued",
      "default": false,
      "match": {"method":"GET","path":"/accounts/acct-100"},
      "response": {"status":200,"delay_ms":0,"headers":{"Content-Type":"application/json"}}
    }
  ]
}
```

## `GET /v1/listResponded?session_id=...`

Lists responses already consumed by the SUT.

Response shape includes `exchange` payload when available, plus:
- `default` for fallback responses.
- `responded_count` (useful for defaults that can match multiple times).

## `GET /v1/getResponse?session_id=...&id=...`

Returns one queued or responded item in detail.

Useful for test assertions (request URL/body/headers, matched ID, response details).

## `POST /v1/teardownTest`

Finalizes one session, writes testcase artifact, and clears remaining queued responses.

Request:

```json
{
  "session_id": "session-1770801133915885000-1",
  "status": "pass"
}
```

Response:

```json
{
  "session_id": "session-1770801133915885000-1",
  "path": "artifacts/tc-1770801133915870000.yaml",
  "artifact": {},
  "schema_validation": {
    "status": "pass",
    "errors": null
  }
}
```

Note: SDK `teardownTest()` maps to this endpoint.
When `record_traffic` is false, the response contains `artifact_recorded: false` and omits `artifact`, `path`, and `schema_validation`.

## `POST /v1/report`

Builds combined coverage outputs from artifact files.

Request:

```json
{
  "artifacts_dir": "artifacts/run-1770801838-30876",
  "sut_name": "Complex REST Services",
  "include_artifacts": false,
  "collection_path": "artifacts/run-1770801838-30876/coverage.postman_collection.json",
  "workflow_path": "artifacts/run-1770801838-30876/coverage.workflow.yaml",
  "html_path": "artifacts/run-1770801838-30876/coverage.html"
}
```

When paths are omitted, the endpoint writes all three formats using the
filenames shown above. The workflow output conforms to Open Workflow
Specification DSL 1.0.3; Postman output remains supported.

Response:

```json
{
  "collection_path": ".../coverage.postman_collection.json",
  "workflow_path": ".../coverage.workflow.yaml",
  "html_path": ".../coverage.html",
  "report": {}
}
```

## `POST /v1/killServer`

Stops runtime components, clears queued state, and terminates all sessions.

Response:

```json
{
  "status": "killed"
}
```
