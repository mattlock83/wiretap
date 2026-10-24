# Postman compatibility checks

Run from the repository root with Node 22+ and Go available:

```sh
npm ci --prefix tests/postman-compat
npm test --prefix tests/postman-compat
```

The harness calls Wiretap's Go test fixture writer. It builds verified raw
archives, runs the actual exporter, and reads the generated collections from a
temporary directory. JSON fixtures are never handwritten substitutes for exports.

HTTP Collection v2.1 is parsed and re-exported by the official
[`postman-collection` SDK](https://github.com/postmanlabs/postman-collection)
5.3.1. Assertions cover request methods/encoded URLs, repeated headers, inline
binary request data, saved binary responses, original requests, status assertions,
folder identity and replay settings.

The SDK's published compatibility table supports Collection v2.1, and its
source package has no native v3 gRPC object model. `@postman/collection` is not a
published npm package. We therefore do **not** run gRPC v3 through that SDK or
claim a Postman import/desktop replay check. Native gRPC output is validated by
Ajv against the pinned, unmodified
[official v3 JSON schema](https://schema.postman.com/collection/json/v3.0.0/draft-07/collection.json),
then round-tripped losslessly as JSON. Assertions cover all four RPC shapes,
ordered messages, metadata/trailers, binary values, rich errors, source schema and
endpoint references, and retries.

Compatibility acceptance is automated: these checks prove the exported document
shape and preserved content, not execution inside the Postman application.
On 24 October 2026, the available Postman 12.30 desktop scratchpad blocked
collection import pending sign-in, so imported-collection invocation was not
verified. The project does not claim desktop replay interoperability from
JSON/schema validation alone.

Both dependencies are exact-pinned and this package is private test tooling. It
is not a runtime dependency or part of the JavaScript SDK package. Upstream
SDK5.3.1 currently includes advisory-affected faker5.5.3 and uuid8.3.2. These tests
only parse locally generated fixtures; they never call faker helpers.fake(),
perform dynamic variable interpolation, or call UUID buffer offset APIs. Keep
reviewing upstream fixes rather than silently substituting an incompatible SDK.
