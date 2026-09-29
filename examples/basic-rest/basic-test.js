const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { startCapture } = require("../../sdk/js/src");

const RUN_E2E = Boolean(process.env.PHROND_BASE_URL || process.env.PHROND_HTTP_LISTEN);

if (!RUN_E2E) {
  test("hello world happy flow", { skip: "set PHROND_BASE_URL to run this example" }, () => {});
} else {
  let phrond;
  before(async () => {
    phrond = await startCapture();
  });
  after(async () => { if (phrond) await phrond.finishRun(); });

  test("hello world happy flow", () => phrond.runTest("hello world happy flow", async () => {
    const status = await fetch(`${phrond.sutUrl}/status`);
    assert.deepEqual(await status.json(), { status: "ok" });

    const result = await fetch(`${phrond.sutUrl}/process`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ id: "123", action: "process" }) });
    assert.equal(result.status, 200);
    assert.deepEqual(await result.json(), { message: "processed", id: "123", action: "process" });
  }));
}
