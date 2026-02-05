const { createHarness } = require("../../sdk/js/src/index");

async function main() {
  const harness = createHarness({ baseUrl: "http://127.0.0.1:18000" });

  await harness.startTestCase("basic", { suite: "examples" });

  await harness.startProxy({});
  await harness.startMockServer({});

  await harness.registerStub({
    id: "hello-stub",
    match: { method: "GET", path: "/hello" },
    response: { status: 200, body: "hi" }
  });

  await harness.assertField({
    channel: "sut_to_mock",
    phase: "response",
    field: "body",
    operator: "exists",
    path_lang: "jsonpath"
  });

  const artifact = await harness.finishTestCase("pass");
  console.log("artifact:", artifact.path);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
