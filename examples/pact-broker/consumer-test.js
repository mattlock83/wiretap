const { test } = require("node:test");
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("../../sdk/js/src");

if (!process.env.PHROND_BASE_URL) {
  test("orders and inventory Pact consumer example", {
    skip: "run ./examples/run-pact-broker-e2e.sh"
  }, () => {});
} else {
  const phrond = createNodeTestSuite({
    baseUrl: process.env.PHROND_BASE_URL,
    proxy: { listenAddr: process.env.PHROND_PROXY_LISTEN },
    sutBaseUrl: process.env.ORDERS_BASE_URL,
    consumerName: "orders-service",
    schemas: [{
      id: "orders-openapi",
      role: "sut",
      type: "openapi",
      source: { type: "file", path: "examples/pact-broker/schemas/orders.openapi.yaml" }
    }],
    dependencies: {
      inventory: {
        assetName: "Inventory Service",
        upstreamUrl: process.env.INVENTORY_UPSTREAM_URL,
        schema: {
          id: "inventory-openapi",
          type: "openapi",
          source: { type: "file", path: "examples/pact-broker/schemas/inventory.openapi.yaml" }
        }
      }
    }
  });

  async function assertAvailableOrder() {
    const response = await phrond.http.postJson("/orders", {
      productId: "coffee-beans", quantity: 2
    });
    assert.equal(response.status, 201);
    assert.deepEqual(response.body, {
      orderId: "order-coffee-beans",
      productId: "coffee-beans",
      quantity: 2,
      status: "accepted"
    });
    await phrond.assertDependency("inventory", {
      method: "GET",
      path: "/inventory/products/coffee-beans",
      providerState: {
        name: "product is available",
        params: { productId: "coffee-beans" }
      },
      response: {
        "$.productId": "coffee-beans",
        "$.available": true
      }
    });
  }

  phrond.test("accepts an order when the product is available", assertAvailableOrder);
  phrond.test("repeated available-product scenarios become one Pact interaction", assertAvailableOrder);

  phrond.test("rejects an order when the product is unavailable", async () => {
    await assert.rejects(
      phrond.http.postJson("/orders", { productId: "decaf", quantity: 1 }),
      (error) => error.status === 409 && error.body.productId === "decaf"
    );
    await phrond.assertDependency("inventory", {
      method: "GET",
      path: "/inventory/products/decaf",
      providerState: {
        name: "product is unavailable",
        params: { productId: "decaf" }
      },
      response: {
        "$.productId": "decaf",
        "$.available": false
      }
    });
  });
}
