# Postman compatibility checks

Run from the repository root with Node 22+ and Go available:

```sh
npm ci --prefix tests/postman-compat
npm audit --prefix tests/postman-compat --audit-level=low
npm test --prefix tests/postman-compat
```

The harness calls Wiretap's Go test fixture writer. It builds verified raw
archives, runs the actual exporter, and reads the generated collections from a
temporary directory. JSON fixtures are never handwritten substitutes for exports.

HTTP Collection v2.1 is validated by Ajv against the pinned, unmodified
[official v2.1 JSON schema](https://schema.postman.com/collection/json/v2.1.0/draft-07/collection.json).
Concrete fixture expectations cover request methods/encoded URLs, repeated
headers, inline binary request data, saved binary responses, original requests,
status assertions, folder identity and replay settings. The runner smoke and
external Jest example use the same schema validator and compare exported HTTP
content with the archive's recorded traffic and retained payload files.

Native gRPC output is validated by Ajv against the pinned, unmodified
[official v3 JSON schema](https://schema.postman.com/collection/json/v3.0.0/draft-07/collection.json).
Concrete fixture expectations cover all four RPC shapes,
ordered messages, metadata/trailers, binary values, rich errors, source schema and
endpoint references, and retries.

Compatibility acceptance is automated: these checks prove the exported document
shape and preserved content, not execution inside the Postman application.
On 24 October 2026, the available Postman 12.30 desktop scratchpad blocked
collection import pending sign-in, so imported-collection invocation was not
verified. The project does not claim desktop replay interoperability from
JSON/schema validation alone.

Ajv is exact-pinned and this package is private test tooling. It is not a runtime
dependency or part of the JavaScript SDK package. The unpatched
`postman-collection` SDK is no longer installed: version 5.3.1 pins vulnerable
Faker and UUID versions, and a patched Faker override is API incompatible.
These checks establish official schema conformance and preservation of captured
content; they do not exercise the Postman SDK parser or desktop replay.
