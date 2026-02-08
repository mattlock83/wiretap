const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { createRuntime, caseConfig, assertFinishPass } = require("./test-helpers");

let runtime;

before(async () => {
  runtime = await createRuntime();
});

after(async () => {
  await runtime?.sut?.stop();
  await runtime?.suite?.teardown();
});

test("basic REST flow through proxy and fake", async () => {
  const { finish } = await runtime.suite.withTest(
    caseConfig("basic-rest", runtime.sut.listen),
    async (testCase) => {
      const payload = JSON.stringify({ id: "123", action: "process" });

      await testCase.mockHttp({
        id: "alpha-basic-rest",
        match: { method: "POST", path: "/alpha", body: payload },
        response: {
          status: 200,
          body: JSON.stringify({ result: "alpha-ok" }),
          headers: { "Content-Type": "application/json" }
        }
      });

      await testCase.mockHttp({
        id: "beta-basic-rest",
        match: { method: "POST", path: "/beta", body: payload },
        response: {
          status: 200,
          body: JSON.stringify({ result: "beta-ok" }),
          headers: { "Content-Type": "application/json" }
        }
      });

      await testCase.get("/status");
      const result = await testCase.postJson("/process", { id: "123", action: "process" });
      assert.equal(result.status, 200);
    }
  );

  assertFinishPass(finish);
});
