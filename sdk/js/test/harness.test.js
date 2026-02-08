const { test } = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");

const { start } = require("../src/index.js");

const INLINE_OPENAPI = [
  {
    id: "sut-openapi",
    role: "sut",
    type: "openapi",
    source: {
      type: "inline",
      content: "openapi: 3.0.0\ninfo:\n  title: t\n  version: 1\npaths: {}\n"
    }
  }
];

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

test("start initializes lifecycle and startup components with listen addresses", async () => {
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
    res.end(JSON.stringify({ status: "ok" }));
  });

  const phrond = await start({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    proxy: { listen_addr: "127.0.0.1:20010" },
    mockServer: { listenAddr: "127.0.0.1:20011" }
  });
  await phrond.stop({ writeArtifact: false });

  assert.equal(requests[0].path, "/v1/lifecycle/start");
  const proxyReq = requests.find((entry) => entry.path === "/v1/proxy/start");
  const mockReq = requests.find((entry) => entry.path === "/v1/mock/start");
  assert.deepEqual(JSON.parse(proxyReq.body), { listen_addr: "127.0.0.1:20010" });
  assert.deepEqual(JSON.parse(mockReq.body), { listen_addr: "127.0.0.1:20011" });
  assert.equal(requests.at(-1).path, "/v1/lifecycle/stop");

  await server.close();
});

test("mocking lazily starts a scenario and auto-generates a scenario name", async () => {
  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/mock/start") {
      res.end(JSON.stringify({ addr: "127.0.0.1:19101" }));
      return;
    }
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({
        path: "artifacts/case.yaml",
        schema_validation: { status: "pass", errors: null },
        artifact: { testcase: { status: "pass" } }
      }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });

  const phrond = await start({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    mockServer: true
  });

  await phrond.mock.http({ id: "stub-1" });
  await phrond.stop();

  const startReq = requests.find((entry) => entry.path === "/v1/testcases/start");
  const payload = JSON.parse(startReq.body);
  assert.ok(/^scenario-/.test(payload.name));
  assert.equal(payload.schemas.length, 1);

  await server.close();
});

test("scenarioName overrides generated scenario id", async () => {
  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/mock/start") {
      res.end(JSON.stringify({ addr: "127.0.0.1:19101" }));
      return;
    }
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({
        path: "artifacts/case.yaml",
        schema_validation: { status: "pass", errors: null },
        artifact: { testcase: { status: "pass" } }
      }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });

  const phrond = await start({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    mockServer: true
  });

  await phrond.scenarioName("submit-payment");
  await phrond.mock.http({ id: "stub-1" });
  await phrond.stop();

  const startReq = requests.find((entry) => entry.path === "/v1/testcases/start");
  const payload = JSON.parse(startReq.body);
  assert.equal(payload.name, "submit-payment");

  await server.close();
});

test("http helper sends traffic through proxy", async () => {
  const proxyRequests = [];
  const proxyServer = await startServer(async (req, res) => {
    const body = await readBody(req);
    proxyRequests.push({ method: req.method, path: req.url, host: req.headers.host, body });

    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ ok: true }));
  });

  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/proxy/start") {
      res.end(JSON.stringify({ listen_addr: proxyServer.url.replace("http://", "") }));
      return;
    }
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({
        path: "artifacts/case.yaml",
        schema_validation: { status: "pass", errors: null },
        artifact: { testcase: { status: "pass" } }
      }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });

  const phrond = await start({
    baseUrl: server.url,
    sutBaseUrl: "http://sut.local:18090",
    schemas: INLINE_OPENAPI,
    proxy: true
  });

  const resp = await phrond.http.postJson("/process?mode=test", { id: "123" });
  assert.equal(resp.status, 200);
  assert.deepEqual(resp.body, { ok: true });

  await phrond.stop();

  assert.deepEqual(proxyRequests.map((entry) => entry.path), ["http://sut.local:18090/process?mode=test"]);
  assert.equal(proxyRequests[0].host, "sut.local:18090");

  await proxyServer.close();
  await server.close();
});

test("stop throws when schema validation fails", async () => {
  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/mock/start") {
      res.end(JSON.stringify({ addr: "127.0.0.1:19101" }));
      return;
    }
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({
        path: "artifacts/case.yaml",
        schema_validation: {
          status: "fail",
          errors: [{ message: "unknown route" }]
        },
        artifact: { testcase: { status: "fail" } }
      }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });

  const phrond = await start({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    mockServer: true
  });

  await phrond.mock.http({ id: "stub-1" });

  await assert.rejects(() => phrond.stop(), /unknown route/);
  assert.equal(requests.at(-1).path, "/v1/lifecycle/stop");

  await server.close();
});

test("reset finalizes current scenario, writes artifact, and clears mocks", async () => {
  const requests = [];
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/mock/start") {
      res.end(JSON.stringify({ addr: "127.0.0.1:19101" }));
      return;
    }
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({
        path: "artifacts/case.yaml",
        schema_validation: { status: "pass", errors: null },
        artifact: { testcase: { status: "pass" } }
      }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });

  const phrond = await start({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    mockServer: true
  });

  await phrond.scenarioName("first");
  await phrond.mock.http({ id: "stub-1" });

  const resetFinish = await phrond.reset();
  assert.equal(resetFinish.path, "artifacts/case.yaml");

  await phrond.scenarioName("second");
  await phrond.mock.http({ id: "stub-2" });
  await phrond.stop();

  const testcaseStarts = requests.filter((entry) => entry.path === "/v1/testcases/start");
  const testcaseFinishes = requests.filter((entry) => entry.path === "/v1/testcases/finish");
  const clears = requests.filter((entry) => entry.path === "/v1/stubs/clear");

  assert.equal(testcaseStarts.length, 2);
  assert.equal(testcaseFinishes.length, 2);
  assert.equal(clears.length, 1);

  await server.close();
});

test("expectCall returns matching outbound SUT request", async () => {
  const server = await startServer(async (req, res) => {
    const body = await readBody(req);
    void body;

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/mock/start") {
      res.end(JSON.stringify({ addr: "127.0.0.1:19101" }));
      return;
    }
    if (req.url === "/v1/mock/exchanges") {
      res.end(
        JSON.stringify([
          {
            Request: {
              Method: "GET",
              Path: "/accounts/acct-100",
              URL: "/accounts/acct-100",
              Headers: {},
              Query: {},
              Body: ""
            },
            Response: {
              Status: 200,
              Headers: { "Content-Type": ["application/json"] },
              Body: '{"id":"acct-100"}'
            },
            MatchedStubID: "account-acct-100",
            Matched: true,
            StartedAt: "2026-02-08T19:00:00Z",
            FinishedAt: "2026-02-08T19:00:00Z"
          }
        ])
      );
      return;
    }
    if (req.url === "/v1/testcases/finish") {
      res.end(JSON.stringify({
        path: "artifacts/case.yaml",
        schema_validation: { status: "pass", errors: null },
        artifact: { testcase: { status: "pass" } }
      }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });

  const phrond = await start({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    mockServer: true
  });

  await phrond.mock.http({ id: "stub-1" });
  const match = await phrond.expectCall({ method: "GET", path: "/accounts/acct-100" });

  assert.equal(match.method, "GET");
  assert.equal(match.path, "/accounts/acct-100");
  assert.equal(match.matchedStubId, "account-acct-100");
  assert.equal(match.body, "");

  await phrond.stop();
  await server.close();
});
