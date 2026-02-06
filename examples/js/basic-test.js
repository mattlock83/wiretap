const { test, before, after, beforeEach, afterEach } = require("node:test");
const assert = require("node:assert/strict");
const { setupSuite } = require("../../sdk/js/src/index");
const { startSut } = require("./sut");

const SCHEMAS = [
  {
    id: "sut-openapi",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "examples/system/sut-openapi.yaml" }
  },
  {
    id: "mock-openapi",
    role: "fake",
    type: "openapi",
    source: { type: "file", path: "examples/system/mock-openapi.yaml" }
  }
];

let suite;
let sut;
let caseSession;
let caseCounter = 0;

before(async () => {
  const baseUrl =
    process.env.PHROND_BASE_URL ||
    (process.env.PHROND_HTTP_LISTEN ? `http://${process.env.PHROND_HTTP_LISTEN}` : "http://127.0.0.1:18000");
  const sutListen = process.env.SUT_LISTEN || "127.0.0.1:18090";

  suite = await setupSuite({ baseUrl, proxy: true, mockServer: true });
  const proxyListen = suite.runtime.proxy?.listen_addr || suite.runtime.proxy?.ListenAddr;
  const mockAddr = suite.runtime.mockServer?.addr;

  assert.ok(proxyListen, "proxy listener address must be returned");
  assert.ok(mockAddr, "mock server address must be returned");

  sut = await startSut({
    listen: sutListen,
    dependencyBaseUrl: `http://${mockAddr}`
  });
});

after(async () => {
  await sut?.stop();
  await suite?.teardown();
});

beforeEach(async () => {
  caseCounter += 1;
  caseSession = await suite.setupTest({
    name: `basic-${caseCounter}`,
    meta: { suite: "examples" },
    sutBaseUrl: `http://${sut.listen}`,
    schemas: SCHEMAS
  });
});

afterEach(async () => {
  if (!caseSession) return;
  const finish = await caseSession.teardown({ status: "pass" });
  caseSession = null;

  assert.ok(typeof finish.path === "string" && finish.path.length > 0, "artifact path should be returned");
  if (finish.schemaValidation) {
    assert.equal(finish.schemaValidation.status, "pass");
  }
});

test("basic SUT flow through proxy + mocks", async () => {
  const payload = JSON.stringify({ id: "123", action: "process" });
  await caseSession.mockHttp({
    id: "alpha-basic",
    match: { method: "POST", path: "/alpha", body: payload },
    response: {
      status: 200,
      body: JSON.stringify({ result: "alpha-ok" }),
      headers: { "Content-Type": "application/json" }
    }
  });
  await caseSession.mockHttp({
    id: "beta-basic",
    match: { method: "POST", path: "/beta", body: payload },
    response: {
      status: 200,
      body: JSON.stringify({ result: "beta-ok" }),
      headers: { "Content-Type": "application/json" }
    }
  });

  await caseSession.get("/status");
  const result = await caseSession.postJson("/process", { id: "123", action: "process" });
  assert.equal(result.status, 200);

  await caseSession.assertField({
    flow_id: "alpha-basic",
    channel: "sut_to_mock",
    phase: "response",
    path_lang: "json",
    field: "result",
    operator: "equals",
    expected: "alpha-ok"
  });
  await caseSession.assertField({
    flow_id: "beta-basic",
    channel: "sut_to_mock",
    phase: "response",
    path_lang: "json",
    field: "result",
    operator: "equals",
    expected: "beta-ok"
  });
});
