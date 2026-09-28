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
    if (path === "/v1/startRun") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") return { session_id: "session-1", name: body.name, proxy_listen: "http://127.0.0.1:20010", viewer_path: "/bundle/viewer/index.html" };
    if (path === "/v1/teardownTest") return { capture: { schema_validation: { status: "pass" } } };
    return { status: "ok" };
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl, sutBaseUrl: "http://sut.test", schemas: [{ id: "sut-api" }] });
    const session = await runtime.setupTest({ name: "hello world happy flow" });
    assert.equal(session.name, "hello world happy flow");
    assert.equal(session.viewerPath, "/bundle/viewer/index.html");
    await runtime.teardownTest({ status: "pass" });
    assert.deepEqual(calls[0], {
      path: "/v1/startRun",
      body: { proxy: { target_url: "http://sut.test" } }
    });
    assert.match(calls[1].body.trace_context.traceparent, /^00-[0-9a-f]{32}-[0-9a-f]{16}-0[01]$/);
    const { trace_context, ...setupPayload } = calls[1].body;
    assert.deepEqual(setupPayload, {
      name: "hello world happy flow",
      sut_base_url: "http://sut.test",
      explicit_session: false,
      schemas: [{ id: "sut-api" }],
      dependencies: []
    });
    assert.equal(calls[2].body.status, "pass");
    assert.equal(JSON.stringify(calls).includes("mock"), false);
    assert.deepEqual(setupPayload.dependencies, []);
  } finally {
    await server.close();
  }
});

test("report selects a bundle", async () => {
  let reportPayload;
  const server = await controlServer((path, body) => {
    if (path === "/v1/startRun") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/report") { reportPayload = body; return { status: "ok" }; }
    return {};
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl, generateSchema: true });
    await runtime.report({
      bundlePath: "capture-bundle"
    });
    assert.deepEqual(reportPayload, { bundle_path: "capture-bundle" });
  } finally {
    await server.close();
  }
});

test("declared external dependencies retain strict schemas and session-scoped capture URLs", async () => {
  const calls = [];
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startRun") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") {
      return {
        session_id: "session-1",
        name: body.name,
        explicit: true,
        proxy_listen: "http://127.0.0.1:20010",
        dependencies: { inventory: "http://127.0.0.1:20010/__phrond/session/session-1/dep/inventory" }
      };
    }
    return { capture: { schema_validation: { status: "pass" } } };
  });
  try {
    const inventorySchema = { id: "inventory-api", type: "openapi", source: { type: "file", path: "inventory.yaml" } };
    const runtime = await startCapture({
      baseUrl: server.baseUrl,
      sutBaseUrl: "http://sut.test",
      schemas: [{ id: "sut-api" }],
      dependencies: {
        inventory: { assetName: "Inventory API", upstreamUrl: "http://wiremock.test", schema: inventorySchema }
      }
    });
    assert.equal(runtime.dependencyUrl("inventory"), "http://127.0.0.1:20010/__phrond/dep/inventory");
    const session = await runtime.setupTest({ name: "orders call inventory", explicitSession: true });
    assert.deepEqual(calls[1].body.dependencies, [{
      asset_id: "inventory",
      asset_name: "Inventory API",
      upstream_kind: "unknown",
      upstream_url: "http://wiremock.test",
      schemas: [inventorySchema]
    }]);
    assert.equal(session.dependencyUrls.inventory, "http://127.0.0.1:20010/__phrond/session/session-1/dep/inventory");
    assert.equal(runtime.dependencyUrl("inventory"), session.dependencyUrls.inventory);
    assert.throws(() => runtime.dependencyUrl("payments"), /not configured/);
    assert.equal(JSON.stringify(calls).includes('"mock"'), false);
  } finally {
    await server.close();
  }
});

test("external dependencies require their own schema by default", async () => {
  await assert.rejects(
    () => startCapture({
      baseUrl: "http://127.0.0.1:1",
      schemas: [{ id: "sut-api" }],
      dependencies: [{ assetId: "inventory", upstreamUrl: "http://wiremock.test" }]
    }),
    /dependency "inventory" requires an OpenAPI schema/
  );
  assert.throws(
    () => createNodeTestSuite({
      generateSchema: true,
      dependencies: [{ assetId: "inventory", upstreamUrl: "http://wiremock.test" }]
    }),
    /dependency "inventory" requires an OpenAPI schema/
  );
  assert.throws(
    () => createNodeTestSuite({
      schemas: [{ id: "sut-api" }],
      dependencies: [{ assetId: "bad/name", upstreamUrl: "http://wiremock.test", schema: { id: "inventory" } }]
    }),
    /valid assetId/
  );
});

test("createNodeTestSuite exposes capture, dependency forwarding, and reporting", () => {
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
	assert.equal(typeof suite.dependencyUrl, "function");
  assert.equal(typeof hooks.before, "function");
  assert.equal(typeof hooks.after, "function");
});

test("schemas are required unless generation is explicitly enabled", async () => {
  const server = await controlServer(() => ({}));
  try {
    await assert.rejects(() => startCapture({ baseUrl: server.baseUrl }), /OpenAPI schemas are required/);
  } finally { await server.close(); }
  assert.throws(
    () => createNodeTestSuite({ schemas: [{ id: "sut-api" }], generateSchema: true }),
    /cannot be used together/
  );
});

test("explicit generation permits skipped validation and writes schema before shutdown", async () => {
  const calls = [];
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startRun") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") return { session_id: "session-1", name: body.name, proxy_listen: "http://127.0.0.1:20010" };
    if (path === "/v1/teardownTest") return { capture: { schema_validation: { status: "skipped" } } };
    if (path === "/v1/generateSchema") return { schema_path: body.output_path, artifact_count: 1 };
    if (path === "/v1/finishRun") return { status: "complete" };
    return {};
  });
  try {
    const runtime = await startCapture({
      baseUrl: server.baseUrl,
      sutBaseUrl: "http://sut.test",
      generateSchema: {
        outputPath: "artifacts/orders.generated.openapi.yaml",
        title: "Orders Service",
        version: "1.0.0",
        force: true
      }
    });
    await runtime.setupTest({ name: "creates an order" });
    await runtime.teardownTest({ status: "pass" });
    const result = await runtime.finishRun();

    const setup = calls.find((call) => call.path === "/v1/setupTest");
    assert.equal(setup.body.generate_schema, true);
    assert.deepEqual(setup.body.schemas, []);
    const paths = calls.map((call) => call.path);
    assert.ok(paths.indexOf("/v1/generateSchema") < paths.indexOf("/v1/finishRun"));
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
    if (path === "/v1/startRun") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") return { session_id: "session-1", name: body.name, proxy_listen: "http://127.0.0.1:20010" };
    if (path === "/v1/teardownTest") return { capture: { schema_validation: { status: "fail", errors: [{ message: "response body is invalid" }] } } };
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

test("project defaults are inherited and shared suites leave finalization to their owner", async () => {
  const calls = [];
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startRun") return {
      sut_url: "http://127.0.0.1:20010",
      defaults: { sut_base_url: "http://sut.test", api_id: "orders", schema_required: false,
        dependencies: [{ asset_id: "inventory", upstream_url: "http://mock.test", schema_required: false, upstream_kind: "mock" }] }
    };
    if (path === "/v1/setupTest") return { session_id: "configured", sut_url: "http://127.0.0.1:20010" };
    if (path === "/v1/teardownTest") return { capture: { schema_validation: { status: "partial", skipped: ["inventory"] } } };
    return {};
  });
  const previousBase = process.env.PHROND_BASE_URL;
  process.env.PHROND_BASE_URL = server.baseUrl;
  try {
    const runtime = await startCapture({ manageRuntime: false });
    await runtime.runTest("configured test", () => {});
    await runtime.finishRun();
    const setup = calls.find(call => call.path === "/v1/setupTest").body;
    assert.equal(setup.schema_required, false);
    assert.equal(setup.sut_base_url, "http://sut.test");
    assert.equal(setup.dependencies[0].schema_required, false);
    assert.equal(setup.dependencies[0].upstream_kind, "mock");
    await runtime.runTest("clears dependencies", () => {}, { dependencies: [] });
    assert.deepEqual(calls.filter(call => call.path === "/v1/setupTest")[1].body.dependencies, []);
    assert.equal(calls.some(call => call.path === "/v1/finishRun"), false);
  } finally {
    if (previousBase === undefined) delete process.env.PHROND_BASE_URL; else process.env.PHROND_BASE_URL = previousBase;
    await server.close();
  }
});
