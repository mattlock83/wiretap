# Mock Template File Format

`queue-response` supports response templates in `response.body` and `response.headers`.

Templates can be inline strings or loaded via `response.body_file`.

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
- `docs/templates/payment-complex.tmpl.json`

## Complex Template Example

`docs/templates/payment-complex.tmpl.json` demonstrates:
- Sprig string/regex/hash helpers
- Sprig object composition (`dict`, `list`)
- strict JSON helpers (`mustFromJSON`, `mustToJSON`, `mustToPrettyJSON`)
- time helpers (`nowUTC`, `nowPlus`, `formatTime`)
- gofakeit usage (`Name`, `Email`, `phoneFormatted`)
- CEL projection (`celJSON`)

## Notes

- Templates are rendered per matched request.
- Queue matching is stack-based (latest queued response is consumed first).
- Use deterministic IDs in tests so `getResponse(id)` can be used for assertions.
