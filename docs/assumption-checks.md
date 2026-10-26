# Check consumer and provider assumptions

`wiretap check` compares a consumer's captured mock calls with the provider's
captured tests. It reads complete Wiretap archives and their Arazzo workflows
for JSON/REST APIs offline.

```sh
wiretap check --consumer ./orders-run --provider ./inventory-run \
  --dependency inventory
```

**A pass means every selected captured mock exchange has a complete, passing
provider test exchange supporting the same concrete request and response.**
Supported frozen contract rules must agree where available. The check does not
start servers, replay requests, execute workflow expressions or generate tests.

For separate suites or CI jobs in the same build, use
[run sets](run-sets.md) with `--consumer-set` and `--provider-set`.

## What a passing result establishes

For each captured call to the selected mock, Wiretap:

1. Requires complete consumer evidence from a passing test attempt.
2. Finds complete passing provider exchanges with the same normalized request.
3. Requires the mocked response in one of those same exchanges.
4. Validates each exchange against the applicable frozen OpenAPI 3.0 contract.
   When both sides supply contracts, exercised validation rules must also agree.
5. Verifies both Arazzo documents and their companions against projections
   regenerated from their original archives.

An empty selection cannot pass. Additional provider scenarios do not create
consumer obligations. If a provider has tested both `200` and `404` for one
request, either response can support its corresponding consumer mock. A pass
establishes at least one supporting example, not that every execution returns
that response.

### Contract assurance

| Contracts available | Meaning of a verified match |
| --- | --- |
| Both sides | Each exchange validates under its own contract and exercised declarations agree. |
| Provider only | Both exchanges validate under the provider contract; no independent consumer declaration comparison. |
| Neither | Example-only agreement, marked `contract_check: "unavailable"`. |
| Consumer only | Unverified: a consumer contract cannot replace provider contract evidence. |

The main API requires a spec by default. Consumer dependencies can omit theirs;
a later check can use the provider's frozen spec without changing the original
consumer coverage report. A provider spec alone never replaces a matching
passing provider test.

## Issues the check will find

| Issue | Outcome |
| --- | --- |
| No provider example for the operation, concrete path, query, request body or significant headers | `uncovered` |
| Matching requests exist, but status, response body, headers or trailers differ | `mismatch` |
| Equal examples have different exercised types, enum sets, requiredness, nullability or constraints | Contract mismatch |
| A checked message violates its applicable contract | Contract violation |
| Selected consumer evidence is failed, incomplete, truncated or unavailable | `unverified` |
| Provider evidence is failed, incomplete or unavailable | Excluded; another complete passing exchange must support the mock |
| Run is interrupted/open, provider is not a service, or a declared identity/version disagrees | `unverified` |
| Archive integrity fails, or Arazzo documents/companions are missing, stale or altered | Input error |

For example, a mock returning `{"quantity":3}` and a provider returning
`{"quantity":"3"}` for the same request produce a response mismatch. If the
provider tested a different item ID or query value, the consumer request is
instead uncovered.

One full exchange must support the example. A provider test returning
`{"available":true,"status":"pending"}` and another returning
`{"available":false,"status":"ready"}` cannot jointly support a mock returning
`{"available":true,"status":"ready"}`.

### Contract rules

With both contracts present, Wiretap compares request parameters, the selected
request body/media type, and the selected response status, media type, body
schema and declared headers. Nested optional properties and composition
participate even if a property is absent from the example. References resolve
within the frozen document graph. Documentation, examples, defaults and enum or
required-name ordering are ignored.

This is conservative rule equality. Enum additions, relaxed bounds or equivalent
schemas expressed differently can fail without proving a runtime break.
Unexercised operations, other response statuses and unselected media types are
not statically compared.

Every response is validated. Requests are validated when their response status
is below `400`; error responses can represent deliberate negative request tests.
Request declarations are still compared when both contracts exist. Framework
assertions do not override contract violations.

Unsupported required rules remain unverified: OpenAPI 3.1, media-type `encoding`,
`content`-encoded parameters/response headers, complex numeric object
serialization, path/operation-level server overrides, and nullable schemas
combined with enum, `not` or composition. Ambiguous header casing or multiple
`Content-Type` values also prevent a reliable check. The loader validates the
full frozen graph, so malformed or precision-unsafe rules elsewhere in it can
prevent contract loading. Projection depth and node budgets are bounded.

## What the check will not establish

- That all possible requests, states or response branches are compatible.
- That two fixtures have the same hidden database state, tenant or clock.
- Which response fields the consumer reads, or whether its tests asserted them.
- Whether an optional extra field or relaxed constraint breaks the consumer.
- Whether retries, ordering, call counts, side effects or workflow execution are correct.
- Whether unexecuted mocks, uncaptured traffic or unselected dependencies agree.
- Whether a live deployment, security configuration or performance is correct.

Payload values are concrete. There are no wildcard IDs, ignored payload fields
or response-subset comparisons. Keep behavior assertions and expected call
counts in Jest. Use [contract coverage](contract-coverage-rules.md) separately to
inspect declared cases and measurement limits.

## Keep the evidence together

Retain the complete run: `bundle.json`, `tests/`, `contracts/`, `payloads/` and
`exports/arazzo/`. The archives supply actual response bodies and headers,
passing-test provenance, provider mappings and frozen schemas. Coverage reports
and workflow documents alone do not replace those inputs.

### Arazzo check inputs

Default documents are `<run>/exports/arazzo/arazzo.json`, with their relative
source companions. Regenerate from the exact intended archive when needed:

```sh
wiretap report --bundle ./consumer-run --format arazzo \
  --output-dir ./consumer-run/exports/arazzo
wiretap report --bundle ./provider-run --format arazzo \
  --output-dir ./provider-run/exports/arazzo
wiretap check --consumer ./consumer-run --provider ./provider-run
```

Explicit pairs can select another projection location:

```sh
wiretap check --consumer ./consumer-run --provider ./provider-run \
  --consumer-arazzo ./consumer-workflows/arazzo.json \
  --provider-arazzo ./provider-workflows
```

Documents must match the archive-derived projection. Editing requests, omitting
workflows or replacing source companions cannot change the evidence accepted by
the checker. Arazzo expressions and success criteria are never executed.

Selected boundaries must contain eligible complete JSON/REST traffic. A partial
export cannot certify omitted unsupported traffic. Export/regeneration is strict
across the whole run: incomplete or unrepresentable eligible traffic elsewhere
can prevent checking, including on an excluded provider attempt. Preserve and
inspect the source archive when generation fails.

Run sets use each member's default projection paths and do not accept per-member
overrides. A member with no eligible workflows may omit its document; an existing
document is stale when the archive generates none.

## Share provider snapshots

Upload and download complete archives using your existing CI artifact tooling.
Choose exact intended builds and pass their paths explicitly. Extract ZIP/TAR
files before checking and preserve referenced directories. Wiretap does not
fetch snapshots or choose a latest run.

Stable identities and optional provider mappings record the intended relationship:

```yaml
# Consumer configuration
application: {id: orders}
sut: {id: orders-api, schema: schemas/orders.openapi.yaml}
dependencies:
  inventory: {url: '${INVENTORY_MOCK_URL}', kind: mock}
provider_mappings:
  inventory: {application_id: inventory-service, api_id: inventory-http, version: v2}
```

```yaml
# Provider configuration
application: {id: inventory-service}
sut: {id: inventory-http, schema: schemas/inventory.openapi.yaml, kind: service}
sut_source: {version: v2, revision: '${BUILD_REVISION}'}
```

An explicit pair also works without a mapping. If a mapping is present, the
provider application's ID, API ID and any declared version must agree. Version
pins require the exact captured `sut_source.version`; unknown is not agreement.

## Select the dependency and provider

`--dependency` names the consumer's local mock dependency. Omit it when exactly
one mock or one matching provider mapping makes selection unambiguous. With
several mocks, select the intended boundary explicitly. Repeat the command for
other boundaries.

The supplied provider archive must capture a SUT declared `kind: service`.
Provider and consumer archives must be different captures. Keep comparable
synthetic fixture values and tested build provenance; test names and identities
need not match across applications.

## What agreement means

HTTP origins and local ports are ignored. Method, encoded path, query values,
body presence, status and significant headers/trailers participate. JSON objects
compare independent of key order; numbers compare precisely, including large
integers. Array order, values, null and absence remain significant. Bodies with
non-JSON declarations compare bytes, while Arazzo eligibility still requires
JSON/REST traffic for the selected boundary.

Transport/framing and tracing headers such as `Host`, `Content-Length`, `Date`,
`Traceparent` and `User-Agent` are ignored. Application headers remain
significant. Additional exclusions are explicit and repeatable:

```sh
wiretap check --consumer ./consumer-run --provider ./provider-run \
  --dependency inventory --ignore-header X-Request-Id
```

Exclusions affect captured-value comparison, not declared contract rules.
The result records the effective `ignored_headers` list.

## Interpreting results

| Result | Meaning |
| --- | --- |
| Verified match | One complete passing provider exchange supports the selected consumer example. |
| `uncovered` | No qualifying provider request matches. |
| `mismatch` | Matching provider requests disagree in response or contract rules. |
| `unverified` | Evidence or a required check cannot establish agreement. |

Use `compatible` as the verdict; finding counts need not partition assumption
counts because run-level failures can be independent of individual exchanges.
`matches` retains one supporting witness per verified example. Each evidence
reference names its run, attempt, flow and JSON path. Follow those references
for exact retained values.

### Diagnostic codes

The [machine interface](machine-interface.md#finding-codes) lists codes such as
`provider_request_uncovered`, `provider_response_mismatch`,
`http_contract_mismatch`, `http_contract_violation` and
`http_contract_unavailable`. Findings contain bounded difference paths and value
excerpts; the comparison itself uses complete retained evidence.

### Read the exact evidence

Text presents verdict, example totals and assurance counts. JSON retains the
complete report, including excluded provider evidence, source identities,
manifest digests, Arazzo paths, test identities and matched evidence pairs.

## Use the result in CI

```sh
mkdir -p check-results
wiretap check --consumer ./orders-run --provider ./inventory-run \
  --dependency inventory --format json --output check-results/inventory.json
```

The output parent must exist. Keep the destination outside source archives and
Arazzo export trees; it must not replace a source document, companion or run-set
manifest. Input validation leaves an existing report unchanged, so retain stderr
and the exit status and use a fresh output path for each check.

Exit `0` means all selected assumptions are supported. Exit `1` means the check
found uncovered, mismatched or unverified evidence, or an operational failure
prevented completion. Exit `2` means invalid flags or inputs. Keep both suites'
Jest exit statuses as independent gates. See the [CI example](ci-example.md).
