const { test, before, beforeEach, afterEach, after } = require("node:test");
const assert = require("node:assert/strict");
const { startRuntime, setupBasicCase, resetBasicCase } = require("./test-helpers");

const RUN_E2E = Boolean(process.env.PHROND_BASE_URL || process.env.PHROND_HTTP_LISTEN);

let runtime;
let sut;

if (!RUN_E2E) {
  test("basic REST flow through proxy and fake", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
} else {
  before(async () => {
    runtime = await startRuntime();
  });

  beforeEach(async (t) => {
    const prepared = await setupBasicCase(runtime, { name: t.name });
    sut = prepared.sut;
  });

  afterEach(async () => {
    await resetBasicCase(runtime, sut);
    sut = null;
  });

  after(async () => {
    await runtime?.kill();
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

    const alphaResponse = await runtime.inspectResponse("alpha-basic-rest");
    const betaResponse = await runtime.inspectResponse("beta-basic-rest");
    assert.equal(alphaResponse.state, "responded");
    assert.equal(betaResponse.state, "responded");
    assert.deepEqual(JSON.parse(alphaResponse.exchange.Request.Body), { id: "123", action: "process" });
    assert.deepEqual(JSON.parse(betaResponse.exchange.Request.Body), { id: "123", action: "process" });
  });
}
