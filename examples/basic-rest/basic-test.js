const { test, beforeEach, afterEach } = require("node:test");
const assert = require("node:assert/strict");
const { createRuntime } = require("./test-helpers");

let runtime;

beforeEach(async () => {
  runtime = await createRuntime();
});

afterEach(async () => {
  await runtime?.stop();
  await runtime?.sut?.stop();
});

test("basic REST flow through proxy and fake", async () => {
  await runtime.scenarioName("basic-rest");

  const payload = JSON.stringify({ id: "123", action: "process" });

  await runtime.mock.http({
    id: "alpha-basic-rest",
    match: { method: "POST", path: "/alpha", body: payload },
    response: {
      status: 200,
      body: JSON.stringify({ result: "alpha-ok" }),
      headers: { "Content-Type": "application/json" }
    }
  });

  await runtime.mock.http({
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

  const alphaCall = await runtime.expectCall({ method: "POST", path: "/alpha", times: 1 });
  const betaCall = await runtime.expectCall({ method: "POST", path: "/beta", times: 1 });
  assert.deepEqual(JSON.parse(alphaCall.body), { id: "123", action: "process" });
  assert.deepEqual(JSON.parse(betaCall.body), { id: "123", action: "process" });
});
