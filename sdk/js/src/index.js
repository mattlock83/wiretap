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
  return value;
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

class Harness {
  constructor(config = {}) {
    this.baseUrl = config.baseUrl || DEFAULT_BASE_URL;
    this.timeoutMs = config.timeoutMs || 0;
  }

  async healthz() {
    return this._get("/healthz");
  }

  async startLifecycle() {
    return this._post("/v1/lifecycle/start", {});
  }

  async stopLifecycle() {
    return this._post("/v1/lifecycle/stop", {});
  }

  async setupSuite(options = {}) {
    if (typeof options !== "object" || Array.isArray(options)) {
      throw new Error("suite options must be an object");
    }

    await this.startLifecycle();
    try {
      const runtime = {};
      const proxyConfig = normalizeComponentConfig(options.proxy, "proxy");
      const mockServerConfig = normalizeComponentConfig(options.mockServer, "mockServer");
      const grpcProxyConfig = normalizeComponentConfig(options.grpcProxy, "grpcProxy");
      const grpcMockServerConfig = normalizeComponentConfig(options.grpcMockServer, "grpcMockServer");

      if (proxyConfig) {
        runtime.proxy = await this.startProxy(proxyConfig);
      }
      if (mockServerConfig) {
        runtime.mockServer = await this.startMockServer(mockServerConfig);
      }
      if (grpcProxyConfig) {
        runtime.grpcProxy = await this.startGrpcProxy(grpcProxyConfig);
      }
      if (grpcMockServerConfig) {
        runtime.grpcMockServer = await this.startGrpcMockServer(grpcMockServerConfig);
      }

      return new SuiteSession(this, runtime);
    } catch (err) {
      try {
        await this.stopLifecycle();
      } catch (_) {
        // ignore teardown failure and preserve original setup error
      }
      throw err;
    }
  }

  async startTestCase(name, meta, options = {}) {
    if (!options.schemas || options.schemas.length === 0) {
      throw new Error("schemas are required; pass options.schemas");
    }
    const payload = { name, meta };
    if (options.tags) {
      payload.tags = options.tags;
    }
    if (options.sutBaseUrl) {
      payload.sut_base_url = options.sutBaseUrl;
    }
    if (options.schemas) {
      payload.schemas = options.schemas;
    }
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

  async getArtifact() {
    return this._get("/v1/artifact");
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

  async waitForStatus(options = {}) {
    const intervalMs = options.intervalMs || 500;
    const timeoutMs = options.timeoutMs || 30_000;
    const start = Date.now();

    while (true) {
      const status = await this._get("/v1/testcases/status");
      if (!status.active) {
        return status;
      }
      if (Date.now() - start > timeoutMs) {
        const err = new Error("timed out waiting for test case to finish");
        err.status = status;
        throw err;
      }
      await new Promise((resolve) => setTimeout(resolve, intervalMs));
    }
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

  async expectCall(expectation) {
    return this._post("/v1/expectations", expectation);
  }

  async setSchema(schema) {
    return this._post("/v1/schemas", schema);
  }

  async addRedaction(rule) {
    return this._post("/v1/redactions", rule);
  }

  async setProxyMode(mode) {
    return this._post("/v1/proxy/mode", { mode });
  }

  async getCaCertificate() {
    return this._get("/v1/proxy/ca");
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

class SuiteSession {
  constructor(harness, runtime = {}) {
    this.harness = harness;
    this.runtime = runtime;
    this.proxyBaseUrl = resolveProxyBaseUrl(runtime.proxy);
  }

  async setupTest(config) {
    if (!config || typeof config !== "object" || Array.isArray(config)) {
      throw new Error("test config is required");
    }

    const { name, meta = {}, tags, sutBaseUrl, schemas } = config;
    if (!name) {
      throw new Error("name is required");
    }

    await this.harness.startTestCase(name, meta, { tags, sutBaseUrl, schemas });
    return new TestSession(this.harness, {
      proxyBaseUrl: this.proxyBaseUrl,
      sutBaseUrl
    });
  }

  async withTest(config, run, options = {}) {
    if (typeof run !== "function") {
      throw new Error("withTest requires a run callback");
    }

    const testSession = await this.setupTest(config);
    let runResult;
    let runError;
    try {
      runResult = await run(testSession);
    } catch (err) {
      runError = err;
    }

    const teardownOptions = { ...(options.teardown || {}) };
    if (!teardownOptions.status) {
      teardownOptions.status = runError ? "fail" : "pass";
    }

    let finish;
    let teardownError;
    try {
      finish = await testSession.teardown(teardownOptions);
    } catch (err) {
      teardownError = err;
    }

    if (runError) {
      if (teardownError) {
        runError.teardownError = teardownError;
      }
      throw runError;
    }
    if (teardownError) {
      throw teardownError;
    }

    return { result: runResult, finish };
  }

  async teardown() {
    return this.harness.stopLifecycle();
  }
}

class TestSession {
  constructor(harness, options = {}) {
    this.harness = harness;
    this.proxyBaseUrl = options.proxyBaseUrl || null;
    this.sutBaseUrl = options.sutBaseUrl || null;
  }

  async mockHttp(stub) {
    return this.harness.registerStub(stub);
  }

  async mockGrpc(stub) {
    return this.harness.registerGrpcStub(stub);
  }

  async clearMocks(options = {}) {
    const clearHttp = options.http !== false;
    const clearGrpc = options.grpc !== false;
    const result = {};

    if (clearHttp) {
      result.http = await this.harness.clearStubs();
    }
    if (clearGrpc) {
      result.grpc = await this.harness.clearGrpcStubs();
    }

    return result;
  }

  async expectCall(expectation) {
    return this.harness.expectCall(expectation);
  }

  async request(options = {}) {
    if (!options || typeof options !== "object" || Array.isArray(options)) {
      throw new Error("request options must be an object");
    }

    const method = (options.method || "GET").toUpperCase();
    const sutBaseUrl = options.sutBaseUrl || this.sutBaseUrl;
    if (!sutBaseUrl) {
      throw new Error("sutBaseUrl is required; pass it to suite.setupTest({ sutBaseUrl }) or test.request({ sutBaseUrl })");
    }

    const proxyBaseUrl = options.proxyBaseUrl || this.proxyBaseUrl;
    if (!proxyBaseUrl) {
      throw new Error("proxy is not configured; start suite with proxy: true to use test.request()");
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

  async teardown(options = {}) {
    const status = options.status || "pass";
    const resp = await this.harness.finishTestCase(status);

    if (options.downloadArtifactTo) {
      resp.downloadedArtifact = await this.harness.downloadArtifact(options.downloadArtifactTo);
    }

    return resp;
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

      if (this.harness.timeoutMs > 0) {
        req.setTimeout(this.harness.timeoutMs, () => {
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

function createHarness(config) {
  return new Harness(config);
}

async function setupSuite(config = {}) {
  if (typeof config !== "object" || Array.isArray(config)) {
    throw new Error("setupSuite config must be an object");
  }

  const {
    baseUrl,
    timeoutMs,
    proxy,
    mockServer,
    grpcProxy,
    grpcMockServer
  } = config;

  const harness = createHarness({ baseUrl, timeoutMs });
  return harness.setupSuite({ proxy, mockServer, grpcProxy, grpcMockServer });
}

module.exports = {
  Harness,
  createHarness,
  setupSuite
};
