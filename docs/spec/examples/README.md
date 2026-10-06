# Application knowledge bundle examples

These fixtures implement the record shapes in
[draft 1](../README.md). Names, source revisions, timestamps, payloads and test
outcomes are fictional. The hashes are real SHA-256 digests of the included files.

| Bundle | Contents |
| --- | --- |
| [Minimal](minimal/index.md) | An application declaration with no APIs or evidence. Demonstrates valid empty inventories and explicit unknown source provenance. |
| [Tested API](tested-api/index.md) | A supplied OpenAPI contract with an imported schema, stable API/operation/test pages, one selected test-runner result and a retained response payload. |

Each directory is a complete bundle root. Copying it elsewhere preserves all
internal links. Its `bundle.json` registers the required files, and each primary
page has a stable subject binding. The tested example uses `capture_state: partial`
because a payload and runner outcome do not constitute a complete HTTP exchange.
It deliberately makes no coverage or provider-compatibility claim.

The minimal example needs core support. The tested example additionally declares
`test-result.v1`. A full implementation needs the filesystem, cross-reference,
publication and change-diff checks in [conformance.md](../conformance.md), beyond
JSON Schema validation of these fixtures.

## Expected changes when implementing publication

- Adding a second test adds its page and evidence directory, and updates the
  manifest, publication selection and relevant navigation. Existing subject paths
  remain unchanged.
- Changing the payment schema updates
  `contracts/payments-v1/schemas/payment.json`, its package manifest and root hash
  registry. New evidence must bind the actual new contract. Retained evidence
  cannot simply have its old contract hash replaced.
- A new execution can replace `evidence/tests/create-payment/primary/attempt.json`
  at the same path, with new execution identity and provenance. Exact references
  to the old observation must pin the old publication and file digest.
- Renaming the test's display title does not rename its `create-payment` directory.

The fixture directories contain only bundle content. This README and the schemas
are supporting specification material outside those roots.
