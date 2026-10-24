# Postman schema fixtures

`postman-v2.1.0.schema.json` is the unmodified official Postman Collection
Format v2.1.0 draft-07 schema, retrieved on 2026-10-25 from:

https://schema.postman.com/collection/json/v2.1.0/draft-07/collection.json

The HTTP compatibility harness, runner smoke and external Jest example use it
to validate generated collections offline.

`postman-v3.0.0.schema.json` is the unmodified official Postman Collection
Format v3.0.0 draft-07 schema, retrieved on 2026-10-17 from:

https://schema.postman.com/collection/json/v3.0.0/draft-07/collection.json

It is stored locally so gRPC export conformance tests run without a network.
The schema uses `$kind: collection` for both the root and nested folders.
