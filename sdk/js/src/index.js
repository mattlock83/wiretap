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

  return {
    session_id: payload?.sessionId || payload?.session_id || sessionId,
    id: payload?.id,
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

  async startRuntime(payload) {
    return this._post("/v1/start", payload || {});
  }

  async setup(payload) {
    return this._post("/v1/setup", payload || {});
  }

  async queueResponse(payload) {
    return this._post("/v1/queue-response", payload || {});
  }

  async teardown(payload) {
    return this._post("/v1/teardown", payload || {});
  }

  async report(payload) {
    return this._post("/v1/report", payload || {});
  }

  async kill() {
    return this._post("/v1/kill", {});
  }

  async listQueued(sessionId) {
    return this._get(`/v1/listQueued?session_id=${encodeURIComponent(sessionId)}`);
  }

  async listResponded(sessionId) {
    return this._get(`/v1/listResponded?session_id=${encodeURIComponent(sessionId)}`);
  }

  async inspectResponse(sessionId, id) {
    return this._get(`/v1/inspectResponse?session_id=${encodeURIComponent(sessionId)}&id=${encodeURIComponent(id)}`);
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

class Runtime {
  constructor(client, config = {}, runtimeInfo = {}) {
    this.client = client;
    this.config = config;
    this.runtimeInfo = runtimeInfo;

    this.defaultSutBaseUrl = config.sutBaseUrl || null;
    this.defaultSchemas = Array.isArray(config.schemas) ? [...config.schemas] : [];
    this.strictSchema = config.strictSchema !== false;
    this.pendingScenarioName = "";

    this.activeSession = null;

    this.http = {
      request: (options) => this.request(options),
      get: (path, options) => this.get(path, options),
      post: (path, body, options) => this.post(path, body, options),
      postJson: (path, payload, options) => this.postJson(path, payload, options)
    };
  }

  _requireSession(sessionId) {
    const id = sessionId || this.activeSession?.sessionId;
    if (!id) {
      throw new Error("no active session; call setup() first");
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
    return this.setup(options);
  }

  scenarioName(name) {
    if (typeof name !== "string" || name.trim() === "") {
      throw new Error("scenario name is required");
    }
    this.pendingScenarioName = name.trim();
    return this.pendingScenarioName;
  }

  async setup(options = {}) {
    const schemas = Array.isArray(options.schemas) ? options.schemas : this.defaultSchemas;
    const split = splitSchemasByRole(schemas);
    const sutSchemas = Array.isArray(options.sutSchemas) ? options.sutSchemas : split.sutSchemas;
    const fakeSchemas = Array.isArray(options.fakeSchemas) ? options.fakeSchemas : split.fakeSchemas;
    const scenarioName = pickFirstString(options.name, options.scenarioName, this.pendingScenarioName);

    const payload = {
      name: scenarioName || undefined,
      tags: options.tags,
      meta: options.meta,
      sut_base_url: options.sutBaseUrl || options.sut_base_url || this.defaultSutBaseUrl,
      sut_schemas: sutSchemas,
      fake_schemas: fakeSchemas
    };

    const session = await this.client.setup(payload);
    this.activeSession = {
      sessionId: session.session_id,
      name: session.name,
      sutBaseUrl: payload.sut_base_url,
      proxyListen: session.proxy_listen || this.runtimeInfo?.proxy?.listen_addr,
      mockBaseUrl: session.mock_base_url
    };
    this.pendingScenarioName = "";
    return this.activeSession;
  }

  async queueResponse(payload, options = {}) {
    await this._ensureSession(options);
    const sessionId = this._requireSession(options.sessionId || options.session_id || payload?.sessionId || payload?.session_id);
    const request = toSnakeCaseQueuePayload(payload || {}, sessionId);
    return this.client.queueResponse(request);
  }

  async reset(options = {}) {
    const sessionId = options.sessionId || options.session_id || this.activeSession?.sessionId;
    if (!sessionId) {
      return { status: "idle" };
    }

    const response = await this.client.teardown({
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
      yaml_path: options.yamlPath || options.yaml_path,
      html_path: options.htmlPath || options.html_path
    });
  }

  async kill() {
    this.activeSession = null;
    return this.client.kill();
  }

  async listQueued(options = {}) {
    const sessionId = this._requireSession(options.sessionId || options.session_id);
    return this.client.listQueued(sessionId);
  }

  async listResponded(options = {}) {
    const sessionId = this._requireSession(options.sessionId || options.session_id);
    return this.client.listResponded(sessionId);
  }

  async inspectResponse(id, options = {}) {
    if (!id || typeof id !== "string") {
      throw new Error("response id is required");
    }
    const sessionId = this._requireSession(options.sessionId || options.session_id);
    return this.client.inspectResponse(sessionId, id);
  }

  async request(options = {}) {
    if (!options || typeof options !== "object" || Array.isArray(options)) {
      throw new Error("request options must be an object");
    }
    const session = await this._ensureSession(options);

    const method = (options.method || "GET").toUpperCase();
    const sutBaseUrl = options.sutBaseUrl || options.sut_base_url || session.sutBaseUrl || this.defaultSutBaseUrl;
    if (!sutBaseUrl) {
      throw new Error("sutBaseUrl is required");
    }

    const proxyBaseUrl = options.proxyBaseUrl || options.proxy_base_url || session.proxyListen;
    if (!proxyBaseUrl) {
      throw new Error("proxy listen address is not available; check start() response");
    }

    const targetUrl = toURL(options.path, sutBaseUrl);
    const proxyUrl = toURL(proxyBaseUrl.startsWith("http") ? proxyBaseUrl : `http://${proxyBaseUrl}`);

    const headers = { ...(options.headers || {}) };
    headers["X-Phrond-Session-Id"] = session.sessionId;

    return this._requestViaProxy(proxyUrl, targetUrl, {
      method,
      headers,
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

  // Compatibility helper that mirrors old stop semantics.
  async stop(options = {}) {
    if (this.activeSession?.sessionId) {
      await this.reset({ status: options.status || "pass" });
    }
    if (options.kill === false) {
      return { status: "stopped" };
    }
    return this.kill();
  }
}

async function start(config = {}) {
  if (!config || typeof config !== "object" || Array.isArray(config)) {
    throw new Error("start config must be an object");
  }

  const client = new CoreClient({
    baseUrl: config.baseUrl,
    timeoutMs: config.timeoutMs
  });

  const proxyConfig = normalizeComponentConfig(config.proxy, "proxy");
  const mockServerConfig = normalizeComponentConfig(config.mockServer, "mockServer");

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

  const runtimeInfo = await client.startRuntime(payload);
  return new Runtime(client, config, runtimeInfo);
}

module.exports = {
  start
};
