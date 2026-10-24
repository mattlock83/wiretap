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

For cross-repository comparison, use the same provided `api_id` and dependency
`asset_id`, supplied contract, and `rules_version`. Keys use full SHA-256 digests;
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
The cross-bundle comparator itself is not implemented.

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
truncated, redacted or undecodable messages cannot earn payload assurance.
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
messages on a non-streaming side. Incomplete, omitted, redacted or undecodable
streams remain observations without successful credit. Capture limitations yield
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
- Any redaction rule on a message conservatively makes that message unknown;
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
- Only supplied contracts define denominators. Cross-revision target mapping and
  explicit expected-negative annotations remain future work.

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
An operation with no exchanges must still be visible as uncovered. To claim
coverage across all APIs, the run must have a separately declared, complete API
inventory; APIs absent from both the inventory and traffic cannot be discovered
by coverage analysis alone.

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

Each target has a stable identity, applicability prerequisites, a predicate,
and a polarity (valid condition or violation). The inventory records these
before observations are classified.

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
form the initial coverage vocabulary; they are not a request to enumerate every
possible payload.

| Contract feature | Valid coverage classes | Separate violation observations |
| --- | --- | --- |
| Declared operation | Operation exercised | Unknown operation or unmatched route |
| Response status/media alternative | Each declared alternative exercised | Response outside the declared alternatives |
| `enum` | Each permitted value, using its actual JSON type | Value outside the enum |
| `const` | Declared value observed | Different value |
| Optional property/parameter | Present; absent | Invalid present value is recorded against its constraints |
| Required property/parameter | Present | Absent |
| Nullable value | Null; non-null, where each is permitted | Null when forbidden |
| Inclusive numeric bound | Exactly at each bound; strictly inside the valid range | Below minimum; above maximum |
| Exclusive numeric bound | Inside the valid range; an exclusive endpoint is never a valid boundary hit | At or beyond the excluded endpoint |
| String length | At the effective minimum; interior; at a declared maximum | Too short; too long |
| Array length | At the effective minimum; interior; at a declared maximum | Too few; too many items |
| `pattern` | A matching value | A non-matching value |
| Supported, enforced `format` | A conforming value | A non-conforming value |
| Declared type alternatives | Each allowed type observed | Disallowed type |
| `oneOf`, `anyOf`, conditionals | Applicable branch observations, as specified below | Branch-selection or branch-validation failure |

Boundary rules:

- Keep only classes that exist. If minimum equals maximum, there is one boundary
  class, not two. An integer range from 0 to 1 has no interior integer class.
- Respect the entire schema when deciding feasibility. An enum, `multipleOf`,
  or another composed constraint can eliminate a nominal boundary or interior.
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

The supported-condition set is versioned. Constraints not covered by the
initial vocabulary can still be enforced by schema validation; they must not
be described as having condition coverage until measurement rules exist. For example, tuple positions, `uniqueItems`,
`contains`, dynamic-key rules, property dependencies, and forbidden additional
properties need explicit rules before they contribute to a coverage percentage.
Do not generate an unlimited target inventory from arbitrary property names.

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
- `if`/`then`/`else`: apply the dialect's actual conditional evaluation. Credit
  the selected branch and its applicable conditions, not the unselected branch.
- References and recursive schemas retain stable contract locations. Recursion
  must not create an infinite catalogue of depth-specific targets. Repeated
  instances aggregate against their schema locations, with instance paths kept
  as evidence. Any unsupported recursive analysis remains explicit.

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

A future executor could measure rejection assurance separately. It would need to:

- Record which request constraint was violated.
- Associate the documented rejection response with that request.
- Require evidence that the existing test actually expected and checked the
  rejection before calling it successfully tested. A `400` response and a
  suggestive test name alone are insufficient.
- If a request violates several constraints, report rejection of that combination.
  Do not assume the provider independently checked each violated constraint.
- A malformed mock/provider response is a response-contract violation. It does
  not become a successful valid-response target merely because it tests consumer
  resilience. Such resilience intent needs its own explicit test evidence.

Ordinary capture retains strict validation and its existing framework-owned
assertions. Wiretap does not currently measure rejection assurance: observing an
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
coverage percentage. Full Cartesian-product coverage is not required. A future
pairwise or selected-combination policy must define the target set, respect
feasibility and conditional applicability, and disclose what it omits.
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

Preserve three separate descriptors:

1. Operation identity: where comparable interactions belong.
2. Request observation: parameters/message shape, measured conditions, and,
   where appropriate, a digest of canonically normalised retained values.
3. Response observation: outcome/status and the corresponding payload observations.

A condition-combination fingerprint can index the sorted, structured observation
set. Version the canonicalisation and coverage rules. Keep the full digest and
human-readable descriptors; a short three-word alias is only a display aid and
must not be treated as collision-free identity.

Do not put response status into the primary operation key: the same request
returning different statuses must remain comparable. Do not use test names as
cross-repository identifiers. Retain test names as evidence of intent.

Include the contract revision as measurement provenance, but do not let a new
revision make the underlying operation undiscoverable. Comparing condition
coverage across revisions requires mapping changed targets and reporting new,
removed, or changed constraints, rather than comparing raw fingerprints alone.

Canonicalisation must retain meaningful distinctions: missing versus null,
number versus string, array ordering, repeated parameters, and protocol-specific
presence/default semantics. Never assume every ID, timestamp, or header can be
ignored. Any normalisation rule that discards values must be explicit and
versioned. Hash inputs come from permitted retained evidence, not secret values
removed by redaction.

Equal fingerprints mean equal observations under those rules. Different
fingerprints invite an explainable diff. Neither result alone proves
compatibility: providers must accept consumer requests, while captured mock
fields do not prove what the consumer actually requires from responses.

## 9. Preserve evidence quality and protocol boundaries

Do not infer a complete object's fields or an array's length from a truncated
body. Individual facts independently supported by retained evidence may be
reported as observations, but missing facts stay unknown. A skipped body
validation must never count as successful body-condition coverage.

Redaction can change field presence, type, length, enum membership, or pattern
matches. Affected conditions remain unknown unless a supported measurement was
safely computed before redaction and retained with explicit provenance. Such
pre-redaction summaries are a separate privacy-sensitive design choice, not
assumed available in existing bundles. Never count a redaction placeholder as
an original observed value.

The coverage model is protocol neutral; extraction is protocol specific:

- HTTP uses route/method, parameters, request/response bodies, status, and media
  alternatives from the selected OpenAPI dialect.
- gRPC currently uses fully qualified service/method, request/response messages,
  and final status, with structural presence/value conditions. Length bounds
  and business validation annotations remain outside the current measurement.
- Streaming uses ordered message observations and explicit direction completion;
  per-message conditions do not imply business sequence coverage.

These are adapter requirements, not claims of current transport support.

## Measurement guarantees

- One response contributes to every applicable condition and preserves the
  combination without inventing additional scenarios.
- Untested operations and feasible child targets of absent optional fields
  remain visible.
- Equivalent evidence produces deterministic descriptors and fingerprints;
  meaningful differences remain explainable from source observations.
- Failed, incomplete, redacted, unsupported, and ambiguous cases cannot silently
  become successful coverage or shrink the reported scope.
- Mock observations, provider observations and supplied contracts remain
  distinguishable. Mock coverage never establishes provider agreement.
- Reports link claims to test names and exact exchange evidence. Rebuilding a
  report cannot alter captured evidence or test outcomes.
- No new test runner, mock framework, or scenario-labelling system is required.

Related: [bundle evidence](bundle-evidence.md), [capture artifacts](artifacts.md),
[architecture](architecture.md).
