const { test } = require("node:test");
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("../../sdk/js/src");

const RUN_E2E = Boolean(process.env.PHROND_BASE_URL || process.env.PHROND_HTTP_LISTEN);

if (!RUN_E2E) {
  test("hello world happy flow", { skip: "set PHROND_BASE_URL to run this example" }, () => {});
} else {
  const schemaConfig = process.env.PHROND_GENERATE_SCHEMA
    ? {
        generateSchema: {
          output: process.env.PHROND_GENERATED_SCHEMA_PATH,
          title: "Basic REST SUT",
          version: "1.0.0"
        }
      }
    : {
        schemas: [{
          id: "basic-rest-sut-openapi",
          role: "sut",
          type: "openapi",
          source: { type: "file", path: "examples/basic-rest/schemas/sut-openapi.yaml" }
        }]
      };
  const phrond = createNodeTestSuite({
    baseUrl: process.env.PHROND_BASE_URL || `http://${process.env.PHROND_HTTP_LISTEN}`,
    proxy: process.env.PHROND_PROXY_LISTEN ? { listenAddr: process.env.PHROND_PROXY_LISTEN } : true,
    sutBaseUrl: process.env.SUT_BASE_URL || `http://${process.env.SUT_LISTEN || "127.0.0.1:18090"}`,
    ...schemaConfig
  });

  phrond.test("hello world happy flow", async () => {
    const status = await phrond.http.get("/status");
    assert.deepEqual(status.body, { status: "ok" });

    const result = await phrond.http.postJson("/process", { id: "123", action: "process" });
    assert.equal(result.status, 200);
    assert.deepEqual(result.body, { message: "processed", id: "123", action: "process" });
  });
}
