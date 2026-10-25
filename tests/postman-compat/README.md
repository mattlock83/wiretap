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

These automated checks verify the exported document shape and preserved
content. They do not exercise the Postman SDK parser or import and execution
inside the Postman application. See [supported behavior and verification
limits](../../docs/support.md).

Ajv is exact-pinned and this package is private test tooling. It is not a runtime
dependency or part of the JavaScript SDK package.
