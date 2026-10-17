# Postman schema fixture

`postman-v3.0.0.schema.json` is the unmodified official Postman Collection
Format v3.0.0 draft-07 schema, retrieved on 2026-10-17 from:

https://schema.postman.com/collection/json/v3.0.0/draft-07/collection.json

It is stored locally so gRPC export conformance tests run without a network.
The schema uses `$kind: collection` for both the root and nested folders.
