const { AsyncLocalStorage } = require("node:async_hooks");
const tracing = require("./tracing");
const DEFAULT_BASE_URL = "http://127.0.0.1:18000";

function toURL(value, baseUrl) {
  if (!value || typeof value !== "string") throw new Error("request path is required");
  try { return new URL(value); } catch (_) { /* resolve below */ }
  if (!baseUrl) throw new Error("base URL is required to resolve request path");
  return new URL(value, baseUrl);
}

function pickFirstString(...values) {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

function normalizeProxyConfig(config = {}) {
  if (config.proxy === false) throw new Error("the capture proxy cannot be disabled");
  if (config.proxy !== undefined && config.proxy !== null && config.proxy !== true &&
      (typeof config.proxy !== "object" || Array.isArray(config.proxy))) {
    throw new Error("proxy config must be an object or true");
  }
  const proxy = { ...(config.proxy === true ? {} : (config.proxy || {})) };
  if (proxy.listenAddr && !proxy.listen_addr) proxy.listen_addr = proxy.listenAddr;
  if (proxy.targetUrl && !proxy.target_url) proxy.target_url = proxy.targetUrl;
  if (config.sutBaseUrl && !proxy.target_url) proxy.target_url = config.sutBaseUrl;
  delete proxy.listenAddr;
  delete proxy.targetUrl;
  return proxy;
}

function normalizeGenerateSchema(value) {
  if (value === undefined || value === null || value === false) return null;
  if (value === true) return {};
  if (typeof value === "string") {
    if (!value.trim()) throw new Error("generateSchema output path must not be empty");
    return { outputPath: value.trim() };
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new Error("generateSchema must be true, an output path, or an options object");
  }
  const outputPath = pickFirstString(value.outputPath, value.output_path, value.output, value.path);
  const artifactsDir = pickFirstString(value.artifactsDir, value.artifacts_dir);
  const bundlePath = pickFirstString(value.bundlePath, value.bundle_path);
  const title = pickFirstString(value.title);
  const version = pickFirstString(value.version);
  return {
    ...(outputPath ? { outputPath } : {}),
    ...(artifactsDir ? { artifactsDir } : {}),
    ...(bundlePath ? { bundlePath } : {}),
    ...(title ? { title } : {}),
    ...(version ? { version } : {}),
    force: Boolean(value.force)
  };
}

function schemaConfiguration(config = {}) {
  const schemas = Array.isArray(config.schemas) ? config.schemas : [];
  const generateSchema = normalizeGenerateSchema(config.generateSchema ?? config.generate_schema);
  if (config.strictSchema === false || config.strict_schema === false) {
    throw new Error("strictSchema: false is no longer supported; supply schemas or opt in with generateSchema");
  }
  if (schemas.length > 0 && generateSchema) {
    throw new Error("schemas and generateSchema cannot be used together");
  }
  if (schemas.length === 0 && !generateSchema) {
    throw new Error("OpenAPI schemas are required; supply schemas or explicitly opt in with generateSchema");
  }
  return { schemas, generateSchema };
}

function normalizeDependencies(value) {
  if (value === undefined || value === null) return [];
  let entries;
  if (Array.isArray(value)) {
    entries = value;
  } else if (typeof value === "object") {
    entries = Object.entries(value).map(([assetId, dependency]) => ({ assetId, ...(dependency || {}) }));
  } else {
    throw new Error("dependencies must be an array or an object keyed by dependency asset ID");
  }
  const seen = new Set();
  return entries.map((dependency, index) => {
    if (!dependency || typeof dependency !== "object" || Array.isArray(dependency)) {
      throw new Error(`dependency ${index + 1} must be an object`);
    }
    const assetId = pickFirstString(dependency.assetId, dependency.asset_id, dependency.id, dependency.name);
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(assetId)) {
      throw new Error(`dependency ${index + 1} requires a valid assetId`);
    }
    if (seen.has(assetId)) throw new Error(`dependency assetId "${assetId}" is declared more than once`);
    seen.add(assetId);
    const upstreamUrl = pickFirstString(
      dependency.upstreamUrl, dependency.upstream_url, dependency.url, dependency.targetUrl, dependency.target_url
    );
    let parsed;
    try { parsed = new URL(upstreamUrl); } catch (_) { /* validated below */ }
    if (!parsed || !["http:", "https:"].includes(parsed.protocol)) {
      throw new Error(`dependency "${assetId}" requires an absolute http or https upstreamUrl`);
    }
    const schemas = Array.isArray(dependency.schemas) ? [...dependency.schemas] : [];
    if (dependency.schema) schemas.push(dependency.schema);
    if (schemas.length === 0) {
      throw new Error(`dependency "${assetId}" requires an OpenAPI schema`);
    }
    const assetName = pickFirstString(dependency.assetName, dependency.asset_name);
    const upstreamKind = pickFirstString(dependency.upstreamKind, dependency.upstream_kind) || "unknown";
    if (!["mock", "service", "unknown"].includes(upstreamKind)) {
      throw new Error(`dependency "${assetId}" upstreamKind must be mock, service, or unknown`);
    }
    return {
      asset_id: assetId,
      ...(assetName ? { asset_name: assetName } : {}),
      upstream_kind: upstreamKind,
      upstream_url: upstreamUrl,
      ...(schemas.length > 0 ? { schemas } : {})
    };
  });
}

class CoreClient {
  constructor(config = {}) {
    this.baseUrl = config.baseUrl || DEFAULT_BASE_URL;
    this.timeoutMs = config.timeoutMs || 0;
  }

  startServer(payload) { return this._request("POST", "/v1/startServer", payload || {}); }
  setupTest(payload) { return this._request("POST", "/v1/setupTest", payload || {}); }
  teardownTest(payload) { return this._request("POST", "/v1/teardownTest", payload || {}); }
  generateSchema(payload) { return this._request("POST", "/v1/generateSchema", payload || {}); }
  report(payload) { return this._request("POST", "/v1/report", payload || {}); }
  killServer() { return this._request("POST", "/v1/killServer", {}); }

  async _request(method, path, payload) {
    if (typeof fetch !== "function") throw new Error("global fetch is not available; require Node.js >= 18");
    const options = { method, headers: { "content-type": "application/json" } };
    if (payload !== undefined) options.body = JSON.stringify(payload);
    let timeout;
    if (this.timeoutMs > 0) {
      const controller = new AbortController();
      options.signal = controller.signal;
      timeout = setTimeout(() => controller.abort(), this.timeoutMs);
      timeout.unref?.();
    }
    try {
      const res = await tracing.withoutTracing(() => fetch(new URL(path, this.baseUrl), options));
      const text = await res.text();
      const body = text && (res.headers.get("content-type") || "").includes("application/json") ? JSON.parse(text) : (text || null);
      if (!res.ok) {
        const err = new Error(`request failed: ${res.status}${body?.error ? `: ${body.error}` : ""}`);
        err.status = res.status;
        err.body = body;
        throw err;
      }
      return body;
    } finally {
      if (timeout) clearTimeout(timeout);
    }
  }
}

class Runtime {
  constructor(client, config = {}, runtimeInfo = {}) {
    this.client = client;
    this.runtimeInfo = runtimeInfo;
    this.defaultSutBaseUrl = config.sutBaseUrl || null;
    const schemaConfig = schemaConfiguration(config);
    this.defaultSchemas = [...schemaConfig.schemas];
    this.generatedSchemaConfig = schemaConfig.generateSchema;
    this.defaultDependencies = normalizeDependencies(config.dependencies);
    this.apiId = pickFirstString(config.apiId, config.api_id);
    this.sutTargetKind = pickFirstString(config.sutTargetKind, config.sut_target_kind);
    this.testSource = config.testSource || config.test_source;
    this.sutSource = config.sutSource || config.sut_source;
    this.schemaGenerated = false;
    this.manualScenarioName = "";
    this.sutUrl = runtimeInfo.sut_url || null;
    this.sessions = new AsyncLocalStorage();
    this.manualSession = null;
    this.manageRuntime = config.manageRuntime !== false;
    this.explicitSession = Boolean(config.explicitSession);
    this.tracerProvider = config.tracerProvider;
    this.openSessions = new Set();
    this.pendingCleanup = new Set();
    this.http = {
      request: (options) => this._httpRequest(options),
      get: (path, options) => this._httpRequest({ ...options, method: "GET", path }),
      post: (path, body, options) => this._httpRequest({ ...options, method: "POST", path, body }),
      postJson: (path, body, options = {}) => this._httpRequest({
        ...options,
        method: "POST",
        path,
        body: JSON.stringify(body),
        headers: { "Content-Type": "application/json", ...(options.headers || {}) }
      })
    };
  }

  get pendingScenarioName() { return this.sessions.getStore()?.pendingScenarioName || (this.sessions.getStore() ? "" : this.manualScenarioName); }
  set pendingScenarioName(value) { const scope = this.sessions.getStore(); if (scope) scope.pendingScenarioName = value; else this.manualScenarioName = value; }

  get activeSession() { return this.sessions.getStore()?.session ?? (this.sessions.getStore() ? null : this.manualSession); }
  set activeSession(value) {
    const scope = this.sessions.getStore();
    if (scope) scope.session = value; else this.manualSession = value;
  }

  // Adapters own completion so runner timeouts, hooks, and assertion failures
  // can close the same attempt even when its callback never settles.
  _createTestAttempt(name, options = {}) {
    const scope = { session: null, closed: false };
    const ready = this.sessions.run(scope, () => this.setupTest({ ...options, name }));
    // A runner may execute hooks before invoking run/finish. Observe setup
    // rejection immediately while preserving it for those awaited paths.
    ready.catch(() => {});
    let finishing;
    return {
      run: async (fn) => {
        const session = await ready;
        if (scope.closed) throw new Error("capture attempt is already closed");
        return this.sessions.run(scope, () => tracing.api.context.with(
          tracing.api.trace.setSpan(tracing.api.ROOT_CONTEXT, session.span),
          async () => {
            if (typeof options.onSession === "function") await options.onSession(session);
            return fn(session);
          }
        ));
      },
      finish: (outcome = {}) => {
        if (!finishing) {
          scope.closed = true;
          finishing = ready.then(() => this.sessions.run(scope, () => this.teardownTest({
            ...outcome, drainTimeoutMs: options.drainTimeoutMs
          })));
        }
        return finishing;
      }
    };
  }

  async runTest(name, fn, options = {}) {
    const attempt = this._createTestAttempt(name, options);
    let result, failure, failed = false;
    try { result = await attempt.run(fn); }
    catch (err) { failed = true; failure = err; }
    try { await attempt.finish({ status: failed ? "fail" : "pass", error: failure }); }
    catch (err) { if (!failed) { failed = true; failure = err; } }
    if (failed) throw failure;
    return result;
  }

  scenarioName(name) {
    if (typeof name !== "string" || !name.trim()) throw new Error("scenario name is required");
    this.pendingScenarioName = name.trim();
    return this.pendingScenarioName;
  }

  dependencyUrl(assetId) {
    if (typeof assetId !== "string" || !assetId.trim()) throw new Error("dependency assetId is required");
    const selected = assetId.trim();
    const dependencies = this.activeSession?.dependencyUrls;
    if (dependencies && dependencies[selected]) return dependencies[selected];
    if (!this.defaultDependencies.some((dependency) => dependency.asset_id === selected)) {
      throw new Error(`dependency "${selected}" is not configured`);
    }
    const proxyBase = this.sutUrl || this.runtimeInfo?.proxy?.listen_addr;
    if (!proxyBase) throw new Error("proxy listen address is not available");
    const base = proxyBase.startsWith("http") ? proxyBase : `http://${proxyBase}`;
    return `${base.replace(/\/$/, "")}/__phrond/dep/${encodeURIComponent(selected)}`;
  }

  async setupTest(options = {}) {
    const owner = this.sessions.getStore() || this;
    if (owner.closed) throw new Error("capture attempt is already closed");
    if (this.activeSession || owner.setupPending) throw new Error("a capture session is already active");
    const selectedSchemas = Array.isArray(options.schemas) ? options.schemas : this.defaultSchemas;
    const selectedGeneration = options.generateSchema === undefined && options.generate_schema === undefined
      ? this.generatedSchemaConfig
      : normalizeGenerateSchema(options.generateSchema ?? options.generate_schema);
    const schemaConfig = schemaConfiguration({ schemas: selectedSchemas, generateSchema: selectedGeneration });
    const dependencies = options.dependencies === undefined
      ? this.defaultDependencies
      : normalizeDependencies(options.dependencies);
    const payload = {
      name: pickFirstString(options.name, options.scenarioName, this.pendingScenarioName) || undefined,
      tags: options.tags,
      meta: options.meta,
      api_id: pickFirstString(options.apiId, options.api_id, this.apiId) || undefined,
      sut_target_kind: pickFirstString(options.sutTargetKind, options.sut_target_kind, this.sutTargetKind) || undefined,
      test_source: options.testSource || options.test_source || this.testSource,
      sut_source: options.sutSource || options.sut_source || this.sutSource,
      sut_base_url: options.sutBaseUrl || options.sut_base_url || this.defaultSutBaseUrl,
      explicit_session: Boolean(options.explicitSession ?? options.explicit_session ?? this.explicitSession),
      schemas: schemaConfig.schemas
    };
    if (dependencies.length > 0) payload.dependencies = dependencies;
    if (schemaConfig.generateSchema) payload.generate_schema = true;
    const span = tracing.newTestSpan(payload.name || "test", this.tracerProvider);
    payload.trace_context = tracing.serialize(span);
    let session;
    owner.setupPending = true;
    try { session = await this.client.setupTest(payload); }
    catch (err) { tracing.finish(span, err); throw err; }
    finally { owner.setupPending = false; }
    this.openSessions.add(session.session_id);
    this.activeSession = {
      span,
      traceContext: payload.trace_context,
      sessionId: session.session_id,
      name: session.name,
      explicit: Boolean(session.explicit),
      sutBaseUrl: payload.sut_base_url,
      proxyListen: session.proxy_listen || this.runtimeInfo?.proxy?.listen_addr,
      sutUrl: session.sut_url || this.sutUrl,
      runId: session.run_id,
      bundlePath: session.bundle_path,
      indexPath: session.index_path,
      viewerPath: session.viewer_path,
      dependencyUrls: session.dependencies || Object.fromEntries(dependencies.map((dependency) => [
        dependency.asset_id,
        `${String(session.proxy_listen || this.sutUrl || "").replace(/\/$/, "")}/__phrond/dep/${encodeURIComponent(dependency.asset_id)}`
      ])),
      generateSchema: schemaConfig.generateSchema
    };
    this.pendingScenarioName = "";
    return this.activeSession;
  }

  async teardownTest(options = {}) {
    const sessionId = options.sessionId || options.session_id || this.activeSession?.sessionId;
    if (!sessionId) return { status: "idle" };
    const session = this.activeSession;
    if (session?.sessionId === sessionId && this.sessions.getStore()) this.sessions.getStore().closed = true;
    const generation = session?.generateSchema || this.generatedSchemaConfig;
    let failure;
    try {
      const response = await this.client.teardownTest({ session_id: sessionId, status: options.status || "pass",
        ...(options.drainTimeoutMs !== undefined ? { drain_timeout_ms: options.drainTimeoutMs } : {}) });
      this.openSessions.delete(sessionId);
      if (session?.sessionId === sessionId) this.activeSession = null;
      const validationStatus = response?.schema_validation?.status;
      if (validationStatus && validationStatus !== "pass" && (!generation || validationStatus === "fail")) {
        const errors = response.schema_validation.errors || [];
        const err = new Error(errors.map((entry) => entry.message || "schema violation").join("; ") || "schema validation failed");
        err.schemaValidation = response.schema_validation;
        err.response = response;
        throw err;
      }
      if (response?.artifact?.capture_incomplete || (response?.artifact?.testcase?.status === "fail" && options.status !== "fail")) {
        const err = new Error(response.artifact.capture_diagnostics?.join("; ") || "captured test failed");
        err.response = response;
        throw err;
      }
      return response;
    } catch (err) { failure = err; throw err; }
    finally {
      if (session?.sessionId === sessionId) tracing.finish(session.span, failure || options.error || (options.status === "fail" ? new Error("test failed") : null));
    }
  }

  report(options = {}) {
    return this.client.report({
      bundle_path: options.bundlePath || options.bundle_path,
      artifacts_dir: options.artifactsDir || options.artifacts_dir,
      sut_name: options.sutName || options.sut_name,
      include_artifacts: options.includeArtifacts || options.include_artifacts || false,
      workflow_path: options.workflowPath || options.workflow_path,
      html_path: options.htmlPath || options.html_path
    });
  }

  async generateSchema(options) {
    const config = options === undefined ? this.generatedSchemaConfig : normalizeGenerateSchema(options);
    if (!config) throw new Error("generateSchema was not enabled for this capture runtime");
    const response = await this.client.generateSchema({
      bundle_path: config.bundlePath,
      artifacts_dir: config.artifactsDir,
      output_path: config.outputPath,
      title: config.title,
      version: config.version,
      force: Boolean(config.force)
    });
    this.schemaGenerated = true;
    return response;
  }

  async killServer() {
    await Promise.all(this.pendingCleanup);
    if (this.openSessions.size) throw new Error("teardown all capture sessions before closing the runtime");
    if (!this.manageRuntime) return { status: "shared" };
    let generationResult;
    let generationError;
    if (this.generatedSchemaConfig && !this.schemaGenerated) {
      try {
        generationResult = await this.generateSchema();
      } catch (err) {
        generationError = err;
      }
    }
    let killResult;
    try {
      killResult = await this.client.killServer();
    } catch (killError) {
      if (generationError) throw new AggregateError([generationError, killError], "schema generation and Phrond shutdown failed");
      throw killError;
    }
    if (generationError) throw generationError;
    return generationResult ? { ...killResult, generatedSchema: generationResult } : killResult;
  }

  async _ensureSession(options = {}) {
    if (this.sessions.getStore()?.closed) throw new Error("capture attempt is already closed");
    return this.activeSession || this.setupTest(options);
  }

  async _httpRequest(options = {}) {
    if (!options || typeof options !== "object" || Array.isArray(options)) throw new Error("request options must be an object");
    const session = await this._ensureSession(options);
    const proxyBaseUrl = options.proxyBaseUrl || options.proxy_base_url || session.proxyListen;
    if (!proxyBaseUrl) throw new Error("proxy listen address is not available");
    const proxyUrl = toURL(proxyBaseUrl.startsWith("http") ? proxyBaseUrl : `http://${proxyBaseUrl}`);
    const defaultTarget = session.sutBaseUrl || this.defaultSutBaseUrl;
    const overrideTarget = options.sutBaseUrl || options.sut_base_url;
    const forward = Boolean(overrideTarget && overrideTarget !== defaultTarget);
    const targetUrl = forward ? toURL(options.path, overrideTarget) : toURL(options.path, proxyUrl.href);
    const headers = { ...(options.headers || {}), "X-Phrond-Session-Id": session.sessionId };
    if (forward) headers.Host = targetUrl.host;
    return tracing.api.context.with(tracing.api.trace.setSpan(tracing.api.ROOT_CONTEXT, session.span), () => this._requestViaProxy(proxyUrl, targetUrl, {
      method: String(options.method || "GET").toUpperCase(), headers, body: options.body, forward
    }));
  }

  _requestViaProxy(proxyUrl, targetUrl, options) {
    const headers = { ...options.headers };
    let body = options.body;
    if (body !== undefined && body !== null) {
      if (!Buffer.isBuffer(body) && typeof body !== "string" && !(body instanceof Uint8Array)) {
        throw new Error("request body must be a string, Buffer, or Uint8Array; use postJson for objects");
      }
      if (!headers["Content-Length"] && !headers["content-length"]) headers["Content-Length"] = Buffer.byteLength(body);
    }
    const client = proxyUrl.protocol === "https:" ? require("node:https") : require("node:http");
    return new Promise((resolve, reject) => {
      const req = client.request({
        host: proxyUrl.hostname,
        port: proxyUrl.port || (proxyUrl.protocol === "https:" ? 443 : 80),
        method: options.method,
        path: options.forward ? targetUrl.href : targetUrl.pathname + targetUrl.search,
        headers
      }, (res) => {
        const chunks = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => {
          const raw = Buffer.concat(chunks).toString("utf8");
          let parsed = raw || null;
          if (raw && String(res.headers["content-type"] || "").includes("application/json")) {
            try { parsed = JSON.parse(raw); } catch (err) { reject(err); return; }
          }
          const response = { status: res.statusCode || 0, headers: res.headers, body: parsed };
          if (response.status >= 400) {
            const err = new Error(`proxy request failed: ${response.status}`);
            Object.assign(err, response);
            reject(err);
            return;
          }
          resolve(response);
        });
      });
      if (this.client.timeoutMs > 0) req.setTimeout(this.client.timeoutMs, () => req.destroy(new Error("proxy request timed out")));
      req.on("error", reject);
      if (body !== undefined && body !== null) req.write(body);
      req.end();
    });
  }
}

async function startCapture(config = {}) {
  if (!config || typeof config !== "object" || Array.isArray(config)) throw new Error("startCapture config must be an object");
  tracing.initializeTracing({ instrumentation: config.instrumentation !== false });
  const schemaConfig = schemaConfiguration(config);
  const dependencies = normalizeDependencies(config.dependencies);
  const runtimeConfig = { ...config, schemas: schemaConfig.schemas, generateSchema: schemaConfig.generateSchema, dependencies };
  const client = new CoreClient({ baseUrl: config.baseUrl, timeoutMs: config.timeoutMs });
  const runtimeInfo = await client.startServer({ proxy: normalizeProxyConfig(config) });
  return new Runtime(client, runtimeConfig, runtimeInfo);
}

const startServer = startCapture;

function bindTest(runtimeOrGetter, testFn, defaults = {}) {
  const resolveRuntime = typeof runtimeOrGetter === "function" ? runtimeOrGetter : () => runtimeOrGetter;
  if (typeof testFn !== "function") throw new Error("bindTest requires a test function");
  return function phrondTest(name, optionsOrFn, maybeFn) {
    const fn = typeof optionsOrFn === "function" ? optionsOrFn : maybeFn;
    const testOptions = typeof optionsOrFn === "function" ? undefined : optionsOrFn;
    const run = async (t) => {
      const runtime = resolveRuntime();
      if (!runtime) throw new Error("bindTest could not resolve a Phrond runtime");
      if (runtime._createTestAttempt && typeof t?.test === "function" && typeof t?.after === "function") {
        return require("./node-test").runNodeTest(runtime, t, name, fn, defaults);
      }
      if (runtime.runTest) return runtime.runTest(name, (session) => fn(t, session), defaults);
      const session = await runtime.setupTest({ ...defaults, name });
      try {
        if (typeof defaults.onSession === "function") await defaults.onSession(session);
        await fn(t, session);
      } catch (err) {
        try { await runtime.teardownTest({ status: "fail" }); } catch (_) { /* preserve test failure */ }
        throw err;
      }
      await runtime.teardownTest({ status: "pass" });
    };
    return testOptions === undefined ? testFn(name, run) : testFn(name, testOptions, run);
  };
}

function createNodeTestSuite(config = {}) {
  schemaConfiguration(config);
  normalizeDependencies(config.dependencies);
  const testApi = config.testApi || require("node:test");
  if (!testApi || typeof testApi.test !== "function" || typeof testApi.before !== "function" || typeof testApi.after !== "function") {
    throw new Error("createNodeTestSuite testApi must provide test, before, and after functions");
  }
  let runtime = null;
  let starting = null;
  const runtimeConfig = { ...config };
  delete runtimeConfig.testApi;
  delete runtimeConfig.testDefaults;
  delete runtimeConfig.autoHooks;
  const suite = {
    async start() {
      if (runtime) return runtime;
      if (!starting) starting = startCapture(runtimeConfig).then((value) => (runtime = value)).finally(() => { starting = null; });
      return starting;
    },
    async close() {
      if (starting) await starting;
      if (!runtime) return { status: "idle" };
      const active = runtime;
      runtime = null;
      return active.killServer();
    },
    get runtime() { if (!runtime) throw new Error("Phrond capture runtime is not started yet"); return runtime; },
    get sutUrl() { return this.runtime.sutUrl; },
    get http() { return this.runtime.http; },
    dependencyUrl(assetId) { return this.runtime.dependencyUrl(assetId); },
    generateSchema(options) { return this.runtime.generateSchema(options); },
    report(options) { return this.runtime.report(options); }
  };
  suite.test = bindTest(() => runtime, testApi.test, config.testDefaults || {});
  if (config.autoHooks !== false) {
    testApi.before(() => suite.start());
    testApi.after(() => suite.close());
  }
  return suite;
}

module.exports = { startCapture, startServer, bindTest, createNodeTestSuite, initializeTracing: tracing.initializeTracing };
