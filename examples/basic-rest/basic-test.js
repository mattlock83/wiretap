const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { startCapture, bindTest } = require("../../sdk/js/src");

const RUN_E2E = Boolean(process.env.WIRETAP_BASE_URL || process.env.WIRETAP_HTTP_LISTEN);

if (!RUN_E2E) {
  test("hello world happy flow", { skip: "set WIRETAP_BASE_URL to run this example" }, () => {});
} else {
  let wiretap;
  before(async () => {
    wiretap = await startCapture();
  });
  after(async () => {
    if (!wiretap) return;
    const result = await wiretap.finishRun();
    if (process.env.WIRETAP_RESULT_FILE) require("node:fs").writeFileSync(process.env.WIRETAP_RESULT_FILE, JSON.stringify(result));
  });

  bindTest(() => wiretap, test)("hello world happy flow", async (_t, session) => {
    const status = await fetch(`${session.sutUrl}/status`);
    assert.deepEqual(await status.json(), { status: "ok" });

    const result = await fetch(`${session.sutUrl}/process`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ id: "123", action: "process" }) });
    assert.equal(result.status, 200);
    assert.deepEqual(await result.json(), { message: "processed", id: "123", action: "process" });
  });
}
