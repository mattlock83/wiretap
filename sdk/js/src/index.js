const DEFAULT_BASE_URL = "http://127.0.0.1:18000";

function normalizeComponentConfig(value, name) {
  if (value === undefined || value === null) {
    return {};
  }
  if (value === true) {
    return {};
  }
  if (value === false) {
    return null;
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${name} config must be an object, true, or false`);
  }
  return { ...value };
}

function normalizeStartPayload(config) {
  if (!config) {
    return {};
  }
  const payload = { ...config };
  if (payload.listenAddr && !payload.listen_addr) {
    payload.listen_addr = payload.listenAddr;
  }
  delete payload.listenAddr;
  if (payload.targetUrl && !payload.target_url) {
    payload.target_url = payload.targetUrl;
  }
  delete payload.targetUrl;
  return payload;
}

function toURL(value, baseUrl) {
  if (!value || typeof value !== "string") {
    throw new Error("request path is required");
  }

  try {
    return new URL(value);
  } catch (_) {
    // continue with base URL resolution
  }

  if (!baseUrl) {
    throw new Error("base URL is required to resolve request path");
  }
  return new URL(value, baseUrl);
}

function pickFirstString(...values) {
  for (const value of values) {
    if (typeof value === "string" && value.trim() !== "") {
      return value;
    }
  }
  return "";
}

function toSnakeCaseQueuePayload(payload, sessionId) {
  const match = payload?.match || {};
  const response = payload?.response || {};
  const isDefault = payload?.default ?? payload?.isDefault;

  return {
    session_id: payload?.sessionId || payload?.session_id || sessionId,
    id: payload?.id,
    asset_id: payload?.assetId || payload?.asset_id,
    default: Boolean(isDefault),
    match: {
      method: match.method,
      path: match.path,
      headers: match.headers,
      query: match.query,
      body: match.body,
      body_contains: match.bodyContains || match.body_contains
    },
    response: {
      status: response.status,
      delay_ms: response.delayMs ?? response.delay_ms,
      headers: response.headers,
      body: response.body,
      body_file: response.bodyFile || response.body_file,
      connection_error: Boolean(response.connectionError || response.connection_error),
      ssl_error: Boolean(response.sslError || response.ssl_error)
    }
  };
}

function normalizeDependencies(dependencies) {
  if (!Array.isArray(dependencies)) {
    return undefined;
  }
  return dependencies.map((dep) => {
    if (!dep || typeof dep !== "object") {
      throw new Error("each dependency must be an object with an assetId");
    }
    const assetId = dep.assetId || dep.asset_id;
    if (!assetId || typeof assetId !== "string") {
      throw new Error("dependency assetId is required");
    }
    return {
      asset_id: assetId,
      asset_name: dep.assetName || dep.asset_name,
      schema: dep.schema
    };
  });
}

function splitSchemasByRole(schemas = []) {
  const sutSchemas = [];
  const fakeSchemas = [];
  for (const schema of schemas) {
    const role = String(schema?.role || "sut").toLowerCase();
    if (role === "fake") {
      fakeSchemas.push(schema);
    } else {
      sutSchemas.push(schema);
    }
  }
  return { sutSchemas, fakeSchemas };
}

class CoreClient {
  constructor(config = {}) {
    this.baseUrl = config.baseUrl || DEFAULT_BASE_URL;
    this.timeoutMs = config.timeoutMs || 0;
  }

  async startServer(payload) {
    return this._post("/v1/startServer", payload || {});
  }

  async setupTest(payload) {
    return this._post("/v1/setupTest", payload || {});
  }

  async queueResponse(payload) {
    return this._post("/v1/queue-response", payload || {});
  }

  async verify(payload) {
    return this._post("/v1/verify", payload || {});
  }

  async teardownTest(payload) {
    return this._post("/v1/teardownTest", payload || {});
  }

  async report(payload) {
    return this._post("/v1/report", payload || {});
  }

  async killServer() {
    return this._post("/v1/killServer", {});
  }

  async listQueued(sessionId) {
    return this._get(`/v1/listQueued?session_id=${encodeURIComponent(sessionId)}`);
  }

  async listResponded(sessionId) {
    return this._get(`/v1/listResponded?session_id=${encodeURIComponent(sessionId)}`);
  }

  async getResponse(sessionId, id) {
    return this._get(`/v1/getResponse?session_id=${encodeURIComponent(sessionId)}&id=${encodeURIComponent(id)}`);
  }

  async _get(path) {
    return this._request("GET", path);
  }

  async _post(path, payload) {
    return this._request("POST", path, payload);
  }

  async _request(method, path, payload) {
    if (typeof fetch !== "function") {
      throw new Error("global fetch is not available; require Node.js >= 18");
    }

    const url = new URL(path, this.baseUrl);
    const options = {
      method,
      headers: {
        "content-type": "application/json"
      }
    };

    if (payload && method !== "GET") {
      options.body = JSON.stringify(payload);
    }

    if (this.timeoutMs > 0) {
      const controller = new AbortController();
      options.signal = controller.signal;
      setTimeout(() => controller.abort(), this.timeoutMs).unref?.();
    }

    const res = await fetch(url, options);
    const text = await res.text();
    const contentType = res.headers.get("content-type") || "";
    const isJSON = contentType.includes("application/json");
    const body = text ? (isJSON ? JSON.parse(text) : text) : null;

    if (!res.ok) {
      const err = new Error(`request failed: ${res.status}`);
      err.status = res.status;
      err.body = body;
      throw err;
    }

    return body;
  }
}

class Runtime {
  constructor(client, config = {}, runtimeInfo = {}) {
    this.client = client;
    this.runtimeInfo = runtimeInfo;

    this.defaultSutBaseUrl = config.sutBaseUrl || null;
    this.defaultSchemas = Array.isArray(config.schemas) ? [...config.schemas] : [];
    this.defaultDependencies = Array.isArray(config.dependencies) ? [...config.dependencies] : [];
    this.strictSchema = config.strictSchema !== false;
    this.pendingScenarioName = "";

    // sutUrl is the reverse-capture address: tests call it with any HTTP
    // client as if it were the SUT itself.
    this.sutUrl = runtimeInfo?.sut_url || null;

    this.activeSession = null;

    this.http = {
      request: (options) => this._httpRequest(options),
      get: (path, options) => this._httpGet(path, options),
      post: (path, body, options) => this._httpPost(path, body, options),
      postJson: (path, payload, options) => this._httpPostJson(path, payload, options)
    };
  }

  _requireSession(sessionId) {
    const id = sessionId || this.activeSession?.sessionId;
    if (!id) {
      throw new Error("no active session; call setupTest() first");
    }
    return id;
  }

  async _ensureSession(options = {}) {
    const explicitSessionID = options.sessionId || options.session_id;
    if (explicitSessionID) {
      return { sessionId: explicitSessionID };
    }
    if (this.activeSession?.sessionId) {
      return this.activeSession;
    }
    return this.setupTest(options);
  }

  scenarioName(name) {
    if (typeof name !== "string" || name.trim() === "") {
      throw new Error("scenario name is required");
    }
    this.pendingScenarioName = name.trim();
    return this.pendingScenarioName;
  }

  async setupTest(options = {}) {
    const schemas = Array.isArray(options.schemas) ? options.schemas : this.defaultSchemas;
    const split = splitSchemasByRole(schemas);
    const sutSchemas = Array.isArray(options.sutSchemas) ? options.sutSchemas : split.sutSchemas;
    const fakeSchemas = Array.isArray(options.fakeSchemas) ? options.fakeSchemas : split.fakeSchemas;
    const dependencies = normalizeDependencies(
      Array.isArray(options.dependencies) ? options.dependencies : this.defaultDependencies
    );
    const scenarioName = pickFirstString(options.name, options.scenarioName, this.pendingScenarioName);

    const payload = {
      name: scenarioName || undefined,
      tags: options.tags,
      meta: options.meta,
      sut_base_url: options.sutBaseUrl || options.sut_base_url || this.defaultSutBaseUrl,
      record_traffic: options.recordTraffic ?? options.record_traffic,
      explicit_session: Boolean(options.explicitSession ?? options.explicit_session),
      dependencies,
      sut_schemas: sutSchemas,
      fake_schemas: fakeSchemas
    };

    const session = await this.client.setupTest(payload);
    this.activeSession = {
      sessionId: session.session_id,
      name: session.name,
      explicit: Boolean(session.explicit),
      sutBaseUrl: payload.sut_base_url,
      recordTraffic: session.record_traffic,
      proxyListen: session.proxy_listen || this.runtimeInfo?.proxy?.listen_addr,
      sutUrl: session.sut_url || this.sutUrl,
      mockBaseUrl: session.mock_base_url,
      dependencyUrls: session.dependency_urls || {}
    };
    this.pendingScenarioName = "";
    return this.activeSession;
  }

  // dependencyUrl returns the mock base URL to inject into the SUT for one
  // declared dependency. Stable across zero-config sessions, so it can be
  // configured once at SUT startup.
  dependencyUrl(assetId) {
    if (!assetId || typeof assetId !== "string") {
      throw new Error("assetId is required");
    }
    const fromSession = this.activeSession?.dependencyUrls?.[assetId];
    if (fromSession) {
      return fromSession;
    }
    const mockListen = this.runtimeInfo?.mock?.listen_addr;
    if (!mockListen) {
      throw new Error("mock listen address is not available; call startServer() first");
    }
    const base = mockListen.startsWith("http") ? mockListen : `http://${mockListen}`;
    return `${base}/__phrond/dep/${assetId}`;
  }

  // verify records which fields of a mocked response the test depends on.
  async verify(id, fields, options = {}) {
    if (!id || typeof id !== "string") {
      throw new Error("response id is required");
    }
    const list = Array.isArray(fields) ? fields : [fields];
    const sessionId = this._requireSession(options.sessionId || options.session_id);
    return this.client.verify({ session_id: sessionId, id, fields: list });
  }

  async queueResponse(payload, options = {}) {
    await this._ensureSession(options);
    const sessionId = this._requireSession(options.sessionId || options.session_id || payload?.sessionId || payload?.session_id);
    const request = toSnakeCaseQueuePayload(payload || {}, sessionId);
    return this.client.queueResponse(request);
  }

  async teardownTest(options = {}) {
    const sessionId = options.sessionId || options.session_id || this.activeSession?.sessionId;
    if (!sessionId) {
      return { status: "idle" };
    }

    const response = await this.client.teardownTest({
      session_id: sessionId,
      status: options.status || "pass"
    });

    if (this.activeSession?.sessionId === sessionId) {
      this.activeSession = null;
    }

    if (this.strictSchema) {
      const schemaValidation = response?.schema_validation;
      if (schemaValidation?.status && schemaValidation.status !== "pass") {
        const errors = Array.isArray(schemaValidation.errors) ? schemaValidation.errors : [];
        const summary = errors.length > 0 ? errors.map((entry) => entry.message || "schema violation").join("; ") : "schema validation failed";
        const err = new Error(summary);
        err.schemaValidation = schemaValidation;
        err.response = response;
        throw err;
      }
    }

    return response;
  }

  async report(options = {}) {
    return this.client.report({
      artifacts_dir: options.artifactsDir || options.artifacts_dir,
      sut_name: options.sutName || options.sut_name,
      include_artifacts: options.includeArtifacts || options.include_artifacts || false,
      collection_path:
        options.collectionPath ||
        options.collection_path ||
        options.yamlPath ||
        options.yaml_path,
      workflow_path: options.workflowPath || options.workflow_path,
      html_path: options.htmlPath || options.html_path
    });
  }

  async killServer() {
    this.activeSession = null;
    return this.client.killServer();
  }

  async listQueued(options = {}) {
    const sessionId = this._requireSession(options.sessionId || options.session_id);
    return this.client.listQueued(sessionId);
  }

  async listResponded(options = {}) {
    const sessionId = this._requireSession(options.sessionId || options.session_id);
    return this.client.listResponded(sessionId);
  }

  async getResponse(id, options = {}) {
    if (!id || typeof id !== "string") {
      throw new Error("response id is required");
    }
    const sessionId = this._requireSession(options.sessionId || options.session_id);
    return this.client.getResponse(sessionId, id);
  }

  async _httpRequest(options = {}) {
    if (!options || typeof options !== "object" || Array.isArray(options)) {
      throw new Error("request options must be an object");
    }
    const session = await this._ensureSession(options);

    const method = (options.method || "GET").toUpperCase();
    const defaultTarget = session.sutBaseUrl || this.defaultSutBaseUrl;
    const overrideTarget = options.sutBaseUrl || options.sut_base_url;

    const proxyBaseUrl = options.proxyBaseUrl || options.proxy_base_url || session.proxyListen;
    if (!proxyBaseUrl) {
      throw new Error("proxy listen address is not available; check startServer() response");
    }
    const proxyUrl = toURL(proxyBaseUrl.startsWith("http") ? proxyBaseUrl : `http://${proxyBaseUrl}`);

    const headers = { ...(options.headers || {}) };
    headers["X-Phrond-Session-Id"] = session.sessionId;

    if (overrideTarget && overrideTarget !== defaultTarget) {
      // Explicit forward-proxy request for a target other than the configured
      // reverse target (e.g. a second provided API in the same suite).
      const targetUrl = toURL(options.path, overrideTarget);
      return this._requestViaProxy(proxyUrl, targetUrl, {
        method,
        headers,
        body: options.body,
        forward: true
      });
    }

    // Reverse mode: call the proxy address directly with a relative path; the
    // proxy forwards to the SUT target configured at startServer().
    const targetUrl = toURL(options.path, proxyUrl.href);
    return this._requestViaProxy(proxyUrl, targetUrl, {
      method,
      headers,
      body: options.body,
      forward: false
    });
  }

  async _httpGet(path, options = {}) {
    return this._httpRequest({ ...options, method: "GET", path });
  }

  async _httpPost(path, body, options = {}) {
    return this._httpRequest({ ...options, method: "POST", path, body });
  }

  async _httpPostJson(path, payload, options = {}) {
    const headers = { ...(options.headers || {}) };
    if (!headers["Content-Type"] && !headers["content-type"]) {
      headers["Content-Type"] = "application/json";
    }
    return this._httpPost(path, JSON.stringify(payload), {
      ...options,
      headers
    });
  }

  async _requestViaProxy(proxyUrl, targetUrl, options = {}) {
    const method = options.method || "GET";
    const forward = options.forward !== false;
    const headers = { ...(options.headers || {}) };
    if (forward) {
      headers.Host = targetUrl.host;
    }
    // Forward-proxy requests carry the absolute URI; reverse requests carry
    // just the path (the proxy resolves the target itself).
    const requestPath = forward ? targetUrl.href : targetUrl.pathname + targetUrl.search;

    let body = options.body;
    if (body !== undefined && body !== null) {
      if (!Buffer.isBuffer(body) && typeof body !== "string" && !(body instanceof Uint8Array)) {
        throw new Error("request body must be a string, Buffer, or Uint8Array; use postJson for objects");
      }
      if (!headers["Content-Length"] && !headers["content-length"]) {
        headers["Content-Length"] = Buffer.byteLength(body);
      }
    }

    const client = proxyUrl.protocol === "https:" ? require("node:https") : require("node:http");

    return new Promise((resolve, reject) => {
      const req = client.request(
        {
          host: proxyUrl.hostname,
          port: proxyUrl.port || (proxyUrl.protocol === "https:" ? 443 : 80),
          method,
          path: requestPath,
          headers
        },
        (res) => {
          const chunks = [];
          res.on("data", (chunk) => chunks.push(chunk));
          res.on("end", () => {
            const raw = Buffer.concat(chunks).toString("utf8");
            const contentTypeHeader = res.headers["content-type"];
            const contentType = Array.isArray(contentTypeHeader) ? (contentTypeHeader[0] || "") : (contentTypeHeader || "");
            const isJSON = contentType.includes("application/json");

            let parsedBody = raw;
            if (raw && isJSON) {
              try {
                parsedBody = JSON.parse(raw);
              } catch (err) {
                reject(new Error(`proxy response JSON parse failed: ${err.message}`));
                return;
              }
            }
            if (!raw) {
              parsedBody = null;
            }

            const response = {
              status: res.statusCode || 0,
              headers: res.headers,
              body: parsedBody
            };

            if ((res.statusCode || 0) >= 400) {
              const err = new Error(`proxy request failed: ${res.statusCode}`);
              err.status = res.statusCode;
              err.body = parsedBody;
              err.headers = res.headers;
              reject(err);
              return;
            }

            resolve(response);
          });
        }
      );

      if (this.client.timeoutMs > 0) {
        req.setTimeout(this.client.timeoutMs, () => {
          req.destroy(new Error("proxy request timed out"));
        });
      }

      req.on("error", reject);
      if (body !== undefined && body !== null) {
        req.write(body);
      }
      req.end();
    });
  }

}

async function startServer(config = {}) {
  if (!config || typeof config !== "object" || Array.isArray(config)) {
    throw new Error("startServer config must be an object");
  }

  const client = new CoreClient({
    baseUrl: config.baseUrl,
    timeoutMs: config.timeoutMs
  });

  const proxyConfig = normalizeComponentConfig(config.proxy, "proxy");
  const mockServerConfig = normalizeComponentConfig(config.mockServer, "mockServer");

  if (proxyConfig !== null && config.sutBaseUrl && !proxyConfig.targetUrl && !proxyConfig.target_url) {
    // Reverse capture: tests call the proxy address as if it were the SUT.
    proxyConfig.target_url = config.sutBaseUrl;
  }

  const payload = {
    proxy: proxyConfig === null ? undefined : normalizeStartPayload(proxyConfig),
    mock: mockServerConfig === null ? undefined : normalizeStartPayload(mockServerConfig),
    stack_size: config.stackSize || config.stack_size,
    record_responses: config.recordResponses
      ? {
          dependency_url: config.recordResponses.dependencyUrl || config.recordResponses.dependency_url || ""
        }
      : undefined
  };

  const runtimeInfo = await client.startServer(payload);
  return new Runtime(client, config, runtimeInfo);
}

// bindTest wraps a test function (e.g. node:test's `test`) so every test gets
// an automatic phrond session named after it, and — crucially — the artifact
// status reflects the real test outcome (pass/fail) instead of a hardcoded
// value in an afterEach hook.
//
// The first argument is the runtime, or a function returning it (use a getter
// when the runtime is created in a before() hook after tests are registered):
//
//   const { test } = require("node:test");
//   const phrondTest = bindTest(() => runtime, test);
//   phrondTest("submits payment", async (t, session) => { ... });
function bindTest(runtimeOrGetter, testFn, defaults = {}) {
  const resolveRuntime =
    typeof runtimeOrGetter === "function" ? runtimeOrGetter : () => runtimeOrGetter;
  if (typeof testFn !== "function") {
    throw new Error("bindTest requires a test function (e.g. node:test's test)");
  }
  return function phrondTest(name, optionsOrFn, maybeFn) {
    const fn = typeof optionsOrFn === "function" ? optionsOrFn : maybeFn;
    const testOptions = typeof optionsOrFn === "function" ? undefined : optionsOrFn;
    const run = async (t) => {
      const runtime = resolveRuntime();
      if (!runtime || typeof runtime.setupTest !== "function") {
        throw new Error("bindTest could not resolve a phrond runtime");
      }
      const session = await runtime.setupTest({ ...defaults, name });
      if (typeof defaults.onSession === "function") {
        await defaults.onSession(session);
      }
      try {
        await fn(t, session);
      } catch (err) {
        try {
          await runtime.teardownTest({ status: "fail" });
        } catch (_) {
          // preserve the original test failure
        }
        throw err;
      }
      // teardownTest throws on schema validation failure (strictSchema),
      // which correctly fails the test.
      await runtime.teardownTest({ status: "pass" });
    };
    return testOptions === undefined ? testFn(name, run) : testFn(name, testOptions, run);
  };
}

module.exports = {
  startServer,
  bindTest
};
