# Check consumer and provider assumptions

Use `wiretap check` to compare a consumer's captured mock interactions with the
provider's own captured tests. The check works offline from complete Wiretap
archives and their Arazzo workflows for JSON/REST APIs. It does not start servers, replay requests, execute scripts or workflow
expressions, or generate tests.

**A pass means every selected captured mock exchange has at least one complete,
passing provider test exchange that supports that concrete request and response,
with agreement on supported frozen contract rules where available.**
HTTP matches with both schemas absent or explicitly omitted are example-only;
the report identifies them. With only the provider's HTTP schema, the check
validates both captured exchanges against that provider contract.

- [Check against shared application artifacts](#check-against-shared-application-artifacts)
- [What a passing result establishes](#what-a-passing-result-establishes)
- [Issues the check will find](#issues-the-check-will-find)
- [What the check will not establish](#what-the-check-will-not-establish)
- [Keep the evidence together](#keep-the-evidence-together)
- [Share provider snapshots](#share-provider-snapshots)
- [Select the dependency and provider](#select-the-dependency-and-provider)
- [What agreement means](#what-agreement-means)
- [Interpreting results](#interpreting-results)
- [Use the result in CI](#use-the-result-in-ci)

## Check against shared application artifacts

Each team uploads its complete Wiretap runs and Arazzo exports under its application ID in your shared artifact store. Restore the
intended snapshots into a local `api-evidence/` directory, then use one command from either side:

```sh
wiretap check consumer --consumer ./orders-run --artifacts ./api-evidence
wiretap check provider --provider ./inventory-run --artifacts ./api-evidence
```

Both commands use each run's `exports/arazzo/arazzo.json` and its OpenAPI
source companions. The complete archives supply response bodies, response
headers, passing-test provenance, provider mappings and frozen contracts.

The consumer command asks: **Do all my captured mocks agree with their
providers' tests?** It uses each mock dependency's declared provider mapping to
find the provider application/API. Add `--dependency inventory` to check just
that mock. Every selected mock needs a mapping and a unique provider snapshot.

The provider command asks: **Do my tests still support the mocks used by other
applications?** It discovers consumer snapshots whose mappings point to the
local provider application/API and checks each matching mock. Consumers mapped
to a different provider version remain visible as unverified; they are not
silently excluded from the impact check.

Both modes require matching concrete requests and compatible responses from
complete passing provider tests. A request scenario absent from those tests
fails, even when it is valid under the provider's schema. Additional provider
scenarios do not create obligations for consumers.

An HTTP mock needs no consumer-owned copy of the provider spec: the check uses
the provider archive's frozen contract. The consumer's own main HTTP API still
requires its spec by default. See [the shared snapshot setup](#share-provider-snapshots)
for configuration, identity and version selection, and the directory layout.

For a deliberately chosen pair, keep both paths explicit:

```sh
wiretap check consumer \
  --consumer ./orders-run \
  --provider ./inventory-run \
  --dependency inventory
```

`wiretap check provider` accepts the same explicit inputs. Both viewpoints
enforce the same consumer-to-provider relationship. This form also supports
older captures without declared mappings; the supplied pair establishes the
relationship. A declared mapping, when present, must still agree.

For package or CI shards of the same build, use explicit
[run-set manifests](run-sets.md) with `--consumer-set` and `--provider-set`.
A matching request and response must still come from one provider exchange;
separate shards cannot supply half of a match each.

## What a passing result establishes

For each captured call to the selected mock, Wiretap:

1. Requires a complete consumer exchange from a passing test attempt.
2. Finds complete passing provider exchanges with the same normalized request.
3. Looks for the mocked response in one of those **same exchanges**.
4. For HTTP with only a provider contract, validates both exchanges under that
   frozen OpenAPI 3.0 contract. With both contracts, validates each exchange
   against its own schema and compares exercised validation rules. If both
   schemas are absent, explicitly marks an example-only match instead.
5. Checks that the supplied Arazzo workflows and their source companions agree
   with projections regenerated from their own archives.

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
same complete response. With available HTTP contracts, the error response must
validate; intentional negative requests skip request-value validation. Their
declared request rules are still compared when both schemas are supplied. A
connection failure recorded as a transport error cannot establish agreement merely because Wiretap also
recorded a `502`.

### HTTP contract rules that differ despite equal examples

The provider's frozen OpenAPI 3.0 contract validates the concrete mocked
request/response even when the consumer dependency has no spec. It also
validates the provider exchange. This can catch invalid mocked values without
requiring a consumer-owned copy of the provider contract. A matching complete
passing provider test remains mandatory; the specification alone is not test
coverage.

If both sides supply frozen OpenAPI 3.0 contracts, equal examples are
insufficient. For a prospective match, Wiretap compares declared validation rules
for all request parameters, the selected request body/media type, and the
selected response status, media type, body schema and declared headers.
Nested properties participate even if they are optional or absent from the
observed example. References are resolved; moving a schema to a different file
does not itself create drift.
Compared rules include types and formats, enum sets, required properties,
nullability, numeric bounds, string lengths/patterns, collection sizes,
object/property constraints, nested schemas and composition/discriminator rules.

| Issue | Example with the same captured values | Result |
| --- | --- | --- |
| Enum set changes | Both return `status: "ready"`; one contract also permits `"pending"`. | Contract mismatch, including enum additions. |
| Numeric, string or collection bounds change | Both return a three-item array; contracts declare `maxItems: 5` and `maxItems: 10`. | Contract mismatch, including a relaxed bound. |
| Requiredness, nullable or type changes | Both include `quantity: 3`; one contract makes `quantity` optional or nullable. | Contract mismatch. |
| Nested constraints or composition change | An unused optional property, format, pattern, discriminator mapping or composed schema differs under the exercised operation. | Conservative contract mismatch. |
| Equal examples violate a supplied contract | Both return a value excluded by their own matching enum, or a successful request omits a required parameter. | Contract violation; passing framework tests do not override it. |
| Consumer has no dependency contract | Provider supplies OpenAPI 3.0 and matching passing evidence. | Can verify concrete examples under the provider contract, without comparing independent consumer declarations. |
| Provider has no HTTP contract | Consumer supplies a dependency schema, but provider omitted its schema. | Unverified; a consumer-owned spec cannot replace provider contract evidence. |

The comparison preserves declared rules rather than proving their semantic
equivalence. A harmless relaxation or a structurally different but equivalent
schema expression can therefore fail. Documentation, titles, descriptions,
examples and defaults are ignored; reordering enum values or required names
does not create drift. Unused operations, other response statuses and unselected
media types do not receive a static comparison.
Explicit discriminator mappings are preserved conservatively; renaming their
declared schema targets can fail even when the resolved schemas are equivalent.

With only the provider contract, both exchanges are revalidated against it.
With both contracts, each exchange is revalidated against its own schema and
their exercised declarations are compared. Every response is checked; a
request is also checked when that exchange's response status is below `400`;
`400` and higher permit deliberate negative request examples, so request-value
validation is skipped. Request-rule comparison remains active when both
contracts are supplied.

When both HTTP schemas are absent or explicitly omitted, matching examples may
pass with `contract_check: "unavailable"`. This is visible example-only
assurance. A missing provider schema when a consumer schema is supplied,
unsupported OpenAPI 3.1, unreadable contracts or rules whose precision cannot be checked reliably leave the comparison
unverified. Unsupported examples also include media-type `encoding` rules,
OpenAPI `content`-encoded parameters or response-header definitions, complex
numeric object parameter/header serialization, and path/operation-level server
overrides. Multiple differently cased keys for one captured header, or multiple
`Content-Type` values, also prevent a reliable contract check. `nullable` combined with enum, `not` or composition is also
unavailable because the underlying validator cannot establish those null cases
reliably. Scalar and array numeric parameters are supported. Rule projection
is bounded, so schemas exceeding its depth or node budget remain
unverified. These cases do not silently downgrade to example-only agreement.
Before selecting exercised rules, the loader checks the full frozen document
graph for validity and reliable numeric precision. Malformed, unsupported or
precision-unsafe rules elsewhere in those documents can prevent loading and
produce `http_contract_unavailable`, even when their operation is unexercised.

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
| Selected Arazzo inputs are missing, edited, stale or incomplete | Input error when no full matching projection is supplied. Arazzo source companions must also match. |
| Arazzo selection contains non-JSON HTTP or other unsupported traffic | Cannot certify that boundary by exporting only its eligible JSON/REST subset. |

These evidence-selection rules require usable projection inputs. Failed provider
attempts do not automatically fail the check when the exports can be verified
and complete passing evidence supports every selected consumer example.
Incomplete captures may prevent strict Arazzo regeneration even when another
provider attempt could support a match; see [Arazzo input limits](#arazzo-check-inputs).
Keep the provider suite's own exit status as a separate CI gate.

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

### Worked example: an enum changes without changing the example

Both mock and provider return `{"status":"ready"}` for the same request.
The consumer's frozen response schema declares
`enum: [ready]`; the provider declares `enum: [ready, pending]`. Both observed
responses satisfy their own schema, but the allowed sets differ.

The check fails with `http_contract_mismatch` and a contract difference such as
`contract/response/body/properties/status/enum`. The provider's added value may
be a safe change for this consumer. The failure establishes declared-rule drift,
not that `pending` occurred, that the consumer cannot handle it, or that a
runtime break occurred. Align the intended contracts or investigate the change
and recapture the relevant suites. The same conservative rule applies to a
relaxed limit, such as increasing `maxLength` while the observed string remains
short enough for both versions.

## What the check will not establish

### That every request, state or outcome is compatible

Recorded examples identify the operation, status and media types whose HTTP
rules are compared. Those declared rules can expose drift in enum values or
bounds not reached by the example, but agreement does not prove that those
other values behave correctly. A supported example does not generalize to
other IDs, quantities, tenants, error branches or untested API versions. An
operation can have one supported example and matching rules while still having
many untested cases. Use the separate
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
is the right resolution, then regenerate evidence; editing a workflow to
hide the difference does not change the raw capture.

### That the entire API or every schema rule is compatible

With a provider-only frozen OpenAPI 3.0 contract, the check validates the
concrete mock and provider exchanges under that contract. It has no independent
consumer declaration to compare, so an unobserved enum addition or relaxed
provider limit need not fail. The `openapi_3_0_provider_contract` marker makes
this assurance explicit. If both sides supplied contracts, the check also
compares declared rules on the exercised operation and validates each side
under its own schema. Neither path statically diffs unexercised routes, other
response statuses or unselected media alternatives. A new route or a changed unused
error response may therefore remain undetected.
The full frozen document graph must still load reliably: invalid, unsupported
or precision-unsafe rules outside the exercised selection can make the required
contract unverified, even though those rules are not diffed.

A schema-free consumer capture does not infer allowed enum sets, minimum or
maximum lengths, required fields or a replacement OpenAPI file. A captured
`status: "ready"` records that value; it does not declare that `"ready"` is the
only allowed value. Supply a consumer dependency schema when independent
consumer declarations must be compared with the provider's rules.

Declared-rule comparison is conservative equality, not a proof of schema
subtyping or semantic equivalence. Enum additions, relaxed limits and
structurally different equivalent schema expressions can fail without proving
a breaking change. Conversely, equal rules do not establish business behavior
for every value those rules allow. Descriptions, examples and defaults are not
validation rules in this comparison.

Successful and redirect exchanges revalidate request values; error responses
with status `400` or higher permit deliberately invalid requests, so those
request values are not required to validate. Request rules are still compared
when both contracts are supplied, and every response is validated. This does
not establish that a negative test asserted the rejection or that all invalid requests are rejected.

When both HTTP schemas are absent or explicitly omitted, the check can only
support example agreement. `contract_check: "unavailable"` makes this weaker
result explicit. A missing provider schema when only the consumer supplies one,
unsupported OpenAPI 3.1 or rules that cannot be checked reliably produce an
unverified result. Keep contract coverage
alongside the check to see which declared cases have actually been exercised.

Matching values also do not prove business semantics. Two suites can both
record `price: 100` while interpreting the units differently if no visible
field distinguishes the units.

### That workflows, retries or call counts are correct

Each HTTP exchange is checked independently. The command does not
verify prerequisites, ordering across calls, transaction boundaries, side
effects, idempotency, retry policy or the number of times a dependency should
have been called. Multiple consumer exchanges can reuse the same supporting
provider exchange.

All retained attempts are considered; the checker does not select only the
last retry. A failed consumer attempt with selected mock traffic remains
unverified even if a later retry passed. Failed provider retries cannot
support a match, while a complete passing retry can.

### That unexecuted mocks or uncaptured traffic are covered

The inputs contain calls that actually crossed Wiretap. The command does not
discover source-code stub definitions, unexercised consumer code paths, or
traffic that bypassed capture. A mock configured with ten responses but
exercised once contributes the one observed exchange. Adding uncaptured
examples to a selected Arazzo workflow does not extend
the comparison; the edited projection will no longer match its recorded export.

Zero calls to the selected mock fail. However, one captured call can pass
without detecting that an expected second call never happened; assert those
requirements in the application tests. Capture each dependency and declare its
provider mapping. With `--artifacts`,
the consumer command selects all mock dependencies by default, and the provider
command selects all mapped consumers present in that local artifact set. It
cannot discover consumers whose evidence was never uploaded or restored.
In consumer discovery or explicit pair mode, `--dependency` narrows the
selection, so a scoped pass covers only that mock.

### That a live deployment, security policy or performance is correct

The command performs no live requests. It does not establish deployment
routing, DNS, TLS/certificate behavior, HTTP-version compatibility, latency,
load capacity, concurrency or deadline behavior. Endpoint scheme, host and
port are excluded from cross-archive HTTP request matching. Differences in
ignored headers are outside that comparison.

Authorization and business headers are significant unless ignored, but
matching dummy credentials does not test authentication or authorization
policy. OpenAPI security requirements are outside the validation-rule
comparison and do not execute authentication checks. Tests must exercise the
appropriate allowed and denied cases.

The provider identity, target kind `service`, version and source metadata are
declarations in the capture. Integrity hashes detect inconsistent or changed
evidence; they do not authenticate its publisher or prove that the declared
provider code was executed. Without a mapping, explicit inputs establish the
pairing chosen by the caller. Choose and retain evidence from trusted builds.

### That Arazzo workflows execute successfully

Arazzo verification checks the exported document and its local OpenAPI source
companions against the archive-derived projection. It does not evaluate runtime
expressions, execute success criteria or workflows, resolve live servers, or
prove that a particular Arazzo runner can replay them. Response comparison uses
the complete captured response from the archive, rather than treating an
exported status criterion as proof of response-body agreement. Keep the document
and companions together and use an Arazzo runner separately when execution is
the question.

### That other protocols receive native semantic checking

Protocol comparison currently covers HTTP exchanges. Selected
event interactions such as Kafka messages are unverified. GraphQL sent as an
ordinary captured HTTP exchange can be compared as an HTTP example, but this
does not analyze a GraphQL schema, operation semantics or subscriptions.
Opaque, encrypted or unavailable payloads cannot supply missing observations.

## Keep the evidence together

`--consumer` and `--provider` each accept a run directory or its `bundle.json`.
Keep the complete archive, including `tests/`, `contracts/` and `payloads/`.
Coverage reports alone are insufficient: equal coverage conditions do not prove
that the same request received the same response.

Both runs must be complete with no pending attempts. Each selected consumer
example must come from a complete passing attempt and contain supported,
complete evidence. A failed or incomplete provider attempt cannot establish a
match. Another complete passing provider attempt can support it when the chosen
projection inputs can be generated and verified. Excluded provider exchanges
remain visible in the result. Omitting an incomplete interaction from
a projection does not turn it into supporting evidence.

An interrupted run, an unverifiable provider identity, an empty consumer
selection or entirely unusable consumer evidence fails before projection inputs
are required. For an otherwise eligible comparison, missing, invalid or stale
projection inputs are invalid inputs.

### Arazzo check inputs

Each archive defaults to `exports/arazzo/arazzo.json`. Generate missing inputs
from the exact archives:

```sh
wiretap export --bundle ./consumer-run --format arazzo --output-dir ./consumer-run/exports/arazzo
wiretap export --bundle ./provider-run --format arazzo --output-dir ./provider-run/exports/arazzo
wiretap check consumer --consumer ./consumer-run --provider ./provider-run \
  --dependency inventory
```

Explicit pairs can select a different document or directory on either side:

```sh
wiretap check provider --consumer ./consumer-run --provider ./provider-run \
  --dependency inventory \
  --consumer-arazzo ./consumer-arazzo/arazzo.json \
  --provider-arazzo ./provider-arazzo
```

Keep each document's source companions at their exported relative paths.
Missing, stale or tampered documents and companions are input errors. Complete
archives remain required: an Arazzo document alone does not contain all the
response evidence and test provenance needed for comparison. A selected boundary
with unsupported traffic cannot pass by omitting that traffic from its Arazzo
export. Only eligible JSON/REST boundaries can use this format.

Arazzo export and regeneration are strict across the entire run. An incomplete
or unrepresentable eligible JSON capture anywhere can prevent export or cause
check input verification to fail, including traffic on an unselected boundary
or an excluded provider attempt beside a usable passing attempt. Arazzo has no
omit-incomplete mode. A passing provider witness cannot bypass that export
failure.

Set `outputs: {coverage: [json, text], arazzo: true}` in both projects to produce
these inputs automatically. Shared-store and run-set checks use each member's
default export location;
explicit Arazzo path overrides apply only to an explicit pair.

## Share provider snapshots

Each team's test suite records its own provided API and freezes that API's
spec. Consumer teams can capture HTTP mocks without keeping another copy of
the provider's OpenAPI file. Both sides still keep complete run archives and
Arazzo exports.

For example, the inventory provider's `.wiretap.yaml` owns its contract:

```yaml
version: 1
application: {id: inventory-service, name: Inventory}
sut_source:
  version: '${INVENTORY_VERSION}'
  revision: '${INVENTORY_REVISION}'
outputs: {coverage: [json, text], arazzo: true}
sut:
  id: inventory-http
  url: ${INVENTORY_SERVICE_URL}
  kind: service
  schema: schemas/inventory.openapi.yaml
```

The orders consumer supplies its own main API spec and maps its schema-free
HTTP mock to that provider:

```yaml
version: 1
application: {id: orders-service, name: Orders}
outputs: {coverage: [json, text], arazzo: true}
sut:
  id: orders-http
  url: ${ORDERS_SERVICE_URL}
  kind: service
  schema: schemas/orders.openapi.yaml
dependencies:
  inventory:
    url: ${INVENTORY_MOCK_URL}
    kind: mock
provider_mappings:
  inventory:
    application_id: inventory-service
    api_id: inventory-http
    version: '${INVENTORY_VERSION}'
```

Set the environment variables to the intended tested builds before capture.
The mapped provider `version` must equal the provider archive's recorded
`sut_source.version`. The provider's Wiretap version is a separate recorder
version and does not establish the SUT build.

Use the stable application ID as the first folder beneath the artifact root.
Each run keeps its own files together. Additional API, revision and run folders
are optional; discovery reads identities and version provenance from
`bundle.json`, rather than inferring them from nested folder names:

```text
api-evidence/
  inventory-service/
    inventory-http/
      <sut-revision>/
        <run-id>/
          bundle.json
          tests/
          contracts/
          payloads/
          exports/arazzo/
  orders-service/
    orders-http/
      <sut-revision>/
        <run-id>/
          bundle.json
          tests/
          contracts/
          payloads/
          exports/arazzo/
```

The top-level application folder must agree with the captured application ID.
Use a single folder name for that ID, such as `inventory-service`; IDs containing
path separators or traversal components cannot identify a shared-store folder.
Existing captures with such application IDs remain usable in explicit pair
mode; the shared-store layout requires folder-compatible identities.
Keep both providers and consumers in this shared structure: a service can be a
provider of its own API and a consumer of several other APIs in the same run.

Use your existing artifact upload/download tooling. Download the complete run
before invoking `wiretap check`. For Artifactory or CI artifacts packaged as
ZIP or TAR files, extract them first. The CLI accepts an extracted run directory
or its `bundle.json`, with all referenced files retained alongside it.
Preserve internal paths, filenames and contents; moving the whole run is
supported, while flattening its files or copying only workflows loses inputs.
Keep `contracts/` and its imported files together with `tests/` and `payloads/`.
`--artifacts` discovers local extracted runs beneath application folders. It
does not upload to or fetch from Artifactory or another remote store.

Discovery stops at each run directory instead of scanning its payloads. It
rejects symbolic links in the scanned folder structure and invalid or
unsupported manifests, even for applications that would not be selected.
Keep this local directory dedicated to comparison snapshots. Discovery is
bounded to 32 directory levels, 100,000 scanned entries and 1,024 manifests,
with 64 MiB per manifest and 256 MiB of manifests in total. A limit produces
an input error; reduce the downloaded comparison set.

A copy of the local run can remain in the store and is not selected as its own
peer. Different manifests claiming the same run ID are rejected; retain intact
snapshots with their original identities.

Prepare a deliberate set of snapshots for each check:

- **Consumer mode:** each selected mock mapping must resolve to exactly one
  provider run with the declared application/API ID. A mapping's `version`
  narrows selection to matching `sut_source.version` values. Two matching runs
  remain ambiguous even if their contents are identical.
- **Provider mode:** discover consumers whose mappings name the local provider
  application/API, then require one snapshot for each selected consumer
  application/API. A mapping to another provider version is still compared and
  produces `provider_version_unverified` rather than disappearing.
- **Both modes:** missing required peers, ambiguous snapshots and an empty
  selection are input errors. Consumer mode also rejects a selected mock without
  a provider mapping. Wiretap does not choose the latest timestamp,
  combine passing examples from historical runs or silently skip an unresolved
  dependency. Restore only the intended snapshots, or use explicit pair inputs
  to select a particular run.

Your remote store can retain full history. The local `--artifacts` directory is
the selected comparison set, so download a specific revision/run from each team
for the current gate. Keep that set stable while checking: changed manifests or
added/removed runs invalidate the result, including changes to previously
unrelated applications. Record the manifest digests with the result. A capture's
Wiretap producer commit identifies the recorder; `sut_source` identifies the
application build. Display names and a moving `latest` label do not establish
that build.

Once the local set is ready:

```sh
wiretap check consumer \
  --consumer ./orders-run \
  --artifacts ./api-evidence

wiretap check provider \
  --provider ./inventory-run \
  --artifacts ./api-evidence
```

Shared-store checks use each run's `exports/arazzo/arazzo.json` and its source
companions. Shared-store mode rejects Arazzo path overrides; use
explicit pair mode when projections live elsewhere. Supply only the local
viewpoint's run with `--artifacts`: `--consumer` in consumer mode or `--provider` in provider
mode. Supplying both run flags together with `--artifacts` is an input error.

Save reports outside the entire artifact root and the local input archive.
Create `check-results/`, then add
`--format json --output ./check-results/inventory.json` to either command.
The result contains one comparison per selected consumer/mock/provider
relationship and fails if any comparison is uncovered, mismatched or unverified.
No matching consumers is an input error, not a passing provider check.

| Inputs | What a passing HTTP match establishes |
| --- | --- |
| Consumer has no dependency spec; provider has frozen OpenAPI 3.0 | Both captured exchanges satisfy the provider contract and a passing provider test supports the concrete mock example. |
| Both sides have frozen OpenAPI 3.0 | The concrete examples agree, relevant messages validate, and exercised declarations also agree under the strict drift policy. |
| Neither side has a spec | Example agreement only, explicitly marked `contract_check: "unavailable"`. |

The final row covers explicit HTTP schema opt-outs and older captures. The
provider's main HTTP API still requires its spec by default during setup.

Requests paired with HTTP status `400` or higher skip request-value validation
to support negative tests; every response is validated when a contract is
available. This exception applies to both contract-backed rows above.

A schema-free mock capture retains unknown consumer contract coverage. Checking
it against a provider snapshot does not change that original report. A spec
without a qualifying passing provider example still leaves the scenario
uncovered. See [result assurance markers](machine-interface.md#assumption-check-results)
and [the CI gate](ci-example.md#gate-consumerprovider-assumptions-offline).

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

Declared mappings must agree with the selected provider archive. With an
explicit pair, Wiretap infers the mock only when it is unambiguous; otherwise
supply `--dependency`. With `--artifacts`, mappings drive discovery, and consumer
mode checks every mock by default. `--dependency` scopes consumer discovery to
one local mock. Provider discovery checks every mapped consumer dependency
and rejects `--dependency`; use an explicit pair to select just one.

If a mapping declares `version`, the provider's captured `sut_source.version`
must establish it. Consumer discovery requires a snapshot matching the pinned
version. For explicit pairs and provider impact checks, a missing or different
version produces an unverified comparison. Choose tested revisions and runs
with your artifact tooling; Wiretap does not select a latest build.

The HTTP inventory mock needs no schema or opt-out flag. Its captured contract
coverage is unknown when no dependency spec was recorded. The later provider-contract
check does not rewrite that archive or invent consumer coverage. It still
requires an actual matching passing provider test. Add a dependency `schema`
when you also want independent consumer/provider declaration comparison.

## What agreement means

The check first compares concrete examples; prospective matches must also meet
the contract checks described above. A consumer request needs a provider
request with the same relevant method, path, parameters, headers and body.
That provider exchange must also have the response the mock supplied: HTTP
status, application headers and body.

JSON object property order and equivalent numeric notation are immaterial:
`100`, `100.0` and `1e2` represent the same number. JSON values remain significant;
arrays retain order, and `null` differs from an absent field. Different synthetic
request IDs or payload values are different request scenarios. They are
reported as uncovered unless the provider has tested those concrete values.
There is no inference from a passing schema validation or an equal coverage
fingerprint to agreement on arbitrary values.

Absent and present-empty bodies differ. Arazzo inputs require eligible
JSON/REST exchanges; non-JSON bodies and invalid or duplicate-key JSON cannot
be certified through a partial export. HTTP header
names are case-insensitive, while method tokens and escaped paths retain their
case and values. Query key order is normalized, while repeated values retain
their order. Header values and repeated-header order remain significant unless
the header is ignored; semantically equivalent spellings are not inferred.
Arazzo export requires a JSON media type (`application/json` or a type ending
in `+json`) for nonempty bodies. Empty-body operations need a JSON media type
or a matching frozen OpenAPI operation.
The content-type header itself still participates in header comparison.
Request origin addresses are excluded, while paths are retained.
Requests and responses stay paired. A request from one provider test and a
response from another cannot jointly satisfy a mock. When the provider tested
the same request but no qualifying exchange returned the mocked status,
headers and value together, the result is a mismatch. When no qualifying
provider request exists, it is uncovered.

Capture origins and a narrow set of transport, tracing and client/server headers
are ignored because test addresses and capture plumbing vary between suites.
The defaults are `accept-encoding`, `baggage`, `connection`, `content-length`,
`date`, `host`,
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
These exclusions apply to captured header values. Declared OpenAPI request or
response header rules still participate in contract comparison.
A passing check means the supplied provider tests support these captured
examples. It does not prove that every possible request works, that the tests
asserted every captured field, or that a live deployment behaves identically.
Wiretap cannot infer which response fields the consumer actually read, so it
checks the recorded response rather than guessing a smaller required subset.

## Interpreting results

| Result | What the evidence establishes | Next action |
| --- | --- | --- |
| Verified | At least one complete passing provider exchange supports this consumer request and response, with the recorded `contract_check` assurance. | Retain the referenced archives; inspect example-only matches and keep testing additional scenarios separately. |
| Uncovered | No qualifying provider request matches the concrete normalized consumer request. | Inspect exact paths, query values, payloads and significant headers; add the provider case or align fixtures. |
| Mismatch | Examples or exercised HTTP declarations disagree, or a checked HTTP message violates its schema. | Investigate the linked example/contract difference, then correct the mock, provider or intended scenario. |
| Unverified | The selection, evidence or required contract checks cannot establish agreement. | Resolve the capture, identity, contract, target-kind or test-outcome problem and recapture as needed. |
| Input error | Supplied archives, Arazzo inputs or flags cannot form a valid comparison. | Read stderr, supply intact matching inputs and rerun. |

Uncovered is a test-evidence gap, not a statement that the provider would reject
the request. Mismatch is an example, rule or validation disagreement, not an
automatic determination that one repository is wrong or a runtime break
occurred. Unverified is a failure to establish
compatibility; it is not treated as a pass.

### Diagnostic codes

| Code | Meaning |
| --- | --- |
| `provider_request_uncovered` | No eligible provider exchange matches this request scenario. |
| `provider_response_mismatch` | Candidate provider exchanges do not supply the mocked response. |
| `http_contract_mismatch` | Exercised HTTP validation rules differ, including conservative enum-set or bound drift. |
| `http_contract_violation` | A response or qualifying request violates the applicable own or provider frozen HTTP contract. |
| `http_contract_unavailable` | Required HTTP contracts or rules are unavailable, unsupported or cannot be checked reliably. |
| `consumer_evidence_unavailable` | A selected consumer exchange is failed, incomplete, unsupported or otherwise unusable. |
| `consumer_run_incomplete` / `provider_run_incomplete` | The archive is not complete or has pending attempts. |
| `no_consumer_assumptions` | No recorded exchange was selected for the mock. |
| `provider_not_service` | The provider capture does not declare a service target. |
| `provider_identity_mismatch` | Declared application/API mapping conflicts with the supplied provider. |
| `provider_version_unverified` | A declared provider version is missing or different in recorded `sut_source.version`. |

Archive/Arazzo input errors are reported on stderr and do not produce
these finding codes or a new JSON result. An existing `--output` file is left
unchanged when input validation fails. Do not treat an old report as the
result of the current command; use its exit status as well.

### Read the exact evidence

An abbreviated explicit-pair JSON result for the inventory type mismatch above,
when both sides omitted their HTTP schemas, could contain:

```json
{
  "report_version": "1.0",
  "rules_version": "wiretap-assumptions-4",
  "mode": "consumer",
  "scope": "observed_mock_examples_and_exercised_contracts",
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

The full pair result also identifies both source archives, manifest digests,
Arazzo inputs, binding and ignored-header policy. With `--artifacts`, each
entry in `comparisons` has this same pair-report shape; the outer report supplies
the overall verdict and totals. An `evidence_path` is relative
to its source archive; the fragment selects the recorded flow in the attempt
file. Consumer and provider attempt IDs need not match each other.

`matches` records the provider witness chosen for each verified consumer
exchange. Its `contract_check` is `openapi_3_0_validation_rules` for exercised
HTTP rule equality and own-schema value checks,
`openapi_3_0_provider_contract` for validating both HTTP examples with the
provider's schema when the consumer omitted one,
or `unavailable` for an example-only HTTP match with both schemas absent or
omitted. The text summary calls out the example-only match count.
`excluded_provider_evidence` explains why other provider exchanges
could not serve as witnesses. Its presence alone does not fail a comparison.

The report is a diagnostic summary rather than an exhaustive diff. A finding
includes at most ten provider references and twenty differing paths. HTTP rule
differences are under `contract/request/...` or `contract/response/...`;
`contract/validation` identifies a value-validation failure. For a
response mismatch, paths compare the consumer response with the first candidate
provider response; they do not identify the closest candidate or enumerate
every disagreement across every candidate. Response mismatches also include
bounded expected/actual excerpts for those paths. Open the referenced captures
for the complete retained data.

## Use the result in CI

Text is the default. Use `--format markdown` for review artifacts or
`--format junit` for CI import, preserving the same verdict and exit status.
See [CI report formats](report-policies.md#send-assumption-checks-to-ci) for their
presentation limits. For a machine-readable result, select JSON and optionally
save it to a file:

```sh
wiretap check provider --provider ./inventory-run --artifacts ./api-evidence \
  --format json --output assumption-check.json
```

The output's parent directory must already exist. Choose a location outside
the entire `--artifacts` root and local input archive. In explicit pair mode,
keep it outside both archives and any supplied Arazzo input
directory; an output cannot replace a projection input or source companion.
For an explicit Arazzo file, keep output outside its containing export tree;
companions remain protected even when an early unverified verdict skips input
verification.

Pair, discovered-artifact and run-set results use `report_version: "1.0"` and
`rules_version: "wiretap-assumptions-4"`. `compatible` is the verdict, and
`counts` separates verified, uncovered, mismatched and unverified assumptions.
With `--artifacts`, the outer report uses
`scope: "discovered_consumer_provider_relationships"`, identifies `artifacts_path`,
and includes each pair report in `comparisons`. Counts are totals across those
comparisons. One incompatible comparison fails the overall check.

Each pair report uses `scope: "observed_mock_examples_and_exercised_contracts"`.
Its `matches` and `findings` preserve archive paths and test/flow evidence
references. Unverified counts can also include comparison-level blockers, such
as an interrupted archive or conflicting provider identity; they need not
partition the assumption count. Invalid discovery or pair inputs produce
stderr and no new JSON report. Keep the input archives alongside the result.
See the [machine interface](machine-interface.md#assumption-check-results) for
the result envelopes and the [CI example](ci-example.md#gate-consumerprovider-assumptions-offline)
for preserving the exit status and diagnostics.

| Exit status | Meaning |
| --- | --- |
| `0` | All selected mock scenarios have compatible provider evidence. |
| `1` | An assumption mismatches, is uncovered, or cannot be verified; operational failures can also return `1` with a stderr diagnostic. |
| `2` | Inputs or command flags are invalid. |

Use the command's exit status as the CI gate. For capture and export setup, see
[capture artifacts and exports](artifacts.md). For the separate measurement of
declared contract cases, see [contract coverage rules](contract-coverage-rules.md).
