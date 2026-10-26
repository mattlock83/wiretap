const assert = require("node:assert/strict");
const http = require("node:http");
const test = require("node:test");
const { startCapture } = require("../src");

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
    }
    assert.deepEqual(calls, []);

    const runtime = await startCapture({ baseUrl: server.baseUrl, schemaRequired: false });
    assert.throws(() => runtime.createTestAttempt("framework", { redactions: [] }), removed);
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
    const attempt = runtime.createTestAttempt("hello world happy flow");
    const session = await attempt.run(session => session);
    assert.equal(session.name, "hello world happy flow");
    assert.equal(session.bundlePath, "/bundle");
    assert.equal(session.runId, "run-1");
    await attempt.finish({ status: "pass" });
    assert.deepEqual(calls[0], {
      path: "/v1/startRun",
      body: { proxy: { target_url: "http://sut.test" } }
    });
    assert.match(calls[1].body.trace_context.traceparent, /^00-[0-9a-f]{32}-[0-9a-f]{16}-0[01]$/);
    const { trace_context, ...setupPayload } = calls[1].body;
    assert.deepEqual(setupPayload, {
      name: "hello world happy flow",
      sut_base_url: "http://sut.test",
      schemas: [{ id: "sut-api" }]
    });
    assert.equal(calls[2].body.status, "pass");
    assert.equal(JSON.stringify(calls).includes("mock"), false);
    assert.equal(Object.hasOwn(setupPayload, "dependencies"), false);
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
        inventory: { assetName: "Inventory API", upstreamUrl: "http://wiremock.test", schemas: [inventorySchema] }
      }
    });
    const attempt = runtime.createTestAttempt("orders call inventory", { explicitSession: true });
    const session = await attempt.run(session => session);
    assert.deepEqual(calls[1].body.dependencies, [{
      asset_id: "inventory",
      asset_name: "Inventory API",
      upstream_url: "http://wiremock.test",
      schemas: [inventorySchema]
    }]);
    assert.equal(session.dependencyUrls.inventory, "http://127.0.0.1:20010/__wiretap/session/session-1/dep/inventory");
    await attempt.finish();
    assert.equal(JSON.stringify(calls).includes('"mock"'), false);
  } finally {
    await server.close();
  }
});

test("capture configuration has one dependency and schema shape", async () => {
  for (const [options, message] of [
    [{ dependencies: [] }, /object keyed by dependency asset ID/],
    [{ dependencies: { "bad/name": {} } }, /valid asset ID/],
    [{ dependencies: { inventory: null } }, /must be an object/],
    [{ dependencies: { inventory: { assetId: "other" } } }, /asset ID is its object key/],
    [{ dependencies: { inventory: { schema: {} } } }, /uses schemas, an array/],
    [{ schemas: {} }, /schemas must be an array/],
    [{ schemas: [{ role: "dependency" }] }, /role is assigned/]
  ]) await assert.rejects(startCapture(options), message);
});

test("HTTP dependencies need no spec by default and preserve explicit schema requirements", async () => {
  const calls = [];
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startRun") return { sut_url: "http://127.0.0.1:20010" };
    if (path === "/v1/setupTest") return { session_id: `session-${calls.length}`, name: body.name };
    return {};
  });
  try {
    const dependencies = Object.fromEntries(["mock", "service", "unknown"].map(kind => [kind, {
      upstreamUrl: `http://${kind}.test`, upstreamKind: kind
    }]));
    const runtime = await startCapture({ baseUrl: server.baseUrl, schemas: [{ id: "sut-api" }], dependencies });
    await runtime.createTestAttempt("records consumed APIs").finish();
    const setup = calls.find(call => call.path === "/v1/setupTest").body;
    assert.deepEqual(setup.schemas, [{ id: "sut-api" }]);
    assert.deepEqual(setup.dependencies, ["mock", "service", "unknown"].map(kind => ({
      asset_id: kind, upstream_kind: kind, upstream_url: `http://${kind}.test`
    })));

    const schema = { id: "inventory", type: "openapi", source: { type: "file", path: "inventory.yaml" } };
    await runtime.createTestAttempt("opts in to dependency validation", { dependencies: {
      strict: { upstreamUrl: "http://strict.test", schemaRequired: true, schemas: [schema] },
      optional: { upstreamUrl: "http://optional.test", schemaRequired: false }
    } }).finish();
    const strictSetup = calls.filter(call => call.path === "/v1/setupTest").at(-1).body;
    assert.equal(strictSetup.dependencies[0].schema_required, true);
    assert.deepEqual(strictSetup.dependencies[0].schemas, [schema]);
    assert.equal(strictSetup.dependencies[1].schema_required, false);
    await assert.rejects(startCapture({ schemas: [{ id: "sut" }], dependencies: {
      grpc: { upstreamUrl: "http://grpc.test", protocol: "grpc", schemaRequired: false }
    } }), /protocol was removed; Wiretap supports HTTP APIs/);
  } finally { await server.close(); }
});

test("schema defaults are resolved by the runtime", async () => {
  const calls = [];
  const server = await controlServer((path, body) => { calls.push({ path, body }); return {}; });
  try {
    const runtime = await startCapture({ baseUrl: server.baseUrl });
    await runtime.createTestAttempt("uses project schemas").finish();
    const setup = calls.find(call => call.path === "/v1/setupTest").body;
    assert.equal(Object.hasOwn(setup, "schemas"), false);
    assert.equal(Object.hasOwn(setup, "schema_required"), false);
  } finally { await server.close(); }
});

test("explicit schema omission permits skipped validation", async () => {
  const calls = [];
  const server = await controlServer((path, body) => {
    calls.push({ path, body });
    if (path === "/v1/startRun") return { sut_url: "http://127.0.0.1:20010", proxy: { listen_addr: "127.0.0.1:20010" } };
    if (path === "/v1/setupTest") return { session_id: "session-1", name: body.name, proxy_listen: "http://127.0.0.1:20010" };
    if (path === "/v1/teardownTest") return { capture: { schema_validation: { status: "skipped" } } };
    return {};
  });
  try {
    const runtime = await startCapture({
      baseUrl: server.baseUrl,
      sutBaseUrl: "http://sut.test",
      schemaRequired: false
    });
    const result = await runtime.createTestAttempt("creates an order").finish();

    const setup = calls.find((call) => call.path === "/v1/setupTest");
    assert.equal(setup.body.schema_required, false);
    assert.equal(Object.hasOwn(setup.body, "schemas"), false);
    const paths = calls.map((call) => call.path);
    assert.deepEqual(paths, ["/v1/startRun", "/v1/setupTest", "/v1/teardownTest"]);
    assert.equal(result.capture.schema_validation.status, "skipped");

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
    const result = await runtime.createTestAttempt("invalid response").finish();
    assert.equal(result.capture.schema_validation.status, "fail");
    assert.equal(result.capture.schema_validation.errors[0].message, "response body is invalid");
  } finally {
    await server.close();
  }
});

test("project defaults stay in Go and shared suites leave finalization to their owner", async () => {
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
    const runtime = await startCapture();
    await runtime.createTestAttempt("configured test").finish();
    const setup = calls.find(call => call.path === "/v1/setupTest").body;
    assert.deepEqual(Object.keys(setup).sort(), ["name", "trace_context"]);
    assert.equal(Object.hasOwn(calls[0].body, "application"), false);
    await runtime.createTestAttempt("clears inherited configuration", {
      dependencies: {}, schemas: [], schemaRequired: false, explicitSession: false, testSource: {}
    }).finish();
    const override = calls.filter(call => call.path === "/v1/setupTest")[1].body;
    assert.deepEqual(override.dependencies, []);
    assert.deepEqual(override.schemas, []);
    assert.equal(override.schema_required, false);
    assert.equal(override.explicit_session, false);
    assert.deepEqual(override.test_source, {});
    await runtime.createTestAttempt("inherits again").finish();
    assert.deepEqual(Object.keys(calls.filter(call => call.path === "/v1/setupTest")[2].body).sort(), ["name", "trace_context"]);
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
    await runtime.createTestAttempt("runtime application").finish();
    await runtime.createTestAttempt("test application", { application: testApplication }).finish();
    await runtime.createTestAttempt("runtime application again").finish();
    assert.deepEqual(calls[0].body.application, application);
    const setups = calls.filter(call => call.path === "/v1/setupTest").map(call => call.body.application);
    assert.deepEqual(setups, [application, testApplication, application]);
  } finally {
    await server.close();
  }
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
      await runtime.createTestAttempt("display label", { identity: { id: "orders/create", variant: "invalid", attemptOrdinal: ordinal } }).finish();
    }
    assert.deepEqual(setups.map(call => call.test_identity), [
      { id: "orders/create", variant: "invalid", attempt_ordinal: 0 },
      { id: "orders/create", variant: "invalid", attempt_ordinal: 1 }
    ]);
    assert.notEqual(setups[0].trace_context.traceparent, setups[1].trace_context.traceparent);
  } finally { await server.close(); }
});
