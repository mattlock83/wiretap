const { test } = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");

const { createHarness } = require("../src/index.js");

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
