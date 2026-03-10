# Mock Template File Format

`queue-response` supports response templates in `response.body` and `response.headers`.

Templates can be inline strings or loaded via `response.body_file`.
Standalone mode (`--standalone`) also uses template files with the same response template engine.

## Engine

Templates use Go `text/template` syntax:
- Field access: `{{.Request.Path}}`
- Function calls: `{{cel "path"}}`, `{{celJSON "{'path': path}"}}`
- Full Sprig function library (`sprig.TxtFuncMap`)
- Full gofakeit Faker function surface (e.g. `{{FirstName}}`, `{{Email}}`, `{{IntRange 10 20}}`)

Notes:
- gofakeit functions are exposed using their original method names and a lower-camel alias.
  Example: `{{FirstName}}` and `{{firstName}}` are both valid.

## Data Model

Go template context:
- `.Request.Method`
- `.Request.Path`
- `.Request.URL`
- `.Request.Headers` (first-value map)
- `.Request.Query` (first-value map)
- `.Request.Body`
- `.Now` (`time.Time`, UTC)

CEL variables:
- `request` (map with `method`, `path`, `url`, `headers`, `query`, `body`)
- `method`
- `path`
- `url`
- `headers`
- `query`
- `body`
- `now` (timestamp)

## CEL Helpers

- `cel "<expr>"`: evaluates CEL and renders as string.
- `celJSON "<expr>"`: evaluates CEL and JSON-encodes the result.

## Additional Helpers

- `mustFromJSON "<raw>"`: decode JSON string into object/array.
- `mustToJSON <value>`: JSON encode any value.
- `mustToPrettyJSON <value>`: indented JSON encode.
- `nowUTC`: fixed render timestamp in UTC.
- `nowPlus "<duration>"`: add duration to render timestamp (e.g. `"24h"`).
- `formatTime "<layout>" <timeOrString>`: format a time value.
- `uuid`: generate UUIDv4 string.
- `setVar "<key>" <value>`: store a session-scoped variable (returns empty string).
- `getVar "<key>"`: read a session-scoped variable (returns empty string when missing).
- `mustGetVar "<key>"`: read a session-scoped variable and fail rendering when missing.

Date math example:

```gotemplate
{{cel "now + duration('24h')"}}
```

## Example: Inline Template

```json
{
  "session_id": "session-1",
  "id": "submit-payment-response",
  "match": {"method":"POST","path":"/payments/submit"},
  "response": {
    "status": 200,
    "headers": {
      "Content-Type": "application/json",
      "X-Trace": "{{index .Request.Headers \"X-Trace-Id\"}}"
    },
    "body": "{\"received_path\":\"{{.Request.Path}}\",\"expires_at\":\"{{cel \"now + duration('24h')\"}}\"}"
  }
}
```

## Example: File-Based Template

Use `response.body_file`:

```json
{
  "session_id": "session-1",
  "id": "account-lookup",
  "match": {"method":"GET","path":"/accounts/acct-100"},
  "response": {
    "status": 200,
    "headers": {"Content-Type":"application/json"},
    "body_file": "docs/templates/account-balance.tmpl.json"
  }
}
```

Template files included in this repo:
- `docs/templates/account-balance.tmpl.json`
- `docs/templates/payment-submit.tmpl.json`
- `docs/templates/payment-complex-account-context.tmpl.json`
- `docs/templates/payment-complex.tmpl.json`

## Complex Template Example

Complex template flow now uses two files:
- `docs/templates/payment-complex-account-context.tmpl.json` sets session variables with `setVar`.
- `docs/templates/payment-complex.tmpl.json` reads those variables with `mustGetVar/getVar`.

Together they demonstrate:
- Sprig string/regex/hash helpers
- Sprig object composition (`dict`, `list`)
- strict JSON helpers (`mustFromJSON`, `mustToJSON`, `mustToPrettyJSON`)
- time helpers (`nowUTC`, `nowPlus`, `formatTime`)
- gofakeit usage (`Name`, `Email`, `phoneFormatted`)
- CEL projection (`celJSON`)
- cross-template variable sharing (`setVar`, `mustGetVar`)

Queue order example:

```json
{
  "session_id": "session-1",
  "id": "account-context",
  "match": {"method":"GET","path":"/accounts/context"},
  "response": {"status":200,"body_file":"docs/templates/payment-complex-account-context.tmpl.json"}
}
```

```json
{
  "session_id": "session-1",
  "id": "payment-submit",
  "match": {"method":"POST","path":"/payments/submit"},
  "response": {"status":200,"body_file":"docs/templates/payment-complex.tmpl.json"}
}
```

## Notes

- Templates are rendered per matched request.
- Variables set with `setVar` are shared across templates in the same runtime `session_id`.
- `setVar/getVar/mustGetVar` require session-scoped requests (runtime `setupTest` + session-prefixed mock URL).
- `mustGetVar` is useful when call order is required; it fails fast if a prerequisite value was not set.
- Queue matching is stack-based (latest queued response is consumed first).
- Use deterministic IDs in tests so `getResponse(id)` can be used for assertions.

## Standalone Template Files

Standalone mode loads JSON template files that include match + response metadata:

```json
{
  "id": "account-default",
  "scenario_id": "insufficient-balance",
  "match": {"method":"GET","path":"/accounts/acct-100"},
  "response": {
    "status": 200,
    "headers": {"Content-Type":"application/json"},
    "body_file": "docs/templates/account-balance.tmpl.json"
  }
}
```

`scenario_id` is optional. When present, it matches only when the configured scenario header (default `X-Phrond-Scenario`) has that value.

## Cross-Template Variable Example

Template A:

```gotemplate
{{- setVar "account_number" "acct-100" -}}
{"status":"loaded"}
```

Template B:

```gotemplate
{"from_account":"{{mustGetVar "account_number"}}"}
```

If Template B runs before Template A in the same `session_id`, `mustGetVar` returns a template error.
