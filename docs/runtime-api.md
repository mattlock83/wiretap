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
  "mock": {
    "listen_addr": "127.0.0.1:19001"
  },
  "stack_size": 100
}
```

## `POST /v1/setupTest`

Creates one isolated test session.

Request:

```json
{
  "name": "submit payment",
  "tags": ["payments", "critical"],
  "meta": {"suite": "complex-rest"},
  "sut_base_url": "http://127.0.0.1:18110",
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
  ],
  "fake_schemas": [
    {
      "id": "account-openapi",
      "role": "fake",
      "type": "openapi",
      "source": {
        "type": "file",
        "path": "examples/complex-rest/schemas/account-openapi.yaml"
      }
    }
  ]
}
```

Response:

```json
{
  "session_id": "session-1770801133915885000-1",
  "name": "submit payment",
  "proxy_listen": "http://127.0.0.1:37079",
  "mock_base_url": "http://127.0.0.1:40713/__phrond/session/session-1770801133915885000-1"
}
```

Notes:
- At least one schema is required across `sut_schemas` + `fake_schemas`.
- `mock_base_url` should be injected into the SUT as the dependency base URL.

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
    "body_file": "docs/templates/account-balance.tmpl.json",
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
- `connection_error` and `ssl_error` are mutually exclusive.
- `body_file` is read from disk when queued and rendered per request at runtime.
- `default: true` marks a fallback response:
  - it only matches when no non-default queued response matches,
  - it is not consumed on match,
  - it stays queued until `teardownTest`,
  - it still counts toward `stack_size`.

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
Also clears any session-scoped template variables set via `setVar`.

## `POST /v1/report`

Builds combined coverage outputs from artifact files.

Request:

```json
{
  "artifacts_dir": "artifacts/run-1770801838-30876",
  "sut_name": "Complex REST Services",
  "include_artifacts": false,
  "collection_path": "artifacts/run-1770801838-30876/coverage.postman_collection.json",
  "html_path": "artifacts/run-1770801838-30876/coverage.html"
}
```

Response:

```json
{
  "collection_path": ".../coverage.postman_collection.json",
  "html_path": ".../coverage.html",
  "report": {}
}
```

## `POST /v1/killServer`

Stops runtime components, clears queued state, and terminates all sessions.
Also clears all session-scoped template variables.

Response:

```json
{
  "status": "killed"
}
```
