const { test } = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");

const { startServer } = require("../src/index.js");

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

test("http helper sends traffic through proxy and tags session header", async (t) => {
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
          mock_base_url: "http://127.0.0.1:19101/__phrond/session/session-2"
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

  assert.equal(proxyRequests.length, 1);
  assert.equal(proxyRequests[0].path, "http://sut.local:18090/process?mode=test");
  assert.equal(proxyRequests[0].host, "sut.local:18090");
  assert.equal(proxyRequests[0].session, "session-2");
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
