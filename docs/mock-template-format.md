# Mock Template File Format

`queue-response` supports response templates in `response.body` and `response.headers`.

Templates can be inline strings or loaded via `response.body_file`.

## Engine

Templates use Go `text/template` syntax:
- Field access: `{{.Request.Path}}`
- Function calls: `{{cel "path"}}`, `{{celJSON "{'path': path}"}}`

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

## Notes

- Templates are rendered per matched request.
- Queue matching is stack-based (latest queued response is consumed first).
- Use deterministic IDs in tests so `inspectResponse(id)` can be used for assertions.
