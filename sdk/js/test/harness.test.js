const { test } = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");

const { startServer, createNodeTestSuite } = require("../src/index.js");

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

async function startHarnessServer(handler) {
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

test("startServer initializes runtime with configured listen addresses", async (t) => {
  const requests = [];
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" },
          stack_size: 100
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    proxy: { listen_addr: "127.0.0.1:20010" },
    mockServer: { listenAddr: "127.0.0.1:20011" }
  });
  await runtime.killServer();

  assert.equal(requests[0].path, "/v1/startServer");
  assert.deepEqual(JSON.parse(requests[0].body), {
    proxy: { listen_addr: "127.0.0.1:20010" },
    mock: { listen_addr: "127.0.0.1:20011" }
  });
});

test("report forwards Postman, workflow, and HTML output paths", async (t) => {
  const requests = [];
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({ baseUrl: server.url });
  await runtime.report({
    artifactsDir: "artifacts/run-1",
    sutName: "Example SUT",
    collectionPath: "artifacts/run-1/coverage.postman.json",
    workflowPath: "artifacts/run-1/coverage.workflow.yaml",
    htmlPath: "artifacts/run-1/coverage.html"
  });
  await runtime.killServer();

  const request = requests.find((entry) => entry.path === "/v1/report");
  assert.deepEqual(JSON.parse(request.body), {
    artifacts_dir: "artifacts/run-1",
    sut_name: "Example SUT",
    include_artifacts: false,
    collection_path: "artifacts/run-1/coverage.postman.json",
    workflow_path: "artifacts/run-1/coverage.workflow.yaml",
    html_path: "artifacts/run-1/coverage.html"
  });
});

test("scenarioName is applied when setup is auto-started by queueResponse", async (t) => {
  const requests = [];
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      const payload = JSON.parse(body || "{}");
      res.end(
        JSON.stringify({
          session_id: "session-1",
          name: payload.name || "scenario-1",
          proxy_listen: "http://127.0.0.1:19100",
          mock_base_url: "http://127.0.0.1:19101/__phrond/session/session-1"
        })
      );
      return;
    }
    if (req.url === "/v1/queue-response") {
      res.end(JSON.stringify({ status: "queued", id: "stub-1", session_id: "session-1" }));
      return;
    }
    if (req.url === "/v1/teardownTest") {
      res.end(
        JSON.stringify({
          path: "artifacts/case.yaml",
          schema_validation: { status: "pass", errors: null },
          artifact: { testcase: { status: "pass" } }
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    sutBaseUrl: "http://sut.local:18090",
    proxy: true,
    mockServer: true
  });

  runtime.scenarioName("submit-payment");
  await runtime.queueResponse({
    id: "stub-1",
    match: { method: "GET", path: "/accounts/acct-100" },
    response: { status: 200, body: "{\"id\":\"acct-100\"}" }
  });
  await runtime.teardownTest();
  await runtime.killServer();

  const setupReq = requests.find((entry) => entry.path === "/v1/setupTest");
  assert.equal(JSON.parse(setupReq.body).name, "submit-payment");
});

test("queueResponse forwards default response flag", async (t) => {
  const requests = [];
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      res.end(
        JSON.stringify({
          session_id: "session-default-1",
          name: "default-flow",
          proxy_listen: "http://127.0.0.1:19100",
          mock_base_url: "http://127.0.0.1:19101/__phrond/session/session-default-1"
        })
      );
      return;
    }
    if (req.url === "/v1/queue-response") {
      res.end(JSON.stringify({ status: "queued", id: "stub-default", session_id: "session-default-1", default: true }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    sutBaseUrl: "http://sut.local:18090",
    proxy: true,
    mockServer: true
  });

  await runtime.setupTest({ name: "default-flow" });
  await runtime.queueResponse({
    id: "stub-default",
    default: true,
    match: { method: "GET", path: "/accounts/acct-100" },
    response: { status: 200, body: "{\"id\":\"acct-100\"}" }
  });
  await runtime.killServer();

  const queueReq = requests.find((entry) => entry.path === "/v1/queue-response");
  const queueBody = JSON.parse(queueReq.body);
  assert.equal(queueBody.default, true);
});

test("setupTest forwards recordTraffic option", async (t) => {
  const requests = [];
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      const payload = JSON.parse(body || "{}");
      res.end(
        JSON.stringify({
          session_id: "session-performance-1",
          name: payload.name,
          record_traffic: payload.record_traffic,
          proxy_listen: "http://127.0.0.1:19100",
          mock_base_url: "http://127.0.0.1:19101/__phrond/session/session-performance-1"
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    proxy: true,
    mockServer: true
  });

  const session = await runtime.setupTest({ name: "performance-flow", recordTraffic: false });
  await runtime.killServer();

  const setupReq = requests.find((entry) => entry.path === "/v1/setupTest");
  assert.equal(JSON.parse(setupReq.body).record_traffic, false);
  assert.equal(session.recordTraffic, false);
});

test("setupTest normalizes external mock dependency configuration", async (t) => {
  const requests = [];
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ path: req.url, body });
    res.setHeader("content-type", "application/json");

    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      res.end(
        JSON.stringify({
          session_id: "session-external-1",
          name: "external-flow",
          record_traffic: true,
          dependency_urls: {
            inventory: "http://127.0.0.1:19101/__phrond/dep/inventory"
          }
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    dependencies: [
      {
        assetId: "inventory",
        assetName: "Inventory",
        mock: {
          mode: "external",
          upstreamUrl: "http://127.0.0.1:18089",
          preserveHost: true
        }
      }
    ]
  });
  await runtime.setupTest({ name: "external-flow" });
  await runtime.killServer();

  const setupReq = requests.find((entry) => entry.path === "/v1/setupTest");
  const dependencies = JSON.parse(setupReq.body).dependencies;
  assert.deepEqual(dependencies, [
    {
      asset_id: "inventory",
      asset_name: "Inventory",
      mock: {
        mode: "external",
        upstream_url: "http://127.0.0.1:18089",
        preserve_host: true
      }
    }
  ]);
});

test("http helper sends reverse-mode traffic to proxy with session header", async (t) => {
  const proxyRequests = [];
  const proxyServer = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    proxyRequests.push({
      method: req.method,
      path: req.url,
      session: req.headers["x-phrond-session-id"],
      body
    });
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ ok: true }));
  });
  t.after(() => proxyServer.close());

  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    void body;
    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: proxyServer.url.replace("http://", "") },
          sut_url: proxyServer.url,
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      res.end(
        JSON.stringify({
          session_id: "session-2",
          name: "scenario-2",
          proxy_listen: proxyServer.url,
          sut_url: proxyServer.url,
          mock_base_url: "http://127.0.0.1:19101"
        })
      );
      return;
    }
    if (req.url === "/v1/teardownTest") {
      res.end(
        JSON.stringify({
          path: "artifacts/case.yaml",
          schema_validation: { status: "pass", errors: null },
          artifact: { testcase: { status: "pass" } }
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    sutBaseUrl: "http://sut.local:18090",
    proxy: true,
    mockServer: true
  });

  await runtime.setupTest({ name: "proxy-flow" });
  const resp = await runtime.http.postJson("/process?mode=test", { id: "123" });
  assert.equal(resp.status, 200);
  assert.deepEqual(resp.body, { ok: true });

  await runtime.teardownTest();
  await runtime.killServer();

  // Reverse mode: a plain relative-path request; the proxy resolves the target.
  assert.equal(proxyRequests.length, 1);
  assert.equal(proxyRequests[0].path, "/process?mode=test");
  assert.equal(proxyRequests[0].session, "session-2");
});

test("http helper uses forward-proxy style for an overridden target", async (t) => {
  const proxyRequests = [];
  const proxyServer = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    proxyRequests.push({
      method: req.method,
      path: req.url,
      host: req.headers.host,
      session: req.headers["x-phrond-session-id"],
      body
    });
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ ok: true }));
  });
  t.after(() => proxyServer.close());

  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    void body;
    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: proxyServer.url.replace("http://", "") },
          sut_url: proxyServer.url,
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      res.end(
        JSON.stringify({
          session_id: "session-2b",
          name: "scenario-2b",
          proxy_listen: proxyServer.url,
          sut_url: proxyServer.url,
          mock_base_url: "http://127.0.0.1:19101"
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    sutBaseUrl: "http://sut.local:18090",
    proxy: true,
    mockServer: true
  });

  await runtime.setupTest({ name: "forward-flow" });
  const resp = await runtime.http.get("/transactions", { sutBaseUrl: "http://other.local:18111" });
  assert.equal(resp.status, 200);
  await runtime.killServer();

  // Overridden target: absolute-URI forward-proxy request.
  assert.equal(proxyRequests.length, 1);
  assert.equal(proxyRequests[0].path, "http://other.local:18111/transactions");
  assert.equal(proxyRequests[0].host, "other.local:18111");
  assert.equal(proxyRequests[0].session, "session-2b");
});

test("teardownTest throws when schema validation fails", async (t) => {
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    void body;
    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      res.end(
        JSON.stringify({
          session_id: "session-3",
          name: "scenario-3",
          proxy_listen: "http://127.0.0.1:19100",
          mock_base_url: "http://127.0.0.1:19101/__phrond/session/session-3"
        })
      );
      return;
    }
    if (req.url === "/v1/teardownTest") {
      res.end(
        JSON.stringify({
          path: "artifacts/case.yaml",
          schema_validation: {
            status: "fail",
            errors: [{ message: "unknown route" }]
          },
          artifact: { testcase: { status: "fail" } }
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    sutBaseUrl: "http://sut.local:18090",
    proxy: true,
    mockServer: true
  });

  await runtime.setupTest({ name: "validation-fail" });
  await assert.rejects(() => runtime.teardownTest(), /unknown route/);
  await runtime.killServer();
});

test("teardownTest finalizes current scenario and does not create a new one", async (t) => {
  const requests = [];
  let sessionCounter = 0;
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ method: req.method, path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      sessionCounter += 1;
      const payload = JSON.parse(body || "{}");
      const id = `session-${sessionCounter}`;
      res.end(
        JSON.stringify({
          session_id: id,
          name: payload.name || `scenario-${sessionCounter}`,
          proxy_listen: "http://127.0.0.1:19100",
          mock_base_url: `http://127.0.0.1:19101/__phrond/session/${id}`
        })
      );
      return;
    }
    if (req.url === "/v1/teardownTest") {
      res.end(
        JSON.stringify({
          path: `artifacts/case-${sessionCounter}.yaml`,
          schema_validation: { status: "pass", errors: null },
          artifact: { testcase: { status: "pass" } }
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    sutBaseUrl: "http://sut.local:18090",
    proxy: true,
    mockServer: true
  });

  await runtime.setupTest({ name: "first" });
  const teardownResult = await runtime.teardownTest();
  assert.equal(teardownResult.path, "artifacts/case-1.yaml");
  await runtime.killServer();

  const setupCalls = requests.filter((entry) => entry.path === "/v1/setupTest");
  const teardownCalls = requests.filter((entry) => entry.path === "/v1/teardownTest");
  assert.equal(setupCalls.length, 1);
  assert.equal(teardownCalls.length, 1);
});

test("list and inspect helpers read queued and responded responses", async (t) => {
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    void body;
    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      res.end(
        JSON.stringify({
          session_id: "session-5",
          name: "scenario-5",
          proxy_listen: "http://127.0.0.1:19100",
          mock_base_url: "http://127.0.0.1:19101/__phrond/session/session-5"
        })
      );
      return;
    }
    if (req.url === "/v1/listQueued?session_id=session-5") {
      res.end(
        JSON.stringify({
          session_id: "session-5",
          items: [{ id: "response-1", match: { method: "GET", path: "/alpha" }, response: { status: 200 } }]
        })
      );
      return;
    }
    if (req.url === "/v1/listResponded?session_id=session-5") {
      res.end(
        JSON.stringify({
          session_id: "session-5",
          items: [{ id: "response-2", match: { method: "GET", path: "/beta" }, response: { status: 200 } }]
        })
      );
      return;
    }
    if (req.url === "/v1/getResponse?session_id=session-5&id=response-2") {
      res.end(
        JSON.stringify({
          session_id: "session-5",
          id: "response-2",
          state: "responded",
          match: { method: "GET", path: "/beta" },
          response: { status: 200 }
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    sutBaseUrl: "http://sut.local:18090",
    proxy: true,
    mockServer: true
  });

  await runtime.setupTest({ name: "list-inspect" });
  const queued = await runtime.listQueued();
  const responded = await runtime.listResponded();
  const inspected = await runtime.getResponse("response-2");

  assert.equal(queued.items[0].id, "response-1");
  assert.equal(responded.items[0].id, "response-2");
  assert.equal(inspected.state, "responded");
  await runtime.killServer();
});

test("getResponse returns exchange details for assertions", async (t) => {
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    void body;
    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      res.end(
        JSON.stringify({
          session_id: "session-4",
          name: "scenario-4",
          proxy_listen: "http://127.0.0.1:19100",
          mock_base_url: "http://127.0.0.1:19101/__phrond/session/session-4"
        })
      );
      return;
    }
    if (req.url === "/v1/getResponse?session_id=session-4&id=account-acct-100") {
      res.end(
        JSON.stringify({
          session_id: "session-4",
          id: "account-acct-100",
          state: "responded",
          exchange: {
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
              Body: "{\"id\":\"acct-100\"}"
            },
            MatchedStubID: "account-acct-100",
            Matched: true,
            StartedAt: "2026-02-08T19:00:00Z",
            FinishedAt: "2026-02-08T19:00:00Z"
          }
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({
    baseUrl: server.url,
    schemas: INLINE_OPENAPI,
    sutBaseUrl: "http://sut.local:18090",
    proxy: true,
    mockServer: true
  });

  await runtime.setupTest({ name: "expectation-test" });
  const match = await runtime.getResponse("account-acct-100");
  assert.equal(match.state, "responded");
  assert.equal(match.exchange.Request.Method, "GET");
  assert.equal(match.exchange.Request.Path, "/accounts/acct-100");
  assert.equal(match.exchange.MatchedStubID, "account-acct-100");
  await runtime.killServer();
});

test("createNodeTestSuite owns only Phrond lifecycle and captures the node:test name", async (t) => {
  const requests = [];
  const server = await startHarnessServer(async (req, res) => {
    const body = await readBody(req);
    requests.push({ path: req.url, body });

    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          sut_url: "http://127.0.0.1:19100",
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      const payload = JSON.parse(body);
      res.end(
        JSON.stringify({
          session_id: "session-suite-1",
          name: payload.name,
          proxy_listen: "http://127.0.0.1:19100",
          sut_url: "http://127.0.0.1:19100",
          mock_base_url: "http://127.0.0.1:19101",
          dependency_urls: {
            greeter: "http://127.0.0.1:19101/__phrond/dep/greeter"
          }
        })
      );
      return;
    }
    if (req.url === "/v1/queue-response") {
      res.end(JSON.stringify({ status: "queued", id: "greeter-happy" }));
      return;
    }
    if (req.url === "/v1/teardownTest") {
      res.end(
        JSON.stringify({
          path: "artifacts/hello-world.yaml",
          schema_validation: { status: "pass", errors: null },
          artifact: {
            testcase: { name: "hello world happy flow", status: "pass" }
          }
        })
      );
      return;
    }
    if (req.url === "/v1/killServer") {
      res.end(JSON.stringify({ status: "stopped" }));
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const hooks = {};
  const registered = {};
  const testApi = {
    before(fn) {
      hooks.before = fn;
    },
    after(fn) {
      hooks.after = fn;
    },
    test(name, optionsOrFn, maybeFn) {
      registered.name = name;
      registered.run = typeof optionsOrFn === "function" ? optionsOrFn : maybeFn;
      return { name };
    }
  };

  const phrond = createNodeTestSuite({
    testApi,
    baseUrl: server.url,
    proxy: true,
    mockServer: true,
    sutBaseUrl: "http://externally-managed-sut:8080",
    dependencies: [{ assetId: "greeter" }]
  });

  phrond.test("hello world happy flow", async () => {
    await phrond.queueResponse({
      id: "greeter-happy",
      assetId: "greeter",
      match: { method: "POST", path: "/greet", json: { name: "Matt" } },
      response: { status: 200, json: { message: "Hello, Matt!" } }
    });
  });

  await hooks.before();
  assert.equal(phrond.sutUrl, "http://127.0.0.1:19100");
  assert.equal(
    phrond.dependencyUrl("greeter"),
    "http://127.0.0.1:19101/__phrond/dep/greeter"
  );
  await registered.run({});
  await hooks.after();

  assert.equal(registered.name, "hello world happy flow");
  const start = JSON.parse(requests.find((entry) => entry.path === "/v1/startServer").body);
  assert.equal(start.proxy.target_url, "http://externally-managed-sut:8080");

  const setup = JSON.parse(requests.find((entry) => entry.path === "/v1/setupTest").body);
  assert.equal(setup.name, "hello world happy flow");
  assert.equal(setup.sut_base_url, "http://externally-managed-sut:8080");

  const queued = JSON.parse(requests.find((entry) => entry.path === "/v1/queue-response").body);
  assert.equal(queued.match.body, "{\"name\":\"Matt\"}");
  assert.equal(queued.response.body, "{\"message\":\"Hello, Matt!\"}");
  assert.equal(queued.response.headers["Content-Type"], "application/json");
  assert.deepEqual(
    requests.map((entry) => entry.path),
    [
      "/v1/startServer",
      "/v1/setupTest",
      "/v1/queue-response",
      "/v1/teardownTest",
      "/v1/killServer"
    ]
  );
});

test("queueResponse rejects ambiguous JSON and literal body declarations", async (t) => {
  const server = await startHarnessServer(async (req, res) => {
    await readBody(req);
    res.setHeader("content-type", "application/json");
    if (req.url === "/v1/startServer") {
      res.end(
        JSON.stringify({
          proxy: { listen_addr: "127.0.0.1:19100" },
          mock: { listen_addr: "127.0.0.1:19101" }
        })
      );
      return;
    }
    if (req.url === "/v1/setupTest") {
      res.end(
        JSON.stringify({
          session_id: "session-json-1",
          name: "json",
          proxy_listen: "http://127.0.0.1:19100",
          mock_base_url: "http://127.0.0.1:19101"
        })
      );
      return;
    }
    res.end(JSON.stringify({ status: "ok" }));
  });
  t.after(() => server.close());

  const runtime = await startServer({ baseUrl: server.url });
  await runtime.setupTest({ name: "json" });
  await assert.rejects(
    () =>
      runtime.queueResponse({
        match: { method: "POST", path: "/", body: "{}", json: {} },
        response: { status: 200 }
      }),
    /match cannot set both body and json/
  );
  await assert.rejects(
    () =>
      runtime.queueResponse({
        match: { method: "GET", path: "/" },
        response: { status: 200, body: "{}", json: {} }
      }),
    /cannot set json together with body/
  );
  await runtime.killServer();
});
