const { test, before, beforeEach, afterEach, after } = require("node:test");
const assert = require("node:assert/strict");
const { startServer } = require("../../sdk/js/src/index");
const { startBasicRestSUT } = require("./test-helpers");

const RUN_E2E = Boolean(process.env.PHROND_BASE_URL || process.env.PHROND_HTTP_LISTEN);

const SCHEMAS = [
  {
    id: "basic-rest-sut-openapi",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "examples/basic-rest/schemas/sut-openapi.yaml" }
  },
  {
    id: "basic-rest-fake-openapi",
    role: "fake",
    type: "openapi",
    source: { type: "file", path: "examples/basic-rest/schemas/fake-openapi.yaml" }
  }
];

let runtime;
let sut;

function resolveBaseUrl() {
  return (
    process.env.PHROND_BASE_URL ||
    (process.env.PHROND_HTTP_LISTEN ? `http://${process.env.PHROND_HTTP_LISTEN}` : "http://127.0.0.1:18000")
  );
}

if (!RUN_E2E) {
  test("basic REST flow through proxy and fake", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
} else {
  before(async () => {
    const baseUrl = resolveBaseUrl();
    const sutListen = process.env.SUT_LISTEN || "127.0.0.1:18090";
    runtime = await startServer({
      baseUrl,
      proxy: process.env.PHROND_PROXY_LISTEN ? { listenAddr: process.env.PHROND_PROXY_LISTEN } : true,
      mockServer: process.env.PHROND_MOCK_LISTEN ? { listenAddr: process.env.PHROND_MOCK_LISTEN } : true,
      schemas: SCHEMAS,
      sutBaseUrl: `http://${sutListen}`,
      strictSchema: true
    });
  });

  beforeEach(async (t) => {
    const sutListen = process.env.SUT_LISTEN || "127.0.0.1:18090";
    const session = await runtime.setupTest({ name: t.name });
    sut = await startBasicRestSUT({
      listen: sutListen,
      dependencyBaseUrl: session.mockBaseUrl
    });
  });

  afterEach(async () => {
    const errors = [];
    try {
      await runtime.teardownTest({ status: "pass" });
    } catch (err) {
      errors.push(err);
    }
    try {
      await sut?.stop?.();
    } catch (err) {
      errors.push(err);
    }
    sut = null;
    if (errors.length > 0) {
      throw errors[0];
    }
  });

  after(async () => {
    await runtime?.killServer();
  });

  test("basic REST flow through proxy and fake", async () => {
    const payload = JSON.stringify({ id: "123", action: "process" });

    await runtime.queueResponse({
      id: "alpha-basic-rest",
      match: { method: "POST", path: "/alpha", body: payload },
      response: {
        status: 200,
        bodyFile: "examples/basic-rest/templates/alpha-response.tmpl.json",
        headers: { "Content-Type": "application/json" }
      }
    });

    await runtime.queueResponse({
      id: "beta-basic-rest",
      match: { method: "POST", path: "/beta", body: payload },
      response: {
        status: 200,
        body: JSON.stringify({ result: "beta-ok" }),
        headers: { "Content-Type": "application/json" }
      }
    });

    await runtime.http.get("/status");
    const result = await runtime.http.postJson("/process", { id: "123", action: "process" });
    assert.equal(result.status, 200);

    const alphaResponse = await runtime.getResponse("alpha-basic-rest");
    const betaResponse = await runtime.getResponse("beta-basic-rest");
    assert.equal(alphaResponse.state, "responded");
    assert.equal(betaResponse.state, "responded");
    assert.deepEqual(JSON.parse(alphaResponse.exchange.Request.Body), { id: "123", action: "process" });
    assert.deepEqual(JSON.parse(betaResponse.exchange.Request.Body), { id: "123", action: "process" });
  });
}
