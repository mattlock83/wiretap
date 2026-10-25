# Contract coverage rules

Wiretap measures contract conditions for HTTP/OpenAPI 3.0 and native gRPC messages under the scope
below. The numbered rules define the measurement contract, including how to
report unsupported features and evidence limits. For a guide to reading the
results, start with [capture artifacts and exports](artifacts.md).

## Using contract coverage

Keep using the existing SDK lifecycle and supply your API and dependency specs.
No new HTTP client, test labels, assertions, or SUT instrumentation is required
for this measurement. Ordinary trace propagation/serial capture requirements
still apply. Register specs even for APIs whose operations were not exercised;
Wiretap cannot discover APIs you never register.

`wiretap report --bundle <path>` calculates a JSON report from verified evidence.
Use `--format yaml` for YAML and `--output <file>` to save it. The report
contains a per-spec catalogue under `contract_coverage`: keys, counts, violations,
request/response combinations and exact attempt/flow evidence references.
`wiretap export` supports the same coverage formats. These reports are derived
outputs, not authoritative capture artifacts.

For cross-repository comparison, first resolve a local dependency to its provider
using explicit `provider_mappings` or an external mapping. Comparable condition
keys require the same API identity, supplied contract and `rules_version`; local
`api_id`/`asset_id` labels alone do not establish provider identity. Keys use full SHA-256 digests;
the catalogue retains readable operation, phase, location and condition fields.
`contract_revision` hashes a canonical frozen document graph independently of
repository paths. Equivalent YAML/JSON representations and relocated copies
match; reorganising the document/reference graph can produce a new revision.
`operation_key` remains discoverable across revisions. Test names and capture
origin do not enter shared condition keys.

The JSON separates `origin` (`provided` or `dependency`) and `target_kind`
(`mock`, `service`, or `unknown`). Select the intended source/run revisions from
`bundle.json` before comparing. Covered consumer/mock keys missing from
provider coverage identify evidence gaps, not automatic incompatibilities.
The separate [assumption checker](assumption-checks.md) uses concrete request and
paired response evidence rather than equating coverage keys. It fails when a
selected mock scenario is unsupported by qualifying provider tests. With only
the provider's frozen OpenAPI 3.0 contract, it validates both actual exchanges
under that schema. When both sides have contracts, it additionally compares
the validation rules for the exercised operation, status and media types.

### Coverage and assumption checks answer different questions

Contract coverage asks whether tests exercised a declared case, such as a
nonempty array or a supported enum value. Assumption checks ask whether the
provider tested the consumer's concrete request and produced the response its
mock supplied. Two messages can cover the same conditions while carrying
different application values. Equal coverage percentages or condition keys do
not satisfy an assumption check.

With both HTTP contracts present, the rule check includes declared enum sets,
requiredness and bounds even when the observed value fits both contracts. An enum addition or a relaxed
limit is therefore drift under its conservative equality policy. This does not
establish a runtime break or prove every declared value was tested. Unused
operations, other response statuses and unselected media receive no static
comparison. A schema-free consumer dependency can instead earn
`openapi_3_0_provider_contract` assurance from provider-schema value checks;
there is no independent consumer declaration to diff. This does not
retroactively create consumer contract coverage. With both HTTP schemas absent,
a passing check is explicitly example-only. gRPC compares the selected method's
reachable message/enum declarations alongside observed wire compatibility. Equal full enum sets do not
prove the tests executed every value, and custom validation annotations are
compared as opaque declarations rather than executed predicates.

An uncovered assumption means no qualifying provider example matches that
request; it does not prove the provider would reject it. A response mismatch
means the captured responses disagree under the check's comparison policy; an
extra optional field may trigger one even when the consumer would tolerate it.
The checker cannot identify which fields an application consumed or infer its
assertions. Keep those distinctions when turning results into a release gate.

See [issues the check finds](assumption-checks.md#issues-the-check-will-find),
[what it cannot establish](assumption-checks.md#what-the-check-will-not-establish)
and [interpreting results](assumption-checks.md#interpreting-results) for concrete
examples and the supported assurance claim.

## Current measurement profiles

Each spec records its own `rules_version`. HTTP keeps `wiretap-contract-1`;
adding gRPC does not change existing HTTP keys. Protobuf uses
`wiretap-protobuf-2` for these structural classes:

| Family | Measured cases |
| --- | --- |
| Method | Every selected method, including unary, client-streaming, server-streaming and bidirectional methods. |
| Field presence | Present/absent where Protobuf exposes presence; required fields have no valid absent target. Implicit proto3 scalar defaults have value coverage, not invented presence. |
| Oneof | Each member and unset; synthetic optional-field oneofs are excluded. |
| Enum | Distinct declared numeric values, collapsing aliases. Unknown numbers remain observations without expanding the denominator. |
| Boolean | True and false. |
| Repeated/map | Empty and nonempty, plus supported value conditions at exact item indices/map keys. |
| Nested messages | Supported nonrecursive fields, with recursive/dynamic Any/expansion limits explicit. |

Measurement decodes the verified wire payload against the frozen descriptor.
ProtoJSON is a readable projection, never the measurement input. Keys include
API identity, fully qualified procedure, profile, contract revision, phase and
field-number path; readable field names and exact message indices accompany
observations. One message can cover many conditions and has a combination
fingerprint, without a Cartesian-product denominator.

Custom validation options are preserved but not evaluated by passive structural
coverage. Affected targets
remain unknown and unsupported scope stays visible. Unknown wire fields,
truncated, legacy-redacted or undecodable messages cannot earn payload assurance.
A complete passing RPC with structurally valid messages and a terminal
gRPC status can cover a method, including a legitimate non-OK outcome. An error
without a result does not cover the success response fields. RPC codes are
observations; Protobuf does not declare a mandatory set of error codes.
No invalid-value targets are generated for mocks. Provider and dependency keys
match when API identity, normalized
contract and measurement profile match.

Native streams require verified direction observations: complete framing, EOF,
no omitted messages, valid terminal status and complete structurally valid
payloads. Field observations and combination fingerprints identify the exact
message index. A valid empty streaming direction has no payload conditions;
it can still belong to an exercised method. A non-OK result cannot excuse extra
messages on a non-streaming side. Incomplete, omitted, legacy-redacted or
undecodable streams remain observations without successful credit. Capture limitations yield
partial schema validation, distinct from an actual contract mismatch.

There is no denominator for stream lengths or cross-message business rules.
`wiretap-protobuf-2` changes measurement semantics; compare condition keys only
with the same profile. Descriptor normalization stays `wiretap-protobuf-1`.

The HTTP/OpenAPI rules follow.

Supported: declared HTTP operations, exact/range/default response alternatives,
media alternatives, JSON bodies, scalar path/query/header/cookie parameters and
response headers, typed enums, property/parameter/body presence, nullable values,
types, numeric boundaries/interiors, Unicode string lengths, array lengths,
regex matches, and `oneOf`/`anyOf`/`allOf` branch observations. Required names are
inventoried even without a property schema. Arrays retain every item's instance
path. Defaults are never inserted into observations.

Equal boundaries are deduplicated. Impossible integral interiors and conditions
ruled out by finite enums or local numeric constraints are excluded with reasons.
More complex feasibility under patterns and composed/intersecting schemas remains
unknown until supported by a valid observation. Counts are separate by phase and
condition family; no combined percentage or Cartesian-product denominator is
published.

Explicit limitations:

- OpenAPI 3.1/other dialects, `const`, conditionals, recursive analysis, complex
  parameter serialization and non-JSON body conditions are unsupported.
- Format predicates, object property counts, `uniqueItems`, `not`, `multipleOf`,
  and dynamic or forbidden additional properties have no independent coverage
  measurement.
  Live schema validation remains unchanged. Security assertions are not inferred.
- Nullable schemas combined at the same node with `enum`, `not`, or composition
  remain unsupported because the current validator skips those constraints for
  null. They cannot earn passing message coverage.
- Unsupported schema semantics, skipped validation of empty scalar parameters,
  repeated scalar parameters/headers, or numbers beyond the validator's reliable precision produce unknown measurements. Frozen
  schemas preserve exact numeric literals; examples, metadata and unused schemas
  do not make an otherwise measurable operation unsupported.
- Legacy captures with redaction rules conservatively make that message unknown;
  original values cannot be recovered. Truncation does the same. No placeholder
  or retained prefix can earn body coverage. Unaffected opposite-phase messages
  can still be measured.
- Only complete passing attempts with valid messages earn coverage. Failed or
  incomplete attempts retain observations with explicit `validation`,
  `capture_incomplete`, and `transport_error` evidence fields. Open/interrupted
  runs retain a `complete: false` snapshot and cannot support final run assurance.
- Combination fingerprints describe condition keys and exact instance locations,
  not arbitrary payload equality. No value-normalisation or payload hashing is
  performed. Unsupported combinations are not invented.
- Only supplied contracts define denominators. Targets from different contract
  revisions are measured separately. Capture does not infer expected-negative
  assertions from test names or error responses.

## Purpose

Measure which declared contract conditions existing tests exercised, with a
traceable link from each result to its test name and captured exchange. Support
comparison of consumer/mock observations and provider-test observations without
requiring new scenario labels or taking ownership of test execution.

A spec defines a finite set of coverage classes under these rules. It does not
necessarily define a finite set of possible payloads. Covering the classes is
not proof that every value, business rule, or combination has been tested.

Test names remain the human-readable description of scenario intent. Wiretap
must not infer that an assertion was made solely from a test name or payload.

## 1. Establish the contract and scope first

Coverage targets come from supplied, frozen contracts, independently of traffic.
Declared operations with no exchanges remain visible as uncovered. The report
describes registered APIs only; it cannot discover APIs absent from the run
inventory and captured traffic.

Keep results separate by:

- Stable API identity and operation identity.
- Frozen contract revision and coverage-rule version.
- Request or response, including parameter/message location.
- Response alternative, such as status and media type, and applicable schema branch.
- Evidence origin: provided API, mocked dependency, or real dependency.

For HTTP, use the matched route template and method, not a literal URL containing
sample IDs or an ephemeral hostname. Consumer dependency IDs and provider API
IDs must identify the same API before comparing their operations. Capture
direction is provenance, not part of the shared operation identity.

A missing or explicitly omitted spec means contract coverage is **unknown**.
Observed mock payloads never supply their own coverage denominator.

## 2. Evaluate each exchange against all applicable conditions

One payload may exercise many conditions. Record each applicable observation
independently and retain the complete set observed together in that exchange.
Do not assign a payload to one exclusive scenario bucket.

Use stable schema locations to identify conditions. An illustrative condition is:

```text
API: inventory
Operation: GET /orders/{id}
Message: response / 200 / application/json
Schema location: /properties/items
Condition: array length at inclusive minimum
```

Each target records a stable key, operation, phase, schema location, condition,
applicability, feasibility and coverage state. Violations are separate records
with their own evidence references.

Each observation links to `{run_id, attempt_id, flow_id}`, its message phase,
instance location, contract location, test name, outcome, and evidence quality.
A hash is an index into this description; the description remains available.
`schema_location` describes the expanded operation context (following references),
including logical property locations for required names without value schemas;
it is not always a literal JSON Pointer into the unexpanded source document.

For repeated array items, evaluate every applicable item and retain item-level
locations. Do not examine only the first item. Repeated hits increase the
observation count but cover the same target only once. Request and response hits
must remain distinguishable even if their schemas look identical.

## 3. Derive finite classes from declared constraints

Only feasible classes enter the applicable denominator. The following rules
describe the supported HTTP coverage vocabulary. They do not enumerate every
possible payload.

| Contract feature | Valid coverage classes | Separate violation observations |
| --- | --- | --- |
| Declared operation | Operation exercised | Unknown operation or unmatched route |
| Response status/media alternative | Each declared alternative exercised | Response outside the declared alternatives |
| `enum` | Each permitted value, using its actual JSON type | Value outside the enum |
| Optional property/parameter | Present; absent | Invalid present value is recorded against its constraints |
| Required property/parameter | Present | Absent |
| Nullable value | Null; non-null, where each is permitted | Null when forbidden |
| Inclusive numeric bound | Exactly at each bound; strictly inside the valid range | Below minimum; above maximum |
| Exclusive numeric bound | Inside the valid range; an exclusive endpoint is never a valid boundary hit | At or beyond the excluded endpoint |
| String length | At the effective minimum; interior; at a declared maximum | Too short; too long |
| Array length | At the effective minimum; interior; at a declared maximum | Too few; too many items |
| `pattern` | A matching value | A non-matching value |
| Declared type alternatives | Each allowed type observed | Disallowed type |
| `oneOf`, `anyOf`, `allOf` | Applicable branch observations, as specified below | Branch-selection or branch-validation failure |

Boundary rules:

- Keep only classes that exist. If minimum equals maximum, there is one boundary
  class, not two. An integer range from 0 to 1 has no interior integer class.
- Finite enums and local numeric constraints can exclude impossible targets.
  More complex feasibility remains unknown until supported by a valid observation.
- String and array lengths have an effective minimum of zero when no minimum
  is declared. With no maximum, the interior class is unbounded above. Thus an
  unconstrained array can distinguish empty from nonempty observations.
- Numeric schemas without bounds do not acquire an invented zero, negative,
  or large-value boundary. Type coverage still applies.
- Use the schema dialect's length and numeric semantics. Do not substitute byte
  count for character length or use floating-point rounding to claim equality
  with a decimal boundary.
- For continuous numbers there is no universal "next value" above a boundary.
  An interior hit does not mean "the smallest valid value was tested".
- A regex match covers the declared predicate only. It does not cover every
  alternation, repetition, or possible matching string inside the regex.
- Unsupported formats or keywords must be identified as unsupported, rather
  than counted as checked. Support and validation dialect must be recorded.

The supported-condition set is versioned. Schema validation and condition
measurement have separate scopes. Tuple positions, `uniqueItems`, `contains`,
dynamic-key rules, property dependencies and forbidden additional properties
have no independent coverage targets. Arbitrary property names do not expand
the target inventory.

## 4. Respect applicability, presence, and schema branches

Missing, explicit null, empty string, empty array, and empty object are distinct.
Do not insert schema defaults before measuring what was actually transmitted.
Protocol adapters must preserve the presence distinctions their protocol allows.
Apply direction-specific contract rules, such as OpenAPI `readOnly` and
`writeOnly`, before deriving request or response targets.

When an optional property is absent:

- Credit the property's absent class.
- Mark its child constraints not applicable to this payload.
- Do not credit its length, enum, regex, type, or nested-property classes.
- Keep feasible child targets in the contract-wide denominator. Never-present
  optional fields must not make their untested contents disappear from coverage.

Apply the same principle to empty arrays and nullable values. Empty arrays do
not exercise item constraints. Null does not exercise non-null string or object
constraints. An absent parent does not imply every optional child was separately
exercised as absent.

Select HTTP request/response alternatives according to the contract's matching
rules, including exact status, status range, default, and media-type selection.
An observed status matching a range or default covers that alternative; it does
not claim that every possible status in it was exercised. HTTP responses that prohibit
bodies exclude their body/media conditions; headers and status still require
validation. Redirect responses with bodies also require validation before credit. Request and response
coverage are calculated separately, then associated through the exchange.

For composed schemas:

- `oneOf`: exactly one successful branch is required. Credit that branch; zero or
  multiple matches are violations, not multiple successful branch hits.
- `anyOf`: record every branch the instance actually satisfies. Several may
  match; do not describe them as mutually exclusive scenarios.
- `allOf`: all constituent constraints apply. Satisfying one constituent alone
  does not establish validity of the composed schema.
- References retain stable locations in the expanded operation context. Repeated
  instances aggregate against those locations, with instance paths kept as
  evidence. Recursive analysis and `if`/`then`/`else` conditions are unsupported.

A condition may be infeasible because of another condition. Exclude it only
when that is established, and explain why. Lack of observations is not evidence
of impossibility. Where feasibility cannot be established, report that
uncertainty instead of silently shrinking the denominator.

## 5. Separate observation, successful coverage, and rejection testing

Maintain at least these distinct results:

| Result | Meaning |
| --- | --- |
| Observed | The retained evidence supports this condition observation, regardless of test outcome. |
| Covered | A passing, complete attempt exercised the condition in a message whose applicable contract and branch validated successfully. |
| Violation observed | The evidence violated a declared constraint. This is not successful coverage. |
| Unknown | Required evidence, contract support, or evaluation is missing or inconclusive. |
| Not applicable | This condition does not apply to this particular payload or branch. |

A failed test can contribute useful observations and differences, but cannot
silently become a successful coverage hit. Multiple passing retries do not
multiply the number of covered targets; preserve all attempt outcomes alongside
the deduplicated coverage result.

Request and response conditions are evaluated separately. An invalid request
earns no positive request-condition credit. A documented, valid error response
can earn response-condition credit if the attempt otherwise qualifies as passing
and complete; this alone does not prove the rejection scenario was asserted.

Ordinary capture retains strict validation and its existing framework-owned
assertions. Wiretap does not measure rejection assurance: observing an
error status is not evidence that a test asserted the intended validation rule.
Generated tests and their execution are outside the local tool's scope.

## 6. Measure individual conditions and combinations separately

Individual condition coverage counts each feasible target once:

```text
covered targets / feasible targets in the supported measurement scope
```

Report operation, request, response, enum, boundary, presence, and branch
coverage separately. A single combined percentage can be dominated by APIs with
large schemas and must not hide an entirely untested operation.

Show unsupported conditions, unknown evidence, feasibility uncertainty, and
explicit exclusions beside any percentage. If the denominator is unresolved,
report a partial measurement or unknown denominator, not an unqualified 100%.
State the selected runs, tested source revisions, and measurement scope. Keep
distinct target counts separate from the number of exchanges or tests supporting
them. Do not combine different contract or application revisions into an
apparently coherent passing result without an explicit comparison policy.
A zero-sized denominator is not 100% coverage. Missing or partial runs cannot
produce a final assurance claim, even when their observed targets look complete.

Combination coverage records conditions observed together. It is additional to
individual coverage: independent hits in separate exchanges cannot be assembled
into an interaction that never occurred.

For example, observing `status=cancelled` in one response and `items=[]` in another
does not establish coverage of `status=cancelled` with `items=[]`.

Observed combinations are listed with their evidence, without a combination
coverage percentage or a generated Cartesian-product denominator.
Within arrays, preserve instance relationships: a field observed in item 0 and
another field in item 1 do not prove the same-item combination was exercised.

## 7. Example: one response exercises multiple conditions

Assume the response contract declares:

- `status`: enum `pending`, `shipped`, `cancelled`.
- `items`: array with `minItems: 0` and `maxItems: 10`.
- `reference`: string matching `^ORD-[0-9]{3}$`.
- `note`: optional, non-null string with `minLength: 1` and `maxLength: 200`.

Observed response:

```json
{
  "status": "pending",
  "items": [],
  "reference": "ORD-123"
}
```

| Condition | Contribution |
| --- | --- |
| Status enum | `pending` observed; the other enum values remain unexercised by this response. |
| Items length | Minimum boundary `0` observed; interior and maximum remain unexercised. |
| Reference pattern | Matching value observed; no claim about regex-internal path coverage. |
| Note presence | Absent observed; present remains unexercised. |
| Note length | Not applicable to this response; its feasible length targets remain in the contract inventory. |

If the attempt is passing and complete and the response validates, the supported
observations also become covered targets. All point to the same test/exchange.
The combination is recorded as those observations occurring together.
A later response can cover several remaining targets at once, without adding
new labels to either the mock or the test.

## 8. Fingerprints support comparison, not compatibility verdicts

Each target records its operation key, contract revision, phase, condition and
measurement rules. Combination fingerprints hash the measured condition keys
and exact instance locations observed together. They do not hash arbitrary
payload values, include test names in cross-repository identity, or infer
application assertions.

Response status is a condition under an operation, so responses with different
statuses remain associated with the same operation. Contract revision is part
of target identity; an operation key remains discoverable across revisions.

`wiretap compare` reports gained/lost coverage for unchanged target identities
and additions/removals when a contract changes. It does not map changed targets
across revisions. See [coverage comparisons](report-policies.md#compare-two-existing-coverage-reports).

Equal combination fingerprints identify equal measured condition combinations.
They do not establish equal payloads or consumer/provider compatibility. The
[assumption checker](assumption-checks.md) separately compares retained concrete
requests, paired responses and supported contract declarations.

## 9. Preserve evidence quality and protocol boundaries

Do not infer a complete object's fields or an array's length from a truncated
body. Individual facts independently supported by retained evidence may be
reported as observations, but missing facts stay unknown. A skipped body
validation must never count as successful body-condition coverage.

Older redacted archives may have changed field presence, type, length, enum
membership or pattern matches. Readers preserve those limits: affected messages
remain unknown and placeholders never count as original observations. Current
capture retains synthetic test data unchanged.

The coverage model is protocol neutral; extraction is protocol specific:

- HTTP uses route/method, parameters, request/response bodies, status, and media
  alternatives from the selected OpenAPI dialect.
- gRPC uses fully qualified service/method, request/response messages,
  and final status, with structural presence/value conditions. Length bounds
  and business validation annotations remain outside the current measurement.
- Streaming uses ordered message observations and explicit direction completion;
  per-message conditions do not imply business sequence coverage.

These are the current HTTP and native gRPC measurement boundaries. Generic
event recordings for other protocols remain explicitly unmeasured.

## Measurement guarantees

- One response contributes to every applicable condition and preserves the
  combination without inventing additional scenarios.
- Untested operations and feasible child targets of absent optional fields
  remain visible.
- Equivalent evidence produces deterministic descriptors and fingerprints;
  meaningful differences remain explainable from source observations.
- Failed, incomplete, legacy-redacted, unsupported, and ambiguous cases cannot silently
  become successful coverage or shrink the reported scope.
- Mock observations, provider observations and supplied contracts remain
  distinguishable. Mock coverage never establishes provider agreement.
- Reports link claims to test names and exact exchange evidence. Rebuilding a
  report cannot alter captured evidence or test outcomes.
- No new test runner, mock framework, or scenario-labelling system is required.

Related: [bundle evidence](bundle-evidence.md), [capture artifacts](artifacts.md),
[architecture](architecture.md).
