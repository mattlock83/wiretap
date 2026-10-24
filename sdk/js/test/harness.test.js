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

test("removed redaction options fail clearly before creating capture state", async () => {
  const calls = [];
  const server = await controlServer((path) => {
    calls.push(path);
    return {};
  });
  const removed = /redactions option was removed.*retains test traffic unchanged/;
  try {
    for (const redactions of [[], undefined, null, [{ field: "request.headers.authorization", rule: "mask" }]]) {
      const config = { baseUrl: server.baseUrl, schemaRequired: false, redactions };
      await assert.rejects(startCapture(config), removed);
      await assert.rejects(startCapture({ baseUrl: server.baseUrl, schemaRequired: false, testDefaults: { redactions } }), removed);
      assert.throws(() => createNodeTestSuite(config), removed);
      assert.throws(() => createNodeTestSuite({ schemaRequired: false, testDefaults: { redactions } }), removed);
      assert.throws(() => bindTest({}, () => {}, { redactions }), removed);
    }
    assert.deepEqual(calls, []);

    const runtime = await startCapture({ baseUrl: server.baseUrl, schemaRequired: false });
    await assert.rejects(runtime.setupTest({ name: "manual", redactions: [] }), removed);
    assert.throws(() => runtime.createTestAttempt("framework", { redactions: [] }), removed);
    await assert.rejects(runtime.runTest("callback", () => assert.fail("must not run"), { redactions: [] }), removed);
    const wrapped = bindTest(runtime, () => assert.fail("must not register"));
    assert.throws(() => wrapped("runner options", { redactions: [] }, () => {}), removed);
    assert.deepEqual(calls, ["/v1/startRun"]);
  } finally {
    await server.close();
  }
});

test("startCapture and named test session send capture-only payloads", async () => {
  const calls = [];
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startRun") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") return { session_id: "session-1", name: body.name, proxy_listen: "http://127.0.0.1:20010", bundle_path: "/bundle", run_id: "run-1" };
    if (path === "/v1/teardownTest") return { capture: { schema_validation: { status: "pass" } } };
    return { status: "ok" };
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl, sutBaseUrl: "http://sut.test", schemas: [{ id: "sut-api" }] });
    const session = await runtime.setupTest({ name: "hello world happy flow" });
    assert.equal(session.name, "hello world happy flow");
    assert.equal(session.bundlePath, "/bundle");
    assert.equal(session.runId, "run-1");
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
    const runtime = await startCapture({ baseUrl: server.baseUrl, schemaRequired: false });
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
        dependencies: { inventory: "http://127.0.0.1:20010/__wiretap/session/session-1/dep/inventory" }
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
    assert.equal(runtime.dependencyUrl("inventory"), "http://127.0.0.1:20010/__wiretap/dep/inventory");
    const session = await runtime.setupTest({ name: "orders call inventory", explicitSession: true });
    assert.deepEqual(calls[1].body.dependencies, [{
      asset_id: "inventory",
      asset_name: "Inventory API",
      upstream_kind: "unknown",
      upstream_url: "http://wiremock.test",
      schemas: [inventorySchema]
    }]);
    assert.equal(session.dependencyUrls.inventory, "http://127.0.0.1:20010/__wiretap/session/session-1/dep/inventory");
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
      schemaRequired: false,
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
	assert.equal(typeof suite.dependencyUrl, "function");
  assert.equal(typeof hooks.before, "function");
  assert.equal(typeof hooks.after, "function");
});

test("schemas are required unless explicitly omitted", async () => {
  const server = await controlServer(() => ({}));
  try {
    await assert.rejects(() => startCapture({ baseUrl: server.baseUrl }), /OpenAPI schemas are required/);
    const runtime = await startCapture({ baseUrl: server.baseUrl, schemas: [{ id: "sut-api" }], schemaRequired: false });
    assert.deepEqual(runtime.defaultSchemas, [{ id: "sut-api" }]);
  } finally { await server.close(); }
});

test("explicit schema omission permits skipped validation and finalizes the run", async () => {
  const calls = [];
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startRun") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") return { session_id: "session-1", name: body.name, proxy_listen: "http://127.0.0.1:20010" };
    if (path === "/v1/teardownTest") return { capture: { schema_validation: { status: "skipped" } } };
    if (path === "/v1/finishRun") return { status: "complete" };
    return {};
  });
  try {
    const runtime = await startCapture({
      baseUrl: server.baseUrl,
      sutBaseUrl: "http://sut.test",
      schemaRequired: false
    });
    await runtime.setupTest({ name: "creates an order" });
    await runtime.teardownTest({ status: "pass" });
    const result = await runtime.finishRun();

    const setup = calls.find((call) => call.path === "/v1/setupTest");
    assert.equal(setup.body.schema_required, false);
    assert.deepEqual(setup.body.schemas, []);
    const paths = calls.map((call) => call.path);
    assert.deepEqual(paths, ["/v1/startRun", "/v1/setupTest", "/v1/teardownTest", "/v1/finishRun"]);
    assert.deepEqual(result, { status: "complete" });

  } finally {
    await server.close();
  }
});

test("schema violations remain evidence without failing teardown", async () => {
  const server = await controlServer((path, body) => {
    if (path === "/v1/startRun") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") return { session_id: "session-1", name: body.name, proxy_listen: "http://127.0.0.1:20010" };
    if (path === "/v1/teardownTest") return { capture: { schema_validation: { status: "fail", errors: [{ message: "response body is invalid" }] } } };
    return {};
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl, schemas: [{ id: "sut-api" }] });
    await runtime.setupTest({ name: "invalid response" });
    const result = await runtime.teardownTest({ status: "pass" });
    assert.equal(result.capture.schema_validation.status, "fail");
    assert.equal(result.capture.schema_validation.errors[0].message, "response body is invalid");
  } finally {
    await server.close();
  }
});

test("project defaults are inherited and shared suites leave finalization to their owner", async () => {
  const calls = [];
  const application = { name: "Order service", id: "orders", description: "Accepts customer orders" };
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startRun") return {
      sut_url: "http://127.0.0.1:20010",
      defaults: { application, sut_base_url: "http://sut.test", api_id: "orders", schema_required: false,
        dependencies: [{ asset_id: "inventory", upstream_url: "http://mock.test", schema_required: false, upstream_kind: "mock" }] }
    };
    if (path === "/v1/setupTest") return { session_id: "configured", sut_url: "http://127.0.0.1:20010" };
    if (path === "/v1/teardownTest") return { capture: { schema_validation: { status: "partial", skipped: ["inventory"] } } };
    return {};
  });
  const previousBase = process.env.WIRETAP_BASE_URL;
  process.env.WIRETAP_BASE_URL = server.baseUrl;
  try {
    const runtime = await startCapture({ manageRuntime: false });
    await runtime.runTest("configured test", () => {});
    await runtime.finishRun();
    const setup = calls.find(call => call.path === "/v1/setupTest").body;
    assert.deepEqual(setup.application, application);
    assert.equal(Object.hasOwn(calls[0].body, "application"), false);
    assert.equal(setup.schema_required, false);
    assert.equal(setup.sut_base_url, "http://sut.test");
    assert.equal(setup.dependencies[0].schema_required, false);
    assert.equal(setup.dependencies[0].upstream_kind, "mock");
    await runtime.runTest("clears dependencies", () => {}, { dependencies: [] });
    assert.deepEqual(calls.filter(call => call.path === "/v1/setupTest")[1].body.dependencies, []);
    assert.equal(calls.some(call => call.path === "/v1/finishRun"), false);
  } finally {
    if (previousBase === undefined) delete process.env.WIRETAP_BASE_URL; else process.env.WIRETAP_BASE_URL = previousBase;
    await server.close();
  }
});

test("application metadata is sent at run start and resolved for each test", async () => {
  const calls = [];
  const projectApplication = { name: "Project service", id: "project" };
  const application = { name: "Order service", id: "orders", description: "Accepts customer orders" };
  const testApplication = { name: "Other service", id: "other" };
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startRun") return { defaults: { application: projectApplication, schema_required: false } };
    if (path === "/v1/setupTest") return { session_id: body.name };
    return {};
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl, application });
    await runtime.runTest("runtime application", () => {});
    await runtime.runTest("test application", () => {}, { application: testApplication });
    await runtime.runTest("runtime application again", () => {});
    await runtime.finishRun();
    assert.deepEqual(calls[0].body.application, application);
    const setups = calls.filter(call => call.path === "/v1/setupTest").map(call => call.body.application);
    assert.deepEqual(setups, [application, testApplication, application]);
  } finally {
    await server.close();
  }
});

test("gRPC project defaults preserve protocol and direct dependency targets", async () => {
  const calls = [];
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startRun") return {
      sut_url: "http://127.0.0.1:20010",
      dependencies: { inventory: "http://127.0.0.1:20011" },
      defaults: { sut_base_url: "http://sut.test", sut_protocol: "grpc", api_id: "orders",
        dependencies: [{ asset_id: "inventory", protocol: "grpc", upstream_url: "http://mock.test", upstream_kind: "mock" }] }
    };
    if (path === "/v1/setupTest") return { session_id: "grpc", sut_url: "http://127.0.0.1:20010", dependencies: { inventory: "http://127.0.0.1:20011" } };
    if (path === "/v1/teardownTest") return { capture: { schema_validation: { status: "pass" } } };
    return {};
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl });
    assert.equal(runtime.dependencyUrl("inventory"), "http://127.0.0.1:20011");
    await runtime.runTest("grpc provider and mock", () => {});
    const setup = calls.find(call => call.path === "/v1/setupTest").body;
    assert.equal(setup.sut_protocol, "grpc");
    assert.equal(setup.dependencies[0].protocol, "grpc");
    assert.deepEqual(setup.schemas, []);
    await runtime.finishRun();
  } finally {
    await server.close();
  }
});

test("suite owner selects outputs and receives exact generated paths", async () => {
  const outputs = { coverage: ["json", "yaml"], postman: true };
  const expected = { bundle_path: "/capture/run-1", coverage: { json: "/capture/run-1/exports/coverage.json" }, postman: ["/capture/run-1/exports/postman/sut.json"], errors: [] };
  let payload;
  const server = await controlServer((path, body) => {
    if (path === "/v1/finishRun") { payload = body; return { status: "complete", outputs: expected }; }
    return {};
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl, schemaRequired: false, outputs });
    const result = await runtime.finishRun();
    assert.deepEqual(payload, { outputs });
    assert.deepEqual(result.outputs, expected);
  } finally { await server.close(); }
});

test("logical identity is separate from retries and execution traces", async () => {
  const setups = [];
  const server = await controlServer((path, body) => {
    if (path === "/v1/setupTest") { setups.push(body); return { session_id: `s-${setups.length}`, name: body.name }; }
    return {};
  });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl, schemaRequired: false });
    for (let ordinal = 0; ordinal < 2; ordinal++) {
      await runtime.runTest("display label", () => {}, { identity: { id: "orders/create", variant: "invalid", attemptOrdinal: ordinal } });
    }
    assert.deepEqual(setups.map(call => call.test_identity), [
      { id: "orders/create", variant: "invalid", attempt_ordinal: 0 },
      { id: "orders/create", variant: "invalid", attempt_ordinal: 1 }
    ]);
    assert.notEqual(setups[0].trace_context.traceparent, setups[1].trace_context.traceparent);
  } finally { await server.close(); }
});
