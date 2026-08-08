const assert = require("node:assert/strict");
const http = require("node:http");
const test = require("node:test");
const { startCapture, bindTest, createNodeTestSuite } = require("../src");

async function controlServer(handler) {
  const server = http.createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
    const response = await handler(req.url, body);
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(response || {}));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  return {
    baseUrl: `http://127.0.0.1:${server.address().port}`,
    close: () => new Promise((resolve) => server.close(resolve))
  };
}

test("startCapture and named test session send capture-only payloads", async () => {
  const calls = [];
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startServer") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") return { session_id: "session-1", name: body.name, proxy_listen: "http://127.0.0.1:20010" };
    if (path === "/v1/teardownTest") return { schema_validation: { status: "pass" } };
    return { status: "ok" };
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl, sutBaseUrl: "http://sut.test", schemas: [{ id: "sut-api" }] });
    runtime.scenarioName("hello world happy flow");
    const session = await runtime.setupTest();
    assert.equal(session.name, "hello world happy flow");
    await runtime.teardownTest({ status: "pass" });
    assert.deepEqual(calls[0], {
      path: "/v1/startServer",
      body: { proxy: { target_url: "http://sut.test" } }
    });
    assert.deepEqual(calls[1].body, {
      name: "hello world happy flow",
      sut_base_url: "http://sut.test",
      explicit_session: false,
      schemas: [{ id: "sut-api" }]
    });
    assert.equal(calls[2].body.status, "pass");
    assert.equal(JSON.stringify(calls).includes("mock"), false);
    assert.equal(JSON.stringify(calls).includes("dependencies"), false);
  } finally {
    await server.close();
  }
});

test("report forwards all supported output paths", async () => {
  let reportPayload;
  const server = await controlServer((path, body) => {
    if (path === "/v1/startServer") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/report") { reportPayload = body; return { status: "ok" }; }
    return {};
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl, generateSchema: true });
    await runtime.report({ collectionPath: "capture.postman.json", workflowPath: "capture.workflow.yaml", htmlPath: "capture.html" });
    assert.equal(reportPayload.collection_path, "capture.postman.json");
    assert.equal(reportPayload.workflow_path, "capture.workflow.yaml");
    assert.equal(reportPayload.html_path, "capture.html");
  } finally {
    await server.close();
  }
});

test("bindTest records the test name and preserves failures", async () => {
  const calls = [];
  const runtime = {
    setupTest: async (payload) => { calls.push(["setup", payload]); return { sessionId: "session-1" }; },
    teardownTest: async (payload) => { calls.push(["teardown", payload]); return {}; }
  };
  let wrapped;
  const fakeTest = (name, fn) => { wrapped = fn; return name; };
  bindTest(runtime, fakeTest)("hello world happy flow", async () => { throw new Error("boom"); });
  await assert.rejects(() => wrapped({}), /boom/);
  assert.equal(calls[0][1].name, "hello world happy flow");
  assert.equal(calls[1][1].status, "fail");
});

test("createNodeTestSuite exposes only capture, HTTP, and reporting", () => {
  const hooks = {};
  const suite = createNodeTestSuite({
    autoHooks: true,
    schemas: [{ id: "sut-api" }],
    testApi: {
      test: () => {},
      before: (fn) => { hooks.before = fn; },
      after: (fn) => { hooks.after = fn; }
    }
  });
  assert.equal(typeof suite.test, "function");
  assert.equal(typeof suite.report, "function");
  assert.equal(typeof suite.generateSchema, "function");
  assert.equal(suite.queueResponse, undefined);
  assert.equal(suite.verify, undefined);
  assert.equal(suite.dependencyUrl, undefined);
  assert.equal(typeof hooks.before, "function");
  assert.equal(typeof hooks.after, "function");
});

test("schemas are required unless generation is explicitly enabled", async () => {
  await assert.rejects(
    () => startCapture({ baseUrl: "http://127.0.0.1:1" }),
    /OpenAPI schemas are required/
  );
  assert.throws(
    () => createNodeTestSuite({ schemas: [{ id: "sut-api" }], generateSchema: true }),
    /cannot be used together/
  );
  assert.throws(
    () => createNodeTestSuite({ strictSchema: false }),
    /no longer supported/
  );
});

test("explicit generation permits skipped validation and writes schema before shutdown", async () => {
  const calls = [];
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startServer") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") return { session_id: "session-1", name: body.name, proxy_listen: "http://127.0.0.1:20010" };
    if (path === "/v1/teardownTest") return { schema_validation: { status: "skipped" } };
    if (path === "/v1/generateSchema") return { schema_path: body.output_path, artifact_count: 1 };
    if (path === "/v1/killServer") return { status: "killed" };
    return {};
  });
  try {
    const runtime = await startCapture({
      baseUrl: server.baseUrl,
      sutBaseUrl: "http://sut.test",
      generateSchema: {
        output: "artifacts/orders.generated.openapi.yaml",
        title: "Orders Service",
        version: "1.0.0",
        force: true
      }
    });
    await runtime.setupTest({ name: "creates an order" });
    await runtime.teardownTest({ status: "pass" });
    const result = await runtime.killServer();

    const setup = calls.find((call) => call.path === "/v1/setupTest");
    assert.equal(setup.body.generate_schema, true);
    assert.deepEqual(setup.body.schemas, []);
    const paths = calls.map((call) => call.path);
    assert.ok(paths.indexOf("/v1/generateSchema") < paths.indexOf("/v1/killServer"));
    const generation = calls.find((call) => call.path === "/v1/generateSchema");
    assert.deepEqual(generation.body, {
      output_path: "artifacts/orders.generated.openapi.yaml",
      title: "Orders Service",
      version: "1.0.0",
      force: true
    });
    assert.equal(result.generatedSchema.schema_path, "artifacts/orders.generated.openapi.yaml");
  } finally {
    await server.close();
  }
});

test("strict schema violations fail teardown", async () => {
  const server = await controlServer((path, body) => {
    if (path === "/v1/startServer") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") return { session_id: "session-1", name: body.name, proxy_listen: "http://127.0.0.1:20010" };
    if (path === "/v1/teardownTest") return { schema_validation: { status: "fail", errors: [{ message: "response body is invalid" }] } };
    return {};
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl, schemas: [{ id: "sut-api" }] });
    await runtime.setupTest({ name: "invalid response" });
    await assert.rejects(() => runtime.teardownTest({ status: "pass" }), /response body is invalid/);
  } finally {
    await server.close();
  }
});
