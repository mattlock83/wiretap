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

function requestViaProxy(proxyUrl, targetUrl, { method = "GET", body = null, headers = {} } = {}) {
  return new Promise((resolve, reject) => {
    const proxy = new URL(proxyUrl);
    const target = new URL(targetUrl);

    const reqHeaders = { Host: target.host, ...headers };
    if (body !== null && body !== undefined) {
      reqHeaders["Content-Length"] = Buffer.byteLength(body);
      if (!reqHeaders["Content-Type"]) {
        reqHeaders["Content-Type"] = "application/json";
      }
    }

    const req = http.request(
      {
        host: proxy.hostname,
        port: proxy.port,
        method,
        path: target.href,
        headers: reqHeaders
      },
      (res) => {
        let data = "";
        res.on("data", (chunk) => (data += chunk));
        res.on("end", () => resolve({ status: res.statusCode, body: data }));
      }
    );
    req.on("error", reject);
    if (body !== null && body !== undefined) {
      req.write(body);
    }
    req.end();
  });
}

function postViaProxy(proxyUrl, targetUrl, body) {
  return requestViaProxy(proxyUrl, targetUrl, { method: "POST", body });
}

function stopProcess(proc, timeoutMs = 2000) {
  return new Promise((resolve) => {
    if (!proc) {
      resolve();
      return;
    }
    if (proc.exitCode !== null) {
      resolve();
      return;
    }

    let finished = false;
    const finish = () => {
      if (finished) return;
      finished = true;
      resolve();
    };

    const timer = setTimeout(() => {
      if (proc.exitCode === null) {
        proc.kill("SIGKILL");
        const killTimer = setTimeout(finish, 1000);
        proc.once("exit", () => {
          clearTimeout(killTimer);
          finish();
        });
      } else {
        finish();
      }
    }, timeoutMs);

    proc.once("exit", () => {
      clearTimeout(timer);
      finish();
    });

    proc.kill("SIGTERM");
  });
}

async function runCase(harness, { name, payload, alphaResult, betaResult }) {
  await harness.startTestCase(name, { suite: "examples" }, {
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
    id: `alpha-${name}`,
    match: { method: "POST", path: "/alpha", body: payload },
    response: {
      status: 200,
      body: JSON.stringify({ result: alphaResult }),
      headers: { "Content-Type": "application/json" }
    }
  });

  await harness.registerStub({
    id: `beta-${name}`,
    match: { method: "POST", path: "/beta", body: payload },
    response: {
      status: 200,
      body: JSON.stringify({ result: betaResult }),
      headers: { "Content-Type": "application/json" }
    }
  });

  const sutListen = process.env.SUT_LISTEN || "127.0.0.1:18090";
  const sutProcessUrl = `http://${sutListen}/process`;
  const sutBinary = process.env.SUT_BINARY;
  const sutCommand = sutBinary || "go";
  const sutArgs = sutBinary ? [] : ["run", "./examples/system/sut"];

  const sut = spawn(sutCommand, sutArgs, {
    stdio: "inherit",
    env: {
      ...process.env,
      SUT_LISTEN: sutListen,
      DEPENDENCY_BASE_URL: `http://${mockInfo.addr}`
    }
  });

  try {
    const healthy = await waitForHealth(`http://${sutListen}/healthz`);
    if (!healthy) {
      throw new Error("SUT failed to start");
    }

    const proxyListen = proxyInfo.listen_addr || proxyInfo.ListenAddr;
    if (!proxyListen) {
      throw new Error("Proxy listen address not returned");
    }
    const sutStatusUrl = `http://${sutListen}/status`;
    await requestViaProxy(`http://${proxyListen}`, sutStatusUrl, { method: "GET" });
    const result = await postViaProxy(`http://${proxyListen}`, sutProcessUrl, payload);
    console.log("SUT response:", result.status, result.body);

    await harness.assertField({
      flow_id: `alpha-${name}`,
      channel: "sut_to_mock",
      phase: "response",
      path_lang: "json",
      field: "result",
      operator: "equals",
      expected: alphaResult
    });

    await harness.assertField({
      flow_id: `beta-${name}`,
      channel: "sut_to_mock",
      phase: "response",
      path_lang: "json",
      field: "result",
      operator: "equals",
      expected: betaResult
    });

    const finish = await harness.finishTestCase("pass");
    if (finish.schemaValidation) {
      console.log("schema validation:", finish.schemaValidation.status, "errors:", finish.schemaValidation.errors?.length || 0);
    }
    console.log("artifact:", finish.path);
  } finally {
    await stopProcess(sut);
  }
}

async function main() {
  const baseUrl =
    process.env.PHROND_BASE_URL ||
    (process.env.PHROND_HTTP_LISTEN
      ? `http://${process.env.PHROND_HTTP_LISTEN}`
      : "http://127.0.0.1:18000");
  const harness = createHarness({ baseUrl });

  await runCase(harness, {
    name: "basic",
    payload: JSON.stringify({ id: "123", action: "process" }),
    alphaResult: "alpha-ok",
    betaResult: "beta-ok"
  });

  await runCase(harness, {
    name: "alternate",
    payload: JSON.stringify({ id: "456", action: "review" }),
    alphaResult: "alpha-alt",
    betaResult: "beta-alt"
  });
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
