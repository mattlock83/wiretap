# Pact export and Pact Broker publishing

Phrond can turn captured SUT→dependency traffic into standard Pact
Specification V3 consumer contracts without becoming a mock server, replacing
existing stubs, or running provider verification.

## Capture a consumer interaction

Keep the existing SUT and mock-server setup, declare the dependency and its
OpenAPI schema, and optionally assert the response fields that matter:

```js
const { createNodeTestSuite } = require("@phrond/sdk");

const phrond = createNodeTestSuite({
  baseUrl: process.env.PHROND_BASE_URL,
  sutBaseUrl: process.env.SUT_BASE_URL,
  consumerName: "orders-service",
  schemas: [ordersOpenAPI],
  dependencies: {
    inventory: {
      upstreamUrl: process.env.WIREMOCK_BASE_URL,
      schema: inventoryOpenAPI
    }
  }
});

phrond.test("creates an order for available stock", async () => {
  const result = await phrond.http.postJson("/orders", {
    productId: "product-123"
  });

  await phrond.assertDependency("inventory", {
    method: "GET",
    path: "/inventory/product-123",
    providerState: {
      name: "product exists and is available",
      params: { productId: "product-123" }
    },
    response: {
      "$.productId": "product-123",
      "$.available": true
    }
  });
});
```

If the existing mock returns additional fields, such as internal warehouse
metadata, they remain in the full capture artifact and HTML report but are
not added to this Pact interaction. Without `assertDependency`, the complete
captured dependency response is included. Schema-derived type, integer,
boolean, UUID, date, date-time, and pattern matchers keep unasserted
full-response contracts resilient to dynamic values. Explicit assertions
retain exact equality semantics.

`providerState` is optional. It accepts a string or `{ name, params }`; use
`providerStates` for several states. These values are standard Pact provider
verification prerequisites, not commands sent to the existing mock server.

## Export one contract per provider

From the SDK:

```js
const exported = await phrond.report({
  pactDir: "artifacts/pacts",
  providerNames: { inventory: "inventory-service" }
});

console.log(exported.pact_paths);
console.log(exported.consumer_version, exported.branch);
```

From the standalone coverage CLI:

```bash
go run ./cmd/phrond-coverage \
  --artifacts ./artifacts \
  --consumer-name orders-service \
  --provider-name inventory=inventory-service \
  --format pact \
  --output ./artifacts/pacts
```

The output is one regular Pact JSON file for each consumer/provider pair:

```text
artifacts/pacts/orders-service-inventory-service.json
artifacts/pacts/orders-service-payments-service.json
```

Dependency `asset_id` is the default provider participant name. Override names
only when the actual Pact Broker participant uses a different name. The
consumer defaults to the explicitly supplied SUT name or the producing
repository name, but setting `consumerName`/`--consumer-name` is preferable
when multiple repositories or deployment units share naming conventions.

The detected Git commit SHA and branch are returned by the report API for
publication. Artifact directories containing different producer repositories
or consumer commit SHAs are rejected; a Pact publication must describe one
consumer application version.

## Deduplication and safety

- Only successful `sut_to_dependency` HTTP exchanges are exported. Failed
  tests, test→SUT exchanges, and upstream transport failures are excluded.
- All tests are merged into one contract per consumer/provider pair.
- Interactions with equivalent requests, responses, matching rules, and
  provider states are deduplicated even when different tests captured them.
- Distinct query values, request bodies, response expectations, and provider
  states remain distinct. Colliding descriptions receive deterministic
  suffixes so Pact Broker cannot collapse different scenarios.
- Headers containing authorization, cookies, API keys, proxy state, tracing,
  transport metadata, and other run-specific values are omitted. Common
  credential query parameters are also omitted.
- Truncated or binary bodies fail export explicitly; increase
  `PHROND_MAX_CAPTURE_BYTES` or use `0` for unlimited retention when a complete
  HTTP contract is required.
- Existing artifact redaction still applies before export. Review captured
  request/response bodies for domain-specific secrets or personal data.

## Publish using the standard Pact tooling

Use the normal Pact Broker CLI after all test artifacts or CI shards have been
collected and exported:

```bash
pact-broker publish ./artifacts/pacts \
  --consumer-app-version "$GIT_COMMIT" \
  --branch "$GIT_BRANCH"
```

Use the same Git-based application version when this service publishes consumer
contracts and when it publishes its own provider verification results. Publish
the complete merged directory once per consumer version; independently
publishing partial contracts for the same consumer/provider/version can
overwrite another shard or be rejected by the broker.

Provider teams continue to fetch and verify contracts using their existing Pact
provider tooling. Phrond neither starts the provider nor assumes ownership of
Pact Broker.
