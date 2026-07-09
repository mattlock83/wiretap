const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { startServer, bindTest } = require("../../sdk/js/src/index");
const { startBasicRestSUT } = require("./test-helpers");

const RUN_E2E = Boolean(process.env.PHROND_BASE_URL || process.env.PHROND_HTTP_LISTEN);

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

let runtime;
let sut;

// bindTest wraps node:test so each test gets an auto-named session and a
// truthful pass/fail artifact status.
const phrondTest = bindTest(() => runtime, test);

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
      schemas: [SUT_SCHEMA],
      dependencies: [DEPENDENCY],
      sutBaseUrl: `http://${sutListen}`,
      strictSchema: true
    });

    // The dependency URL is stable across tests: configure the SUT once and
    // never re-point it (no per-test rewiring, no forwarding shim).
    sut = await startBasicRestSUT({
      listen: sutListen,
      dependencyBaseUrl: runtime.dependencyUrl(DEPENDENCY.assetId)
    });
  });

  after(async () => {
    const errors = [];
    try {
      await sut?.stop?.();
    } catch (err) {
      errors.push(err);
    }
    try {
      await runtime?.killServer();
    } catch (err) {
      errors.push(err);
    }
    sut = null;
    runtime = null;
    if (errors.length > 0) {
      throw errors[0];
    }
  });

  phrondTest("basic REST flow through proxy and fake", async () => {
    const payload = JSON.stringify({ id: "123", action: "process" });

    await runtime.queueResponse({
      id: "alpha-basic-rest",
      match: { method: "POST", path: "/alpha", body: payload },
      response: {
        status: 200,
        body: JSON.stringify({ result: "alpha-ok" }),
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
    assert.equal(alphaResponse.asset_id, DEPENDENCY.assetId);
    assert.deepEqual(JSON.parse(alphaResponse.exchange.Request.Body), { id: "123", action: "process" });
    assert.deepEqual(JSON.parse(betaResponse.exchange.Request.Body), { id: "123", action: "process" });

    // Declare which mocked fields the SUT's behavior depends on, so later
    // analysis can tell breaking changes from changes to ignored fields.
    await runtime.verify("alpha-basic-rest", ["body.result"]);
  });
}
