# Check consumer and provider assumptions

Use `wiretap check` to compare a consumer's captured mock interactions with the
provider's own captured tests. The check works offline from complete Wiretap
archives and their Postman collections. It does not start servers, replay
requests, execute collection scripts or generate tests.

**A pass means every selected captured mock exchange has at least one complete,
passing provider test exchange that supports that concrete request and response.**
The scope is the supplied examples and recorded values.

- [What a passing result establishes](#what-a-passing-result-establishes)
- [Issues the check will find](#issues-the-check-will-find)
- [What the check will not establish](#what-the-check-will-not-establish)
- [Keep the evidence together](#keep-the-evidence-together)
- [Select the dependency and provider](#select-the-dependency-and-provider)
- [What agreement means](#what-agreement-means)
- [Interpreting results](#interpreting-results)
- [Use the result in CI](#use-the-result-in-ci)

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

## What a passing result establishes

For each captured call to the selected mock, Wiretap:

1. Requires a complete consumer exchange from a passing test attempt.
2. Finds complete passing provider exchanges with the same normalized request.
3. Looks for the mocked response in one of those **same exchanges**.
4. For native gRPC, also checks that the observed request and response bytes
   can be understood using the opposite side's frozen protobuf descriptors.
5. Checks that the supplied Postman examples agree with their own archives.

All selected consumer exchanges must succeed. An empty selection cannot pass.
Provider tests need not share the consumer's test names, files or attempt IDs.
The report links the actual consumer and provider attempts used for each match.

This is an **at least one matching example** rule. Suppose passing provider
tests record the following for the same visible request:

| Provider fixture | Request | Response |
| --- | --- | --- |
| Order exists | `GET /orders/order-1` | `200`, with the order |
| Order was deleted | `GET /orders/order-1` | `404`, with an error |

A consumer mock returning the first response can pass because that response
has supporting provider evidence. The additional `404` does not invalidate
that match. To check the consumer's handling of `404`, capture a consumer test
that exercises that mock response as well. A pass does not mean the provider
always returns the mocked response for that request.

## Issues the check will find

The outcome depends on where the evidence stops agreeing. A different request
usually produces **uncovered**: there is no qualifying provider example for
that concrete scenario. A different response produces **mismatch** when the
provider has tested the request but no matching exchange returns the mocked
response. Evidence that cannot support a comparison produces **unverified**
or an input error, rather than a compatibility claim.

### HTTP request and response disagreements

For response rows below, assume the normalized request is identical and no
other qualifying provider exchange supplies the mocked response.

| Issue | Example | Result |
| --- | --- | --- |
| Missing operation or concrete path | Consumer calls `POST /reservations`; provider tests never record it. | Uncovered. |
| Different path or query values | Consumer uses `/stock/widget-1?warehouse=melbourne`; provider only tests `warehouse=sydney`. | Uncovered, even if both values are schema-valid. |
| Different request payload | Consumer sends `{"quantity":"3"}`; provider examples send `{"quantity":3}`. | Uncovered; request values and types differ. |
| Missing request parameter, header or body | Consumer sends no tenant header or omits a body present in every provider example. | Uncovered. |
| Wrong response status | Mock returns `201`; matching provider requests return `200`. | Mismatch. |
| Missing, renamed or added response field | Mock has `quantity`; provider has `availableQuantity`, omits it, or adds another field. | Mismatch under whole-response equality. |
| Wrong field type | Mock returns `{"quantity":3}`; provider returns `{"quantity":"3"}`. | Mismatch. |
| Wrong boolean, enum-like value or other literal | Mock says `available: true` or `status: "ready"`; provider returns `false` or `"pending"`. | Mismatch. |
| Null, absence and empty-body differences | Mock omits `note`; provider returns `note: null`, or one side has an absent body and the other a present empty body. | Mismatch. |
| Array differences | Mock returns `["a","b"]`; provider returns `["b","a"]`, fewer elements or different values. | Mismatch. |
| Application response header or trailer differs | Mock supplies `X-Stock-Unit: items`; provider supplies `boxes` or no such header. | Mismatch unless explicitly ignored. |
| Non-JSON bytes or malformed JSON differ | Retained bytes differ in a non-JSON response or a deliberately malformed JSON example. | Mismatch; those bodies compare byte for byte. |

HTTP error responses can be valid supporting examples. A passing test that
intentionally receives `400`, `404` or `500` can support a mock returning the
same complete response. A connection failure recorded as a transport error
cannot establish agreement merely because Wiretap also recorded a `502`.

### Native gRPC disagreements

| Issue | Example | Result |
| --- | --- | --- |
| Missing procedure or request scenario | Consumer calls `inventory.v1.Stock/Reserve` with values absent from provider tests. | Uncovered. |
| Request message count, order, type or stream kind differs | Consumer sends two ordered client-stream messages; provider only records one, or a different stream kind. | Uncovered. |
| Wrong terminal application status | Mock returns gRPC `OK`; a matching provider exchange returns `NOT_FOUND`. | Mismatch if no matching `OK` example exists. HTTP/2 `200` alone is insufficient. |
| Response messages differ | A server-stream mock drops a message, changes its value, or returns messages in another order. | Mismatch. |
| Application metadata or trailers differ | Mock omits business metadata or supplies a different value. | Mismatch unless that metadata field is ignored. |
| Rich-error details differ | Mock supplies different retained error details. | Mismatch; header exclusions do not remove the separately recorded rich-error evidence. |
| Observed message interleaving differs | A bidirectional conversation records a different order of complete request and response messages. | Mismatch under the recorded conversation policy. |
| JSON looks equal but exercised wire fields are incompatible | Both examples show `quantity: 3`, but that value uses a different protobuf field number. | Wire mismatch when cross-decoding cannot reproduce the opposite side's recorded value. |
| Source bytes contain fields unknown to the target descriptor | An observed message includes an unknown nested field or an unpacked `Any` the target cannot understand. | Conservative wire mismatch. |

Nonzero terminal gRPC statuses can support intentional negative tests when
the test passed and the request, response and terminal status were captured
completely. An incomplete deadline or cancellation capture is not promoted
to complete evidence just because its test expected cancellation.

### Missing or unsuitable evidence and inputs

| Issue | How it is handled |
| --- | --- |
| Selected dependency has no captured calls | Unverified; zero assumptions cannot certify compatibility. |
| Provider never exercised the consumer's scenario | Uncovered, even if the provider declares the operation in a contract. |
| Only failed, skipped or incomplete provider attempts cover it | Those attempts are excluded; the scenario remains uncovered unless another complete passing attempt matches. |
| Selected consumer attempt failed or its capture is incomplete | Its exchange is unverified. Another successful consumer attempt does not erase it. |
| Either run is open, interrupted or has pending attempts | Unverified at run level. |
| Body/message bytes are truncated, omitted, legacy-redacted or unavailable | Cannot support a verified exchange. Consumer evidence is unverified; provider evidence is excluded. |
| Selected interaction protocol is unsupported | Unverified for the selected consumer interaction; it cannot silently disappear behind supported HTTP calls. |
| Provider target is declared as a mock or has unknown kind | Unverified; provider evidence must declare a service target. |
| Declared provider application, API or version does not agree | Unverified; an unknown required version is not treated as a match. |
| Archive hashes, references or format are invalid | Input error, with no passing verdict. |
| Selected Postman examples are missing, edited, stale or incomplete | Input error when no full matching projection is supplied. |

Failed provider attempts do not automatically fail the assumption check if
complete passing evidence supports every selected consumer example. Keep the
provider suite's own exit status as a separate CI gate.

### Worked example: an unrealistic inventory response

Assume both suites record this request with otherwise equal significant
headers and body presence:

```http
GET /stock/widget-1?warehouse=melbourne
Accept: application/json
```

The consumer's mock returns:

```json
{"sku":"widget-1","available":true,"quantity":3}
```

The provider test returns the same status and headers, but its response is:

```json
{"sku":"widget-1","available":true,"quantity":"3"}
```

The result is `mismatch`, with a difference path such as
`response/body/value/quantity`. The provider covered the request, but the mock's
response is not supported by that exchange. Correct the mock or investigate
the provider's changed behavior, rerun the affected suites and compare the
new evidence.

If the provider only tested `warehouse=sydney`, the result would instead be
`uncovered`. That says to add or align a provider test for the Melbourne
scenario; it does not establish that the running provider rejects Melbourne.

### Worked example: isolated facts cannot be combined

For the same normalized request, suppose the consumer mocks:

```json
{"available":true,"status":"ready"}
```

Provider test A returns `{"available":true,"status":"pending"}` and provider
test B returns `{"available":false,"status":"ready"}`. Both individual
field values occurred somewhere, but no exchange supplies the consumer's
combination. The check fails with a mismatch. Coverage percentages or separate
field hits cannot substitute for the missing paired example.

## What the check will not establish

### That every request, state or outcome is compatible

Only recorded concrete examples participate. A supported example does not
generalize to other IDs, quantities, tenants, enum values, boundaries, error
branches or future API versions. An operation can have one supported example
and still have many untested cases. Use the separate
[contract coverage report](contract-coverage-rules.md) to inspect declared
cases and their measurement limits.

One matching provider response is sufficient for each consumer exchange.
The checker does not flag all alternative provider responses, infer
nondeterminism, or require a consumer to handle every possible outcome.

### That the fixtures represent the same hidden state

Matching uses visible boundary requests and responses. It does not join test
names, identity variants, database fixtures, feature flags, the current clock,
prior application calls or other hidden state across repositories.

For example, a provider test may return an order only because its database was
seeded, while the consumer implicitly assumes the order always exists. A
matching captured example can pass without proving that broader assumption.
Put meaningful scenario inputs into the visible request where applicable,
and keep corresponding test-state assumptions explicit in the test suites.

Different synthetic fixture values also cause deliberate failures. Consumer
`widget-1` and provider `widget-2` are different concrete paths or payloads;
the checker does not infer that they are interchangeable placeholders.
There is no wildcard-ID, payload-field exclusion or shape-only matching mode.

### That a response difference actually breaks the consumer

Wiretap does not observe which response fields application code reads. It
compares the whole captured response, including fields the consumer might
ignore. A provider adding `{"updated_at":"..."}` can cause a mismatch even
when the consumer would safely ignore that field.

Treat the failure as an example disagreement to investigate. It is not proof
of a runtime breaking change. Update unrealistic or outdated mocks when that
is the right resolution, then regenerate evidence; editing a collection to
hide the difference does not change the raw capture.

### That the API matches its specification or its business meaning

The HTTP comparison does not diff OpenAPI revisions, generalize route templates,
or prove declared requiredness, enum membership, numeric bounds or every other
schema rule. It does not use recorded schema-validation outcomes as its pass
gate. Two passing test suites can agree on an example that violates a supplied
contract. Deliberately invalid requests paired with expected error responses
can also be legitimate comparison examples.

Keep schema validation and coverage checks alongside this command. The
assumption check can compare HTTP captures whose schema was explicitly omitted;
that does not create knowledge of an API contract.

For gRPC, cross-decoding checks the **observed message values** against the
opposite frozen descriptor. It is not an exhaustive protobuf schema-evolution
analysis. A changed field that neither suite exercises can remain undetected.
Conversely, the verifier rejects observed unknown fields even where a
particular protobuf client would tolerate them; that is a conservative failure.

Matching values also do not prove business semantics. Two suites can both
record `price: 100` while interpreting the units differently if no visible
field distinguishes the units.

### That workflows, retries or call counts are correct

Each HTTP exchange or RPC is checked independently. The command does not
verify prerequisites, ordering across calls, transaction boundaries, side
effects, idempotency, retry policy or the number of times a dependency should
have been called. Multiple consumer exchanges can reuse the same supporting
provider exchange.

Within one native streaming gRPC call, retained message order and observed
request/response interleaving are compared. This does not establish timings,
lifecycle-event equivalence or workflows across separate RPCs.

All retained attempts are considered; the checker does not select only the
last retry. A failed consumer attempt with selected mock traffic remains
unverified even if a later retry passed. Failed provider retries cannot
support a match, while a complete passing retry can.

### That unexecuted mocks or uncaptured traffic are covered

The inputs contain calls that actually crossed Wiretap. The command does not
discover source-code stub definitions, unexercised consumer code paths, or
traffic that bypassed capture. A mock configured with ten responses but
exercised once contributes the one observed exchange. Adding uncaptured
examples to a selected Postman collection does not extend the comparison;
the edited collection will no longer match its recorded export.

Zero calls to the selected mock fail. However, one captured call can pass
without detecting that an expected second call never happened; assert those
requirements in the application tests. Capture each dependency and check it
explicitly. One invocation does not check every dependency or every consumer
of a provider.

### That a live deployment, security policy or performance is correct

The command performs no live requests. It does not establish deployment
routing, DNS, TLS/certificate behavior, HTTP-version compatibility, latency,
load capacity, concurrency or deadline behavior. Endpoint scheme, host and
port are excluded from cross-archive HTTP request matching. Differences in
ignored headers, including `grpc-timeout`, are outside that comparison.

Authorization and business headers are significant unless ignored, but
matching dummy credentials does not test authentication or authorization
policy. Tests must exercise the appropriate allowed and denied cases.

The provider identity, target kind `service`, version and source metadata are
declarations in the capture. Integrity hashes detect inconsistent or changed
evidence; they do not authenticate its publisher or prove that the declared
provider code was executed. Without a mapping, explicit inputs establish the
pairing chosen by the caller. Choose and retain evidence from trusted builds.

### That Postman scripts or replay configuration work

Collection verification checks recorded example fidelity. It does not run
pre-request scripts, test scripts, custom assertions, variable substitution,
cookie handling or redirect behavior. Editing scripts, labels or descriptions
does not establish a new assumption and may leave the verdict unchanged.
Unsupported authentication transformations and external request-body files
are rejected rather than evaluated.

HTTP URLs in a supplied collection must agree with that collection's own
archive; origin normalization applies between the two archives. Native gRPC
replay URLs, schema locations and connection settings are allowed to differ
without proving those settings are usable. Run Postman separately when replay
behavior is the question.

Collection directories are searched for full matching projections. Unmatched
files can be ignored as other boundaries or unrelated exports. A valid copy
beside a stale or malformed copy can therefore still satisfy the selection.
Use explicit file flags to require validation of particular files; this
command is not a directory-wide export cleanup check.

### That other protocols receive native semantic checking

Native protocol comparison currently covers HTTP exchanges and gRPC. Selected
event interactions such as Kafka messages are unverified. GraphQL sent as an
ordinary captured HTTP exchange can be compared as an HTTP example, but this
does not analyze a GraphQL schema, operation semantics or subscriptions.
Opaque, encrypted or unavailable payloads cannot supply missing observations.

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
names are case-insensitive, while method tokens and escaped paths retain their
case and values. Query key order is normalized, while repeated values retain
their order. Header values and repeated-header order remain significant unless
the header is ignored; semantically equivalent spellings are not inferred.
Valid JSON body bytes receive JSON normalization regardless of HTTP
`Content-Type`; the content-type header itself still participates in header
comparison.
Request origin addresses are excluded, while paths are retained.
gRPC comparisons also retain stream shape, message order, observed direction
interleaving and rich-error details.
For a prospective gRPC match, Wiretap also decodes the consumer's request bytes
with the provider's frozen descriptor and the provider's response bytes with
the consumer's descriptor. Equal JSON examples cannot hide incompatible field
numbers or wire types.

Requests and responses stay paired. A request from one provider test and a
response from another cannot jointly satisfy a mock. When the provider tested
the same request but no qualifying exchange returned the mocked status,
headers and value together, the result is a mismatch. When no qualifying
provider request exists, it is uncovered.

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
For gRPC, excluding a header such as `grpc-status` or `grpc-status-details-bin`
does not exclude the separately recorded terminal status, status message or
rich-error details.

A passing check means the supplied provider tests support these captured
examples. It does not prove that every possible request works, that the tests
asserted every captured field, or that a live deployment behaves identically.
Wiretap cannot infer which response fields the consumer actually read, so it
checks the recorded response rather than guessing a smaller required subset.

## Interpreting results

| Result | What the evidence establishes | Next action |
| --- | --- | --- |
| Verified | At least one complete passing provider exchange supports this consumer request and response. | Retain the referenced archives; keep testing additional scenarios separately. |
| Uncovered | No qualifying provider request matches the concrete normalized consumer request. | Inspect exact paths, query values, payloads and significant headers; add the provider case or align fixtures. |
| Mismatch | Provider examples cover the request but none supports the mocked response, or observed gRPC bytes fail the cross-contract check. | Investigate the linked response/wire difference, then correct the mock, provider or intended scenario. |
| Unverified | The selection or evidence cannot establish agreement. | Resolve the capture, identity, target-kind or test-outcome problem and recapture as needed. |
| Input error | Supplied archives, collections or flags cannot form a valid comparison. | Read stderr, supply intact matching inputs and rerun. |

Uncovered is a test-evidence gap, not a statement that the provider would reject
the request. Mismatch is an observed example disagreement, not an automatic
determination that one repository is wrong. Unverified is a failure to establish
compatibility; it is not treated as a pass.

### Diagnostic codes

| Code | Meaning |
| --- | --- |
| `provider_request_uncovered` | No eligible provider exchange matches this request scenario. |
| `provider_response_mismatch` | Candidate provider exchanges do not supply the mocked response. |
| `provider_wire_mismatch` | A prospective gRPC example match fails decoding under the opposite descriptor. |
| `consumer_evidence_unavailable` | A selected consumer exchange is failed, incomplete, unsupported or otherwise unusable. |
| `consumer_run_incomplete` / `provider_run_incomplete` | The archive is not complete or has pending attempts. |
| `no_consumer_assumptions` | No recorded exchange was selected for the mock. |
| `provider_not_service` | The provider capture does not declare a service target. |
| `provider_identity_mismatch` | Declared application/API mapping conflicts with the supplied provider. |
| `provider_version_unverified` | A declared provider version is missing or different in recorded `sut_source.version`. |

Archive/collection input errors are reported on stderr and do not produce
these finding codes or a new JSON result. An existing `--output` file is left
unchanged when input validation fails. Do not treat an old report as the
result of the current command; use its exit status as well.

### Read the exact evidence

An abbreviated JSON result for the inventory type mismatch above could contain:

```json
{
  "report_version": "1.0",
  "rules_version": "wiretap-assumptions-1",
  "mode": "consumer",
  "scope": "observed_mock_examples",
  "compatible": false,
  "counts": {
    "assumptions": 1,
    "verified": 0,
    "uncovered": 0,
    "mismatched": 1,
    "unverified": 0
  },
  "findings": [
    {
      "kind": "mismatch",
      "code": "provider_response_mismatch",
      "operation": "GET /stock/widget-1",
      "message": "provider tests cover the request, but none returns the mocked response",
      "differences": ["response/body/value/quantity"],
      "consumer": {
        "run_id": "orders-run",
        "attempt_id": "consumer-attempt",
        "test_name": "reserves available inventory",
        "flow_id": "inventory-call",
        "evidence_path": "tests/consumer-attempt.json#/capture/traffic/0",
        "test_outcome": "pass"
      },
      "provider": [
        {
          "run_id": "inventory-run",
          "attempt_id": "provider-attempt",
          "test_name": "returns stock for widget-1",
          "flow_id": "stock-request",
          "evidence_path": "tests/provider-attempt.json#/capture/traffic/0",
          "test_outcome": "pass"
        }
      ]
    }
  ]
}
```

The full result also identifies both source archives, manifest digests, input
collections, binding and ignored-header policy. An `evidence_path` is relative
to its source archive; the fragment selects the recorded flow in the attempt
file. Consumer and provider attempt IDs need not match each other.

`matches` records the provider witness chosen for each verified consumer
exchange. `excluded_provider_evidence` explains why other provider exchanges
could not serve as witnesses. Its presence alone does not fail a comparison.

The report is a diagnostic summary rather than an exhaustive diff. A finding
includes at most ten provider references and twenty differing paths. For a
response mismatch, paths compare the consumer response with the first candidate
provider response; they do not identify the closest candidate or enumerate
every disagreement across every candidate. Open the referenced captures for
the complete retained data.

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
| `1` | An assumption mismatches, is uncovered, or cannot be verified; operational failures can also return `1` with a stderr diagnostic. |
| `2` | Inputs or command flags are invalid. |

Use the command's exit status as the CI gate. For capture and export setup, see
[capture artifacts and exports](artifacts.md). For the separate measurement of
declared contract cases, see [contract coverage rules](contract-coverage-rules.md).
