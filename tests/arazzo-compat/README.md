# Arazzo export validation

The Jest package smoke and external example use `arazzo-validation.cjs` to
validate exports against the pinned official Arazzo schema and independently
compare eligible JSON/REST workflows with raw archive records and payload bytes.

Run from the repository root:

```sh
node scripts/runner-smoke.mjs
node examples/existing-jest/run.mjs artifacts/existing-jest
```

Checks cover attempt and flow identities, request counts, methods, URLs, headers,
JSON request bodies, captured status criteria and local OpenAPI companions.
Non-JSON HTTP exchanges remain in the archive and are excluded from Arazzo.
These checks do not execute workflows or establish runner replay behavior.
