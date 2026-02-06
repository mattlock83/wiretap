const { test } = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");

const { createHarness, setupSuite } = require("../src/index.js");

async function startServer(handler) {
  const server = http.createServer(handler);
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address();
  return {
    url: `http://127.0.0.1:${port}`,
    close: () => new Promise((resolve) => server.close(resolve))
  };
}

function readBody(req) {
  return new Promise((resolve) => {
    let data = "";
    req.on("data", (chunk) => (data += chunk));
    req.on("end", () => resolve(data));
  });
}

test("startLifecycle posts to lifecycle endpoint", async () => {
  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ status: "ok" }));
  });

  const harness = createHarness({ baseUrl: server.url });
  const payload = await harness.startLifecycle();

  assert.equal(payload.status, "ok");
  assert.equal(requests.length, 1);
  assert.equal(requests[0].method, "POST");
  assert.equal(requests[0].path, "/v1/lifecycle/start");

  await server.close();
});

test("registerStub posts payload", async () => {
  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ status: "ok" }));
  });

  const harness = createHarness({ baseUrl: server.url });
  await harness.registerStub({ id: "stub-1" });

  assert.equal(requests.length, 1);
  assert.equal(requests[0].path, "/v1/stubs");
  assert.equal(requests[0].body, JSON.stringify({ id: "stub-1" }));

  await server.close();
});

test("errors include status and body", async () => {
  const server = await startServer(async (req, res) => {
    res.statusCode = 500;
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ error: "boom" }));
  });

  const harness = createHarness({ baseUrl: server.url });
  await assert.rejects(() => harness.healthz(), (err) => {
    assert.equal(err.status, 500);
    assert.deepEqual(err.body, { error: "boom" });
    return true;
  });

  await server.close();
});

test("waitForStatus polls until inactive", async () => {
  let callCount = 0;
  const server = await startServer(async (req, res) => {
    callCount += 1;
    res.setHeader("content-type", "application/json");
    if (callCount < 3) {
      res.end(JSON.stringify({ active: true }));
      return;
    }
    res.end(JSON.stringify({ active: false, last_status: "pass" }));
  });

  const harness = createHarness({ baseUrl: server.url, timeoutMs: 5000 });
  const status = await harness.waitForStatus({ intervalMs: 10, timeoutMs: 1000 });

  assert.equal(status.active, false);
  assert.equal(status.last_status, "pass");

  await server.close();
});

test("downloadArtifact saves file", async () => {
  const server = await startServer(async (req, res) => {
    res.setHeader("content-type", "application/yaml");
    res.end("version: 1\n");
  });

  const harness = createHarness({ baseUrl: server.url });
  const { mkdtemp, readFile } = require("node:fs/promises");
  const { tmpdir } = require("node:os");
  const { join } = require("node:path");

  const dir = await mkdtemp(join(tmpdir(), "phrond-"));
  const filePath = join(dir, "artifact.yaml");

  await harness.downloadArtifact(filePath);

  const content = await readFile(filePath, "utf8");
  assert.equal(content, "version: 1\n");

  await server.close();
});

test("suite session API follows setup/test/mock/teardown phases", async () => {
  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });
    res.setHeader("content-type", "application/json");

    if (req.url === "/v1/proxy/start") {
      res.end(JSON.stringify({ listen_addr: "127.0.0.1:19100" }));
      return;
    }
    if (req.url === "/v1/mock/start") {
      res.end(JSON.stringify({ addr: "127.0.0.1:19101" }));
      return;
    }
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({ status: "pass", path: "artifacts/case.yaml" }));
      return;
    }

    res.end(JSON.stringify({ status: "ok" }));
  });

  const harness = createHarness({ baseUrl: server.url });
  const suite = await harness.setupSuite({
    proxy: true,
    mockServer: { listen_addr: "127.0.0.1:0" }
  });

  assert.equal(suite.runtime.proxy.listen_addr, "127.0.0.1:19100");
  assert.equal(suite.runtime.mockServer.addr, "127.0.0.1:19101");

  const testCase = await suite.setupTest({
    name: "basic",
    meta: { suite: "examples" },
    schemas: [
      {
        id: "sut-openapi",
        role: "sut",
        type: "openapi",
        source: { type: "inline", content: "openapi: 3.0.0\ninfo:\n  title: t\n  version: 1\npaths: {}\n" }
      }
    ]
  });

  await testCase.mockHttp({ id: "http-1" });
  await testCase.mockGrpc({ id: "grpc-1" });
  await testCase.expectCall({ id: "exp-1" });
  await testCase.assertField({ id: "assert-1" });
  await testCase.clearMocks();
  const finish = await testCase.teardown({ status: "pass" });
  await suite.teardown();

  assert.equal(finish.status, "pass");
  assert.deepEqual(
    requests.map((r) => r.path),
    [
      "/v1/lifecycle/start",
      "/v1/proxy/start",
      "/v1/mock/start",
      "/v1/testcases/start",
      "/v1/stubs",
      "/v1/grpc/stubs",
      "/v1/expectations",
      "/v1/assertions",
      "/v1/stubs/clear",
      "/v1/grpc/stubs/clear",
      "/v1/testcases/finish",
      "/v1/lifecycle/stop"
    ]
  );

  await server.close();
});

test("test request helpers route traffic through proxy", async () => {
  const proxyRequests = [];
  const proxyServer = await startServer(async (req, res) => {
    const body = await readBody(req);
    proxyRequests.push({
      method: req.method,
      path: req.url,
      host: req.headers.host,
      body
    });
    res.setHeader("content-type", "application/json");
    if (req.url.includes("/status")) {
      res.end(JSON.stringify({ status: "ok" }));
      return;
    }
    res.end(JSON.stringify({ ok: true }));
  });

  const coreRequests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    coreRequests.push({ method: req.method, path: req.url, body });
    res.setHeader("content-type", "application/json");

    if (req.url === "/v1/proxy/start") {
      res.end(JSON.stringify({ listen_addr: proxyServer.url.replace("http://", "") }));
      return;
    }
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({ path: "artifacts/case.yaml" }));
      return;
    }

    res.end(JSON.stringify({ status: "ok" }));
  });

  const harness = createHarness({ baseUrl: server.url });
  const suite = await harness.setupSuite({ proxy: true });
  const testCase = await suite.setupTest({
    name: "proxy-requests",
    meta: { suite: "examples" },
    sutBaseUrl: "http://sut.local:18090",
    schemas: [
      {
        id: "sut-openapi",
        role: "sut",
        type: "openapi",
        source: { type: "inline", content: "openapi: 3.0.0\ninfo:\n  title: t\n  version: 1\npaths: {}\n" }
      }
    ]
  });

  const statusResp = await testCase.get("/status");
  const processResp = await testCase.postJson("/process?mode=test", { id: "123" });
  await testCase.teardown({ status: "pass" });
  await suite.teardown();

  assert.equal(statusResp.status, 200);
  assert.deepEqual(statusResp.body, { status: "ok" });
  assert.equal(processResp.status, 200);
  assert.deepEqual(processResp.body, { ok: true });
  assert.deepEqual(
    proxyRequests.map((req) => req.path),
    ["http://sut.local:18090/status", "http://sut.local:18090/process?mode=test"]
  );
  assert.equal(proxyRequests[1].host, "sut.local:18090");
  assert.equal(proxyRequests[1].body, JSON.stringify({ id: "123" }));
  assert.deepEqual(
    coreRequests.map((r) => r.path),
    ["/v1/lifecycle/start", "/v1/proxy/start", "/v1/testcases/start", "/v1/testcases/finish", "/v1/lifecycle/stop"]
  );

  await server.close();
  await proxyServer.close();
});

test("request helpers require sutBaseUrl", async () => {
  const server = await startServer(async (req, res) => {
    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/proxy/start") {
      res.end(JSON.stringify({ listen_addr: "127.0.0.1:19100" }));
      return;
    }
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({ path: "artifacts/case.yaml" }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });

  const harness = createHarness({ baseUrl: server.url });
  const suite = await harness.setupSuite({ proxy: true });
  const testCase = await suite.setupTest({
    name: "missing-sut-base-url",
    meta: {},
    schemas: [
      {
        id: "sut-openapi",
        role: "sut",
        type: "openapi",
        source: { type: "inline", content: "openapi: 3.0.0\ninfo:\n  title: t\n  version: 1\npaths: {}\n" }
      }
    ]
  });

  await assert.rejects(() => testCase.get("/status"), /sutBaseUrl is required/);
  await testCase.teardown({ status: "pass" });
  await suite.teardown();
  await server.close();
});

test("request helpers require proxy component", async () => {
  const server = await startServer(async (req, res) => {
    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({ path: "artifacts/case.yaml" }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });

  const harness = createHarness({ baseUrl: server.url });
  const suite = await harness.setupSuite();
  const testCase = await suite.setupTest({
    name: "missing-proxy",
    meta: {},
    sutBaseUrl: "http://sut.local:18090",
    schemas: [
      {
        id: "sut-openapi",
        role: "sut",
        type: "openapi",
        source: { type: "inline", content: "openapi: 3.0.0\ninfo:\n  title: t\n  version: 1\npaths: {}\n" }
      }
    ]
  });

  await assert.rejects(() => testCase.get("/status"), /proxy is not configured/);
  await testCase.teardown({ status: "pass" });
  await suite.teardown();
  await server.close();
});

test("top-level setupSuite helper starts lifecycle", async () => {
  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ status: "ok" }));
  });

  const suite = await setupSuite({ baseUrl: server.url });
  await suite.teardown();

  assert.deepEqual(
    requests.map((r) => r.path),
    ["/v1/lifecycle/start", "/v1/lifecycle/stop"]
  );

  await server.close();
});

test("setupSuite tears down lifecycle when component startup fails", async () => {
  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    if (req.url === "/v1/proxy/start") {
      res.statusCode = 500;
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify({ error: "proxy failed" }));
      return;
    }

    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ status: "ok" }));
  });

  const harness = createHarness({ baseUrl: server.url });
  await assert.rejects(() => harness.setupSuite({ proxy: true }), (err) => {
    assert.equal(err.status, 500);
    assert.deepEqual(err.body, { error: "proxy failed" });
    return true;
  });

  assert.deepEqual(
    requests.map((r) => r.path),
    ["/v1/lifecycle/start", "/v1/proxy/start", "/v1/lifecycle/stop"]
  );

  await server.close();
});

test("test teardown can download artifact", async () => {
  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    if (req.url === "/v1/artifact") {
      res.setHeader("content-type", "application/yaml");
      res.end("version: 1\n");
      return;
    }

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({ status: "pass" }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });

  const harness = createHarness({ baseUrl: server.url });
  const suite = await harness.setupSuite();
  const testCase = await suite.setupTest({
    name: "artifact-download",
    meta: { suite: "examples" },
    schemas: [
      {
        id: "sut-openapi",
        role: "sut",
        type: "openapi",
        source: { type: "inline", content: "openapi: 3.0.0\ninfo:\n  title: t\n  version: 1\npaths: {}\n" }
      }
    ]
  });

  const { mkdtemp, readFile } = require("node:fs/promises");
  const { tmpdir } = require("node:os");
  const { join } = require("node:path");
  const dir = await mkdtemp(join(tmpdir(), "phrond-suite-"));
  const filePath = join(dir, "artifact.yaml");

  const finish = await testCase.teardown({ status: "pass", downloadArtifactTo: filePath });
  await suite.teardown();

  assert.equal(finish.status, "pass");
  assert.equal(finish.downloadedArtifact.path, filePath);
  assert.equal(await readFile(filePath, "utf8"), "version: 1\n");
  assert.deepEqual(
    requests.map((r) => r.path),
    [
      "/v1/lifecycle/start",
      "/v1/testcases/start",
      "/v1/testcases/finish",
      "/v1/artifact",
      "/v1/lifecycle/stop"
    ]
  );

  await server.close();
});
