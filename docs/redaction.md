# Retained-data redaction

Set a minimum policy once in `.wiretap.yaml`. It applies to every captured test
and cannot be disabled by a test supplying an empty `redactions` list. SDK/API
rules add to the project policy.

```yaml
version: 1
sut:
  url: http://localhost:8080
  schema: api.yaml
redactions:
  - {field: request.headers.authorization, rule: remove}
  - {field: request.headers.cookie, rule: remove}
  - {field: response.headers.set-cookie, rule: remove}
  - {field: request.query.access_token, rule: remove}
  - {field: request.url.userinfo, rule: remove}
  - {field: response.body.credentials.token, rule: mask}
```

Redaction changes retained evidence after live contract validation. Requests and
responses on the network keep their original bytes. Validation still fails when
the original traffic violates its supplied contract; diagnostics for a redacted
message omit original values, including copies in assertions and metadata.

`request.query.<name>` and `request.url.query.<name>` select all occurrences of
that query parameter. Use `*` to select every query parameter. URL selectors
also support `userinfo`, `fragment`, and `path`; `request.url` omits the entire
retained URL. Query names are case-sensitive. Header names are case-insensitive.
`mask` uses `****`, or an explicit `replacement`; `remove` deletes the selected
value. A query string that cannot be parsed is omitted when a query rule applies.
Rules are validated at setup so unsupported policy names do not silently pass.

HTTP body rules select JSON properties and array indices, including `[*]`.
`request.body`/`response.body` omit the entire retained body. When a field rule
cannot safely inspect a binary, malformed (including duplicate JSON keys), or truncated body, all retained body
representations are omitted and marked `omitted: true` and `redacted: true`.
The existing `truncated` flag independently records capture truncation. A valid
JSON body with no matching field remains available. A changed JSON representation is marked `redacted: true`. Removing one field also
removes its alternate decoded representation. These limits remain visible in
coverage: changed or unavailable messages cannot establish original conditions.

The existing gRPC metadata, trailer, message and rich-status selectors remain
supported. Redaction removes original protobuf/envelope copies when needed.
Rules do not sanitize arbitrary test names, user metadata, supplied contract
examples, or source/build labels; keep secrets out of those inputs.
