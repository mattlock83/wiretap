const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { startCapture } = require("../../sdk/js/src");

const RUN_E2E = Boolean(process.env.WIRETAP_BASE_URL || process.env.WIRETAP_HTTP_LISTEN);

if (!RUN_E2E) {
  test("hello world happy flow", { skip: "set WIRETAP_BASE_URL to run this example" }, () => {});
} else {
  let wiretap;
  before(async () => {
    wiretap = await startCapture();
  });
  after(async () => { if (wiretap) await wiretap.finishRun(); });

  test("hello world happy flow", () => wiretap.runTest("hello world happy flow", async () => {
    const status = await fetch(`${wiretap.sutUrl}/status`);
    assert.deepEqual(await status.json(), { status: "ok" });

    const result = await fetch(`${wiretap.sutUrl}/process`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ id: "123", action: "process" }) });
    assert.equal(result.status, 200);
    assert.deepEqual(await result.json(), { message: "processed", id: "123", action: "process" });

    // Generated checks are awaited by this ordinary node:test case. Stateful
    // applications can reset fixtures in beforeCase and release them in afterCase.
    await wiretap.explore({
      operations: ["POST /process"],
      hooks: {
        checkCase: {
          operations: ["POST /process"],
          handler: async event => ({ checks: [{
            name: "the provider returned a response",
            passed: event.exchanges.length > 0
          }] })
        }
      }
    });
  }));
}
