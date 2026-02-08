const DEFAULT_BASE_URL = "http://127.0.0.1:18000";

function normalizeComponentConfig(value, name) {
  if (value === undefined || value === null || value === false) {
    return null;
  }
  if (value === true) {
    return {};
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
  return payload;
}

function resolveProxyBaseUrl(proxyInfo) {
  if (!proxyInfo || typeof proxyInfo !== "object") {
    return null;
  }
  const listenAddr = proxyInfo.listen_addr || proxyInfo.ListenAddr || proxyInfo.listenAddr;
  if (!listenAddr || typeof listenAddr !== "string") {
    return null;
  }
  if (listenAddr.startsWith("http://") || listenAddr.startsWith("https://")) {
    return listenAddr;
  }
  return `http://${listenAddr}`;
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

class CoreClient {
  constructor(config = {}) {
    this.baseUrl = config.baseUrl || DEFAULT_BASE_URL;
    this.timeoutMs = config.timeoutMs || 0;
  }

  async startLifecycle() {
    return this._post("/v1/lifecycle/start", {});
  }

  async stopLifecycle() {
    return this._post("/v1/lifecycle/stop", {});
  }

  async startProxy(config) {
    return this._post("/v1/proxy/start", config || {});
  }

  async startMockServer(config) {
    return this._post("/v1/mock/start", config || {});
  }

  async startGrpcProxy(config) {
    return this._post("/v1/grpc/proxy/start", config || {});
  }

  async startGrpcMockServer(config) {
    return this._post("/v1/grpc/mock/start", config || {});
  }

  async startTestCase(name, options = {}) {
    const payload = {
      name,
      meta: options.meta || {},
      tags: options.tags,
      sut_base_url: options.sutBaseUrl,
      schemas: options.schemas
    };
    return this._post("/v1/testcases/start", payload);
  }

  async finishTestCase(status) {
    const resp = await this._post("/v1/testcases/finish", { status });
    if (resp && resp.schema_validation && !resp.schemaValidation) {
      resp.schemaValidation = resp.schema_validation;
    }
    if (resp && resp.artifact && resp.artifact.meta && resp.artifact.meta.schema_validation && !resp.schemaValidation) {
      resp.schemaValidation = resp.artifact.meta.schema_validation;
    }
    return resp;
  }

  async registerStub(stub) {
    return this._post("/v1/stubs", stub);
  }

  async clearStubs() {
    return this._post("/v1/stubs/clear", {});
  }

  async registerGrpcStub(stub) {
    return this._post("/v1/grpc/stubs", stub);
  }

  async clearGrpcStubs() {
    return this._post("/v1/grpc/stubs/clear", {});
  }

  async getMockExchanges() {
    return this._get("/v1/mock/exchanges");
  }

  async downloadArtifact(filePath) {
    if (!filePath) {
      throw new Error("filePath is required");
    }
    if (typeof fetch !== "function") {
      throw new Error("global fetch is not available; require Node.js >= 18");
    }

    const url = new URL("/v1/artifact", this.baseUrl);
    const res = await fetch(url);
    if (!res.ok) {
      const text = await res.text();
      const err = new Error(`artifact download failed: ${res.status}`);
      err.status = res.status;
      err.body = text;
      throw err;
    }

    const { writeFile } = require("node:fs/promises");
    const buffer = Buffer.from(await res.arrayBuffer());
    await writeFile(filePath, buffer);
    return { path: filePath };
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

    let controller;
    if (this.timeoutMs > 0) {
      controller = new AbortController();
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

class PhrondSession {
  constructor(client, options = {}, runtime = {}) {
    this.client = client;
    this.runtime = runtime;

    this.schemas = Array.isArray(options.schemas) ? [...options.schemas] : [];
    this.sutBaseUrl = options.sutBaseUrl || null;
    this.tags = Array.isArray(options.tags) ? [...options.tags] : undefined;
    this.meta = options.meta && typeof options.meta === "object" && !Array.isArray(options.meta) ? { ...options.meta } : {};
    this.strictSchema = options.strictSchema !== false;
    this.scenarioPrefix = options.scenarioPrefix || "scenario";

    this.scenarioCounter = 0;
    this.pendingScenarioName = null;
    this.caseActive = false;
    this.currentScenarioName = "";
    this.lastFinish = null;
    this.caseHistory = [];
    this.stopped = false;

    this.mock = {
      http: (stub) => this.mockHttp(stub),
      grpc: (stub) => this.mockGrpc(stub),
      clear: (options) => this.clearMocks(options)
    };

    this.http = {
      request: (options) => this.request(options),
      get: (path, options) => this.get(path, options),
      post: (path, body, options) => this.post(path, body, options),
      postJson: (path, payload, options) => this.postJson(path, payload, options)
    };
  }

  async scenarioName(name, run) {
    this._assertNotStopped();
    if (typeof name !== "string" || name.trim() === "") {
      throw new Error("scenarioName must be a non-empty string");
    }
    if (this.caseActive) {
      throw new Error("scenarioName must be set before the first mock or request in a scenario");
    }
    this.pendingScenarioName = name.trim();

    if (typeof run === "function") {
      return run(this);
    }
    return this;
  }

  async reset(options = {}) {
    this._assertNotStopped();
    let finish = null;

    if (this.caseActive) {
      finish = await this._finishActiveCase({
        status: options.status || "pass",
        strictSchema: options.strictSchema
      });
    }

    if (options.clearMocks !== false) {
      await this.clearMocks();
    }

    if (typeof options.nextScenarioName === "string" && options.nextScenarioName.trim() !== "") {
      this.pendingScenarioName = options.nextScenarioName.trim();
    } else {
      this.pendingScenarioName = null;
    }

    return finish;
  }

  async stop(options = {}) {
    if (this.stopped) {
      return { finish: this.lastFinish, runtime: this.runtime, finishedCases: [...this.caseHistory] };
    }

    let finish = null;
    let finishErr = null;

    const writeArtifact = options.writeArtifact !== false;
    if (writeArtifact) {
      try {
        if (!this.caseActive) {
          await this._ensureCase();
        }
        finish = await this._finishActiveCase({
          status: options.status || "pass",
          strictSchema: options.strictSchema
        });
      } catch (err) {
        finishErr = err;
      }
    }

    let stopErr = null;
    try {
      await this.client.stopLifecycle();
    } catch (err) {
      stopErr = err;
    }

    this.stopped = true;

    if (finishErr) {
      if (stopErr) {
        finishErr.stopError = stopErr;
      }
      throw finishErr;
    }
    if (stopErr) {
      throw stopErr;
    }

    return { finish, runtime: this.runtime, finishedCases: [...this.caseHistory] };
  }

  async mockHttp(stub) {
    this._assertNotStopped();
    await this._ensureCase();
    return this.client.registerStub(stub);
  }

  async mockGrpc(stub) {
    this._assertNotStopped();
    await this._ensureCase();
    return this.client.registerGrpcStub(stub);
  }

  async clearMocks(options = {}) {
    this._assertNotStopped();
    const clearHttp = options.http !== false;
    const clearGrpc = options.grpc !== false;
    const result = {};

    if (clearHttp && this.runtime.mockServer) {
      result.http = await this.client.clearStubs();
    }
    if (clearGrpc && this.runtime.grpcMockServer) {
      result.grpc = await this.client.clearGrpcStubs();
    }

    return result;
  }

  async expectCall(expectation) {
    this._assertNotStopped();
    await this._ensureCase();

    const query = normalizeExpectation(expectation);
    const exchanges = await this.client.getMockExchanges();
    const matches = Array.isArray(exchanges) ? exchanges.filter((exchange) => matchExchange(exchange, query)) : [];

    if (matches.length < query.times) {
      throw new Error(`expected ${query.times} call(s) matching ${query.method || "*"} ${query.path}, found ${matches.length}`);
    }

    const latest = matches[matches.length - 1];
    return normalizeOutboundRequest(latest);
  }

  async request(options = {}) {
    this._assertNotStopped();
    if (!options || typeof options !== "object" || Array.isArray(options)) {
      throw new Error("request options must be an object");
    }

    await this._ensureCase();

    const method = (options.method || "GET").toUpperCase();
    const sutBaseUrl = options.sutBaseUrl || this.sutBaseUrl;
    if (!sutBaseUrl) {
      throw new Error("sutBaseUrl is required; pass it to start({ sutBaseUrl }) or runtime.http.request({ sutBaseUrl })");
    }

    const proxyBaseUrl = options.proxyBaseUrl || resolveProxyBaseUrl(this.runtime.proxy);
    if (!proxyBaseUrl) {
      throw new Error("proxy is not configured; start with proxy: true to use runtime.http helpers");
    }

    const targetUrl = toURL(options.path, sutBaseUrl);
    const proxyUrl = toURL(proxyBaseUrl);

    return this._requestViaProxy(proxyUrl, targetUrl, {
      method,
      headers: options.headers,
      body: options.body
    });
  }

  async get(path, options = {}) {
    return this.request({ ...options, method: "GET", path });
  }

  async post(path, body, options = {}) {
    return this.request({ ...options, method: "POST", path, body });
  }

  async postJson(path, payload, options = {}) {
    const headers = { ...(options.headers || {}) };
    if (!headers["Content-Type"] && !headers["content-type"]) {
      headers["Content-Type"] = "application/json";
    }
    return this.post(path, JSON.stringify(payload), {
      ...options,
      headers
    });
  }

  async _ensureCase() {
    if (this.caseActive) {
      return;
    }

    const name = this.pendingScenarioName || this._nextScenarioName();
    this.pendingScenarioName = null;

    await this.client.startTestCase(name, {
      meta: this.meta,
      tags: this.tags,
      sutBaseUrl: this.sutBaseUrl,
      schemas: this.schemas
    });

    this.caseActive = true;
    this.currentScenarioName = name;
  }

  _nextScenarioName() {
    this.scenarioCounter += 1;
    return `${this.scenarioPrefix}-${Date.now()}-${this.scenarioCounter}`;
  }

  async _finishActiveCase(options = {}) {
    if (!this.caseActive) {
      return null;
    }

    const status = options.status || "pass";
    const finish = await this.client.finishTestCase(status);

    this.caseActive = false;
    this.currentScenarioName = "";
    this.lastFinish = finish;
    this.caseHistory.push(finish);

    const strictSchema = options.strictSchema !== undefined ? options.strictSchema : this.strictSchema;
    this._throwOnSchemaFailure(finish, strictSchema, status);

    return finish;
  }

  _throwOnSchemaFailure(finish, strictSchema, requestedStatus) {
    if (!strictSchema || !finish) {
      return;
    }

    const schema = finish.schemaValidation || finish.schema_validation || finish.artifact?.schema_validation || finish.artifact?.meta?.schema_validation;
    const schemaStatus = schema?.status;

    if (schemaStatus && schemaStatus !== "pass") {
      const errors = Array.isArray(schema.errors) ? schema.errors : [];
      const summary = errors.length > 0 ? errors.map((entry) => entry.message || "schema violation").join("; ") : "schema validation failed";
      const err = new Error(summary);
      err.schemaValidation = schema;
      err.finish = finish;
      throw err;
    }

    const testcaseStatus = finish.artifact?.testcase?.status;
    if (requestedStatus === "pass" && testcaseStatus && testcaseStatus !== "pass") {
      const err = new Error(`testcase finished with status ${testcaseStatus}`);
      err.finish = finish;
      throw err;
    }
  }

  _assertNotStopped() {
    if (this.stopped) {
      throw new Error("runtime session already stopped");
    }
  }

  async _requestViaProxy(proxyUrl, targetUrl, options = {}) {
    const method = options.method || "GET";
    const headers = { ...(options.headers || {}) };
    headers.Host = targetUrl.host;

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
          path: targetUrl.href,
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

function normalizeExpectation(expectation) {
  if (typeof expectation === "string") {
    return {
      path: expectation,
      method: "",
      times: 1
    };
  }

  if (!expectation || typeof expectation !== "object" || Array.isArray(expectation)) {
    throw new Error("expectCall expects a path string or an expectation object");
  }

  const path = expectation.path || expectation.url || "";
  if (!path || typeof path !== "string") {
    throw new Error("expectCall requires expectation.path");
  }

  return {
    path,
    method: expectation.method ? String(expectation.method).toUpperCase() : "",
    times: Number.isInteger(expectation.times) && expectation.times > 0 ? expectation.times : 1,
    bodyContains: typeof expectation.bodyContains === "string" ? expectation.bodyContains : "",
    stubId: expectation.stubId || expectation.stub_id || ""
  };
}

function matchExchange(exchange, expectation) {
  if (!exchange || typeof exchange !== "object") {
    return false;
  }

  const req = exchange.Request || {};

  if (expectation.method && String(req.Method || "").toUpperCase() !== expectation.method) {
    return false;
  }

  const reqPath = String(req.Path || "");
  const reqURL = String(req.URL || "");
  if (reqPath !== expectation.path && reqURL !== expectation.path) {
    return false;
  }

  if (expectation.bodyContains && !String(req.Body || "").includes(expectation.bodyContains)) {
    return false;
  }

  if (expectation.stubId && String(exchange.MatchedStubID || "") !== expectation.stubId) {
    return false;
  }

  return true;
}

function normalizeOutboundRequest(exchange) {
  if (!exchange || typeof exchange !== "object") {
    return null;
  }
  return {
    method: exchange.Request?.Method || "",
    path: exchange.Request?.Path || "",
    url: exchange.Request?.URL || "",
    headers: exchange.Request?.Headers || {},
    query: exchange.Request?.Query || {},
    body: exchange.Request?.Body || "",
    matchedStubId: exchange.MatchedStubID || "",
    startedAt: exchange.StartedAt,
    finishedAt: exchange.FinishedAt
  };
}

async function start(config = {}) {
  if (!config || typeof config !== "object" || Array.isArray(config)) {
    throw new Error("start config must be an object");
  }

  if (!Array.isArray(config.schemas) || config.schemas.length === 0) {
    throw new Error("schemas are required; pass start({ schemas: [...] })");
  }

  const client = new CoreClient({
    baseUrl: config.baseUrl,
    timeoutMs: config.timeoutMs
  });

  const proxyConfig = normalizeComponentConfig(config.proxy, "proxy");
  const mockServerConfig = normalizeComponentConfig(config.mockServer, "mockServer");
  const grpcProxyConfig = normalizeComponentConfig(config.grpcProxy, "grpcProxy");
  const grpcMockServerConfig = normalizeComponentConfig(config.grpcMockServer, "grpcMockServer");

  await client.startLifecycle();

  const runtime = {};
  try {
    if (proxyConfig) {
      runtime.proxy = await client.startProxy(normalizeStartPayload(proxyConfig));
    }
    if (mockServerConfig) {
      runtime.mockServer = await client.startMockServer(normalizeStartPayload(mockServerConfig));
    }
    if (grpcProxyConfig) {
      runtime.grpcProxy = await client.startGrpcProxy(normalizeStartPayload(grpcProxyConfig));
    }
    if (grpcMockServerConfig) {
      runtime.grpcMockServer = await client.startGrpcMockServer(normalizeStartPayload(grpcMockServerConfig));
    }

    return new PhrondSession(client, config, runtime);
  } catch (err) {
    try {
      await client.stopLifecycle();
    } catch (_) {
      // keep original startup error
    }
    throw err;
  }
}

module.exports = {
  start
};
