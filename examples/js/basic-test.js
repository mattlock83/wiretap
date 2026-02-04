const { createHarness } = require("../../sdk/js/src/index");

async function main() {
  const harness = createHarness({ baseUrl: "http://127.0.0.1:18000" });

  await harness.startLifecycle();

  const health = await harness.healthz();
  console.log("healthz:", health);

  await harness.stopLifecycle();
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
