# Orders, inventory, and a real local Pact Broker

This example demonstrates the complete consumer/provider contract lifecycle
between two independently owned services:

- `examples/demo-apps/pact-orders` is the consumer. `POST /orders` checks its
  inventory dependency before accepting or rejecting an order.
- `examples/demo-apps/pact-inventory` is the provider. It serves
  `GET /inventory/products/{productId}` and owns the standard Pact
  provider-state endpoint used during verification.
- Phrond captures both the test→orders and orders→inventory HTTP exchanges but
  starts and configures neither service.
- The official open-source Pact Broker imports the generated Pact V3 contract.
- The official Pact CLI retrieves the published contract, applies its provider
  states to the inventory service, verifies both interactions, and publishes
  the provider verification result back to the broker.

Docker, Go, Node.js 18+, and a running Docker daemon are required:

```bash
./examples/run-pact-broker-e2e.sh
```

The runner starts a disposable Pact Broker using its officially supported
SQLite development configuration. No PostgreSQL service, Pact library, or
additional Go dependency is required. The official Broker and Pact CLI Docker
images are downloaded automatically if they are not already available.

Three named consumer tests become only two provider interactions because the
two available-product scenarios are equivalent. Their `assertDependency`
calls retain only `productId` and `available` in the provider contract, even
though the full inventory response—including `quantity` and `warehouse`—is
still present in Phrond's capture artifact.

The runner prints the artifact directory and writes:

```text
artifacts/pact-run-.../
  pacts/orders-service-inventory-service.json
  published-pact.json
  provider-verification.json
  <one full correlated capture artifact per consumer test>
```

`provider-verification.json` is fetched back from the Broker and checked for a
successful result against the expected provider application version.

To run only the real Broker import test against an already running Broker:

```bash
PACT_BROKER_BASE_URL=http://127.0.0.1:9292 \
  go test ./internal/coverage -run '^TestPactBrokerImportsExportedContract$' -v
```

Without `PACT_BROKER_BASE_URL`, the integration test skips so normal
`go test ./...` runs remain Docker-free. SQLite is intended only for disposable
local development; use PostgreSQL for a real shared or production Pact Broker.
