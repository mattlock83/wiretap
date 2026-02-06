const { createHarness } = require("../../sdk/js/src/index");
const http = require("node:http");
const { spawn } = require("node:child_process");

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitForHealth(url, retries = 20) {
  for (let i = 0; i < retries; i += 1) {
    try {
      const res = await fetch(url);
      if (res.ok) {
        return true;
      }
    } catch (_) {
      // ignore
    }
    await sleep(200);
  }
  return false;
}

function postViaProxy(proxyUrl, targetUrl, body) {
  return new Promise((resolve, reject) => {
    const proxy = new URL(proxyUrl);
    const target = new URL(targetUrl);

    const req = http.request(
      {
        host: proxy.hostname,
        port: proxy.port,
        method: "POST",
        path: target.href,
        headers: {
          Host: target.host,
          "Content-Type": "application/json",
          "Content-Length": Buffer.byteLength(body)
        }
      },
      (res) => {
        let data = "";
        res.on("data", (chunk) => (data += chunk));
        res.on("end", () => resolve({ status: res.statusCode, body: data }));
      }
    );
    req.on("error", reject);
    req.write(body);
    req.end();
  });
}

async function main() {
  const baseUrl =
    process.env.PHROND_BASE_URL ||
    (process.env.PHROND_HTTP_LISTEN
      ? `http://${process.env.PHROND_HTTP_LISTEN}`
      : "http://127.0.0.1:18000");
  const harness = createHarness({ baseUrl });

  await harness.startTestCase("basic", { suite: "examples" }, {
    schemas: [
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
    ]
  });

  const proxyInfo = await harness.startProxy({});
  const mockInfo = await harness.startMockServer({});

  await harness.registerStub({
    id: "alpha",
    match: { method: "POST", path: "/alpha" },
    response: {
      status: 200,
      body: JSON.stringify({ result: "alpha-ok" }),
      headers: { "Content-Type": "application/json" }
    }
  });

  await harness.registerStub({
    id: "beta",
    match: { method: "POST", path: "/beta" },
    response: {
      status: 200,
      body: JSON.stringify({ result: "beta-ok" }),
      headers: { "Content-Type": "application/json" }
    }
  });

  const sutListen = process.env.SUT_LISTEN || "127.0.0.1:18090";
  const sutProcessUrl = `http://${sutListen}/process`;

  const sut = spawn("go", ["run", "./examples/system/sut"], {
    stdio: "inherit",
    env: {
      ...process.env,
      SUT_LISTEN: sutListen,
      DEPENDENCY_BASE_URL: `http://${mockInfo.addr}`
    }
  });

  const healthy = await waitForHealth(`http://${sutListen}/healthz`);
  if (!healthy) {
    sut.kill();
    throw new Error("SUT failed to start");
  }

  const payload = JSON.stringify({ id: "123", action: "process" });
  const proxyListen = proxyInfo.listen_addr || proxyInfo.ListenAddr;
  if (!proxyListen) {
    throw new Error("Proxy listen address not returned");
  }
  const result = await postViaProxy(`http://${proxyListen}`, sutProcessUrl, payload);
  console.log("SUT response:", result.status, result.body);

  await harness.assertField({
    flow_id: "alpha",
    channel: "sut_to_mock",
    phase: "request",
    path_lang: "json",
    field: "id",
    operator: "equals",
    expected: "123"
  });

  await harness.assertField({
    flow_id: "alpha",
    channel: "sut_to_mock",
    phase: "response",
    path_lang: "json",
    field: "result",
    operator: "equals",
    expected: "alpha-ok"
  });

  await harness.assertField({
    flow_id: "beta",
    channel: "sut_to_mock",
    phase: "request",
    path_lang: "json",
    field: "action",
    operator: "equals",
    expected: "process"
  });

  await harness.assertField({
    flow_id: "beta",
    channel: "sut_to_mock",
    phase: "response",
    path_lang: "json",
    field: "result",
    operator: "equals",
    expected: "beta-ok"
  });

  const finish = await harness.finishTestCase("pass");
  if (finish.schemaValidation) {
    console.log("schema validation:", finish.schemaValidation.status, "errors:", finish.schemaValidation.errors?.length || 0);
  }
  console.log("artifact:", finish.path);

  sut.kill();
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
