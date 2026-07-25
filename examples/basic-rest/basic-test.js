const { test } = require("node:test");
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("../../sdk/js/src/index");

const RUN_E2E = Boolean(process.env.PHROND_BASE_URL || process.env.PHROND_HTTP_LISTEN);
const CAPTURE_LIMIT = Number(process.env.PHROND_MAX_CAPTURE_BYTES || 0);
const LARGE_BODY_BYTES = 512;

const SUT_SCHEMA = {
  id: "basic-rest-sut-openapi",
  role: "sut",
  type: "openapi",
  source: { type: "file", path: "examples/basic-rest/schemas/sut-openapi.yaml" }
};

// The dependency is declared once with a stable asset id — the cross-repo key
// a broker later uses to join this consumer's assumptions to the provider.
const DEPENDENCY = {
  assetId: "basic-rest-dependency",
  assetName: "Basic REST Dependency",
  schema: {
    id: "basic-rest-fake-openapi",
    type: "openapi",
    source: { type: "file", path: "examples/basic-rest/schemas/fake-openapi.yaml" }
  }
};

function resolveBaseUrl() {
  return (
    process.env.PHROND_BASE_URL ||
    (process.env.PHROND_HTTP_LISTEN ? `http://${process.env.PHROND_HTTP_LISTEN}` : "http://127.0.0.1:18000")
  );
}

if (!RUN_E2E) {
  test("basic REST flow through proxy and fake", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
  test("forwards a complete body while recording a bounded prefix", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
} else {
  // The runner starts and configures the SUT before this test process. This
  // helper owns only Phrond's capture components and per-test sessions.
  const phrond = createNodeTestSuite({
    baseUrl: resolveBaseUrl(),
    proxy: process.env.PHROND_PROXY_LISTEN ? { listenAddr: process.env.PHROND_PROXY_LISTEN } : true,
    mockServer: process.env.PHROND_MOCK_LISTEN ? { listenAddr: process.env.PHROND_MOCK_LISTEN } : true,
    schemas: [SUT_SCHEMA],
    dependencies: [DEPENDENCY],
    sutBaseUrl: process.env.SUT_BASE_URL || `http://${process.env.SUT_LISTEN || "127.0.0.1:18090"}`,
    strictSchema: true
  });

  phrond.test("basic REST flow through proxy and fake", async () => {
    const payload = { id: "123", action: "process" };

    await phrond.queueResponse({
      id: "alpha-basic-rest",
      match: { method: "POST", path: "/alpha", json: payload },
      response: {
        status: 200,
        json: { result: "alpha-ok" }
      }
    });

    await phrond.queueResponse({
      id: "beta-basic-rest",
      match: { method: "POST", path: "/beta", json: payload },
      response: {
        status: 200,
        json: { result: "beta-ok" }
      }
    });

    await phrond.http.get("/status");
    const result = await phrond.http.postJson("/process", payload);
    assert.equal(result.status, 200);

    const alphaResponse = await phrond.getResponse("alpha-basic-rest");
    const betaResponse = await phrond.getResponse("beta-basic-rest");
    assert.equal(alphaResponse.state, "responded");
    assert.equal(betaResponse.state, "responded");
    assert.equal(alphaResponse.asset_id, DEPENDENCY.assetId);
    assert.deepEqual(JSON.parse(alphaResponse.exchange.Request.Body), { id: "123", action: "process" });
    assert.deepEqual(JSON.parse(betaResponse.exchange.Request.Body), { id: "123", action: "process" });

    // Declare which mocked fields the SUT's behavior depends on, so later
    // analysis can tell breaking changes from changes to ignored fields.
    await phrond.verify("alpha-basic-rest", ["body.result"]);
  });

  test(
    "forwards a complete body while recording a bounded prefix",
    {
      skip:
        CAPTURE_LIMIT <= 0 || CAPTURE_LIMIT >= LARGE_BODY_BYTES
          ? "start Phrond with PHROND_MAX_CAPTURE_BYTES below 512"
          : false
    },
    async () => {
      await phrond.runtime.setupTest({ name: "bounded capture preserves delivery" });

      let teardown;
      try {
        const response = await phrond.http.get("/large");
        assert.equal(response.status, 200);
        assert.equal(response.body, "x".repeat(LARGE_BODY_BYTES));
        teardown = await phrond.runtime.teardownTest({ status: "pass" });
      } catch (err) {
        await phrond.runtime.teardownTest({ status: "fail" }).catch(() => {});
        throw err;
      }

      const flow = teardown.artifact.traffic.find(
        (candidate) => candidate.request?.url?.endsWith("/large")
      );
      assert.ok(flow, "expected /large flow in artifact");

      const captured = flow.response.body;
      assert.equal(captured.truncated, true);
      assert.equal(captured.original_bytes, LARGE_BODY_BYTES);
      assert.equal(captured.text, "x".repeat(CAPTURE_LIMIT));
    }
  );
}
