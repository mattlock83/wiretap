# Check consumer and provider assumptions

Use `wiretap check` to compare a consumer's captured mock interactions with the
provider's own captured tests. The check works offline from complete Wiretap
archives and their Postman collections. It does not start servers, replay
requests, execute collection scripts or generate tests.

Run it after both suites have finalized their archives and Postman exports:

```sh
wiretap check consumer \
  --consumer ./orders/artifacts/<run-id> \
  --provider ./inventory/artifacts/<run-id> \
  --dependency inventory
```

This asks: **Do the inventory mocks used by these orders tests agree with
examples covered by the inventory provider's tests?** Every observed request
scenario to the selected mock needs a matching request and compatible response
from the provider's complete passing tests. An untested request scenario fails
the check, even if it would be valid under the provider's schema.

From the provider repository, use the other viewpoint:

```sh
wiretap check provider \
  --provider ./inventory/artifacts/<run-id> \
  --consumer ./orders/artifacts/<run-id> \
  --dependency inventory
```

This asks: **Do these provider test results still support this consumer's
mocks?** The relation and required evidence are the same in both commands;
`provider` and `consumer` identify the local viewpoint. Neither command requires
the provider to exercise scenarios the consumer has never assumed. Run the
command once for each consumer archive you want to protect.

## Keep the evidence together

`--consumer` and `--provider` each accept a run directory or its `bundle.json`.
Keep the complete archive, including `tests/`, `contracts/` and `payloads/`.
Coverage reports alone are insufficient: equal coverage conditions do not prove
that the same request received the same response.

Collections default to each archive's `exports/postman/` directory. To select
collections stored elsewhere, provide files or directories explicitly:

```sh
wiretap check consumer \
  --consumer ./consumer-run/bundle.json \
  --provider ./provider-run/bundle.json \
  --dependency inventory \
  --consumer-collection ./collections/inventory.postman_collection.json \
  --provider-collection ./provider-collections
```

Repeat `--consumer-collection` or `--provider-collection` for multiple inputs.
Directories are searched recursively for `*.postman_collection.json` and may
contain collections for other boundaries.
Wiretap verifies the collections against projections regenerated from their
archives, including attempt IDs, requests, responses and application headers.
A collection edited to change a payload or status cannot override the captured
evidence. Native gRPC replay URL and source-schema settings may differ because
they configure replay rather than the recorded conversation.

If exports are missing, generate them from the exact archive:

```sh
wiretap export --bundle ./consumer-run --format postman --output-dir ./consumer-run/exports/postman
wiretap export --bundle ./provider-run --format postman --output-dir ./provider-run/exports/postman
```

Both runs must be complete with no pending attempts. Each selected consumer
example must come from a complete passing attempt and contain supported,
complete evidence. A failed or incomplete provider attempt cannot establish a
match, but another complete passing provider attempt can. Excluded provider
exchanges remain visible in the result. Omitting an incomplete interaction from
a collection does not turn it into supporting evidence.

An interrupted run, an unverifiable provider identity, an empty consumer
selection or entirely unusable consumer evidence fails before collections are
required. For an otherwise eligible comparison, missing, invalid or stale
collections are invalid inputs.

## Select the dependency and provider

`--dependency` is the consumer's local mock ID, such as `inventory` in
`.wiretap.yaml`. It selects captured traffic whose target kind is `mock`.
Provider evidence must come from the selected provider's provided API with
target kind `service`, rather than from another mock.

Stable identities make the relationship clearer across repositories:

```yaml
# Consumer configuration
application: {id: orders}
provider_mappings:
  inventory:
    application_id: inventory-service
    api_id: inventory-http
dependencies:
  inventory:
    kind: mock
    url: ${INVENTORY_MOCK_URL}
    schema: schemas/inventory.openapi.yaml
```

```yaml
# Provider configuration
application: {id: inventory-service}
sut:
  id: inventory-http
  kind: service
  url: ${SUT_BASE_URL}
  schema: schemas/inventory.openapi.yaml
```

Declared mappings must agree with the supplied provider archive. Wiretap infers
a selection only when it is unambiguous; otherwise supply `--dependency`.
Choose the intended application and test revisions explicitly. Wiretap checks
the files you supplied and does not discover the latest build of another repo.
If a mapping declares `version`, the provider's captured `sut_source.version`
must establish that version; a missing version leaves the check unverified.

## What agreement means

The check compares concrete examples. A consumer request needs a provider
request with the same relevant method, path, parameters, headers and body, or
the same gRPC procedure and ordered request messages. That provider exchange
must also have the response the mock supplied: HTTP status or gRPC terminal
status, application headers or metadata, and body or ordered messages.

JSON object property order and equivalent numeric notation are immaterial:
`100`, `100.0` and `1e2` represent the same number. JSON values remain significant;
arrays retain order, and `null` differs from an absent field. Different synthetic
request IDs or payload values are different request scenarios. They are
reported as uncovered unless the provider has tested those concrete values.
There is no inference from a passing schema validation or an equal coverage
fingerprint to agreement on arbitrary values.

Absent and present-empty bodies differ. Non-JSON bodies, including invalid or
duplicate-key JSON used in negative tests, compare byte for byte. HTTP header
names are case-insensitive; query parameter names and values remain significant.
Request origin addresses are excluded, while paths are retained.
gRPC comparisons also retain stream shape, message order, observed direction
interleaving and rich-error details.
For a prospective gRPC match, Wiretap also decodes the consumer's request bytes
with the provider's frozen descriptor and the provider's response bytes with
the consumer's descriptor. Equal JSON examples cannot hide incompatible field
numbers or wire types.

Requests and responses stay paired. A request from one provider test and a
response from another cannot jointly satisfy a mock. When the provider tested
the same request but returned a different status, header or value, the result
is a mismatch. When no qualifying provider request exists, it is uncovered.

Capture origins and a narrow set of transport, tracing and client/server headers
are ignored because test addresses and capture plumbing vary between suites.
The defaults are `accept-encoding`, `baggage`, `connection`, `content-length`,
`date`, `grpc-accept-encoding`, `grpc-encoding`, `grpc-timeout`, `host`,
`keep-alive`, `postman-token`, `proxy-connection`, `server`, `te`, `trailer`,
`traceparent`, `tracestate`, `transfer-encoding`, `upgrade`, `user-agent` and
`via`. Headers nominated by `Connection` are also excluded. `Content-Encoding`
is excluded when the retained body has already been decoded. Authorization
and arbitrary `x-*` headers remain significant. Additional volatile headers can
be excluded explicitly:

```sh
wiretap check consumer --consumer ./consumer-run --provider ./provider-run \
  --dependency inventory --ignore-header x-request-id
```

Repeat `--ignore-header` as needed. Ignoring a header removes it from both
request and response comparison; use this only when that value is irrelevant to
the assumption being checked. Payload fields are never silently ignored.

A passing check means the supplied provider tests support these captured
examples. It does not prove that every possible request works, that the tests
asserted every captured field, or that a live deployment behaves identically.
Wiretap cannot infer which response fields the consumer actually read, so it
checks the recorded response rather than guessing a smaller required subset.

## Use the result in CI

Text is the default. For a machine-readable result, select JSON and optionally
save it to a file:

```sh
wiretap check provider --provider ./provider-run --consumer ./consumer-run \
  --dependency inventory --format json --output assumption-check.json
```

The output's parent directory must already exist. Choose a location outside
both input archives and any supplied Postman input directory; an output cannot
replace a collection input.

The JSON result uses `report_version: "1.0"` and
`rules_version: "wiretap-assumptions-1"`. `compatible` is the verdict;
`counts` separates verified, uncovered, mismatched and unverified assumptions.
Unverified counts can also include comparison-level blockers, such as an
interrupted archive or conflicting provider identity.
`matches` and `findings` retain source archive paths and test/flow evidence
references so the result can be traced back to the contributing exchanges.
Keep the archives alongside the result when sharing it. See the
[machine interface](machine-interface.md#assumption-check-results) for envelope
fields.

| Exit status | Meaning |
| --- | --- |
| `0` | All selected mock scenarios have compatible provider evidence. |
| `1` | An assumption mismatches, is uncovered, or cannot be verified from the supplied evidence. |
| `2` | Inputs or command flags are invalid. |

Use the command's exit status as the CI gate. For capture and export setup, see
[capture artifacts and exports](artifacts.md). For the separate measurement of
declared contract cases, see [contract coverage rules](contract-coverage-rules.md).
