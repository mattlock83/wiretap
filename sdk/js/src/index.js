const { AsyncLocalStorage } = require("node:async_hooks");
const tracing = require("./tracing");
const DEFAULT_BASE_URL = "http://127.0.0.1:18000";

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
  const proxy = config.proxy === true ? {} : (config.proxy || {});
  return {
    listen_addr: proxy.listenAddr,
    target_url: proxy.targetUrl || config.sutBaseUrl
  };
}

function schemaConfiguration(config = {}, allowInherited = false) {
  const schemas = Array.isArray(config.schemas) ? config.schemas : [];
  if (config.sutProtocol === "grpc") {
    if (config.schemaRequired === false) throw new Error("gRPC requires a Protobuf contract");
    return { schemas };
  }
  if (schemas.length === 0 && config.schemaRequired !== false && !allowInherited) {
    throw new Error("OpenAPI schemas are required; supply schemas or explicitly set schemaRequired: false for this API");
  }
  return { schemas };
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
    const assetId = pickFirstString(dependency.assetId);
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(assetId)) {
      throw new Error(`dependency ${index + 1} requires a valid assetId`);
    }
    if (seen.has(assetId)) throw new Error(`dependency assetId "${assetId}" is declared more than once`);
    seen.add(assetId);
    const upstreamUrl = pickFirstString(dependency.upstreamUrl);
    const protocol = pickFirstString(dependency.protocol) || "http";
    if (!["http", "grpc"].includes(protocol)) throw new Error(`dependency "${assetId}" protocol must be http or grpc`);
    let parsed;
    try { parsed = new URL(upstreamUrl); } catch (_) { /* validated below */ }
    if (!parsed || !["http:", "https:"].includes(parsed.protocol)) {
      throw new Error(`dependency "${assetId}" requires an absolute http or https upstreamUrl`);
    }
    const schemas = Array.isArray(dependency.schemas) ? [...dependency.schemas] : [];
    if (dependency.schema) schemas.push(dependency.schema);
    if (protocol === "grpc" && dependency.schemaRequired === false) {
      throw new Error(`dependency "${assetId}" requires a Protobuf contract`);
    }
    if (protocol === "http" && schemas.length === 0 && dependency.schemaRequired !== false) {
      throw new Error(`dependency "${assetId}" requires an OpenAPI schema`);
    }
    const assetName = pickFirstString(dependency.assetName);
    const upstreamKind = pickFirstString(dependency.upstreamKind) || "unknown";
    if (!["mock", "service", "unknown"].includes(upstreamKind)) {
      throw new Error(`dependency "${assetId}" upstreamKind must be mock, service, or unknown`);
    }
    return {
      asset_id: assetId,
      ...(assetName ? { asset_name: assetName } : {}),
      upstream_kind: upstreamKind,
      ...(protocol === "grpc" ? { protocol } : {}),
      ...(dependency.schemaRequired === false ? { schema_required: false } : {}),
      upstream_url: upstreamUrl,
      ...(schemas.length > 0 ? { schemas } : {})
    };
  });
}

class CoreClient {
  constructor(config = {}) {
    this.baseUrl = config.baseUrl || process.env.WIRETAP_BASE_URL || DEFAULT_BASE_URL;
    this.timeoutMs = config.timeoutMs ?? 30000;
    if (!Number.isSafeInteger(this.timeoutMs) || this.timeoutMs < 0 || this.timeoutMs > 2147483647) {
      throw new Error("timeoutMs must be an integer between 0 and 2147483647 (0 disables the control request deadline)");
    }
    this.explicitTimeout = config.timeoutMs !== undefined;
  }

  startRun(payload) { return this._request("POST", "/v1/startRun", payload || {}); }
  setupTest(payload) { return this._request("POST", "/v1/setupTest", payload || {}); }
  teardownTest(payload) { return this._request("POST", "/v1/teardownTest", payload || {}); }
  report(payload) { return this._request("POST", "/v1/report", payload || {}); }
  finishRun(payload = {}) { return this._request("POST", "/v1/finishRun", payload); }

  async _request(method, path, payload) {
    if (typeof fetch !== "function") throw new Error("global fetch is not available; require Node.js >= 18");
    const options = { method, headers: { "content-type": "application/json" } };
    if (payload !== undefined) options.body = JSON.stringify(payload);
    // A default cleanup deadline must allow the requested runtime drain. An
    // explicitly configured deadline (including zero) always belongs to the caller.
    const timeoutMs = !this.explicitTimeout && path === "/v1/teardownTest"
      ? Math.max(this.timeoutMs, (payload?.drain_timeout_ms ?? 5000) + 10000)
      : this.timeoutMs;
    let timeout, controller, deadlineError;
    if (timeoutMs > 0) {
      controller = new AbortController();
      options.signal = controller.signal;
      timeout = setTimeout(() => {
        deadlineError = new Error(`Wiretap control request ${method} ${path} timed out after ${timeoutMs}ms`);
        deadlineError.name = "TimeoutError";
        deadlineError.code = "WIRETAP_CONTROL_TIMEOUT";
        controller.abort(deadlineError);
      }, timeoutMs);
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
    } catch (error) {
      throw deadlineError || error;
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
    this.sutProtocol = config.sutProtocol || "http";
    if (!["http", "grpc"].includes(this.sutProtocol)) throw new Error("sutProtocol must be http or grpc");
    const schemaConfig = schemaConfiguration(config);
    this.defaultSchemas = [...schemaConfig.schemas];
    this.schemaRequired = config.schemaRequired;
    this.defaultDependencies = normalizeDependencies(config.dependencies);
    this.defaultRedactions = config.redactions;
    this.application = config.application;
    this.apiId = pickFirstString(config.apiId);
    this.sutTargetKind = pickFirstString(config.sutTargetKind);
    this.testSource = config.testSource;
    this.sutSource = config.sutSource;
    this.outputs = config.outputs;
    this.sutUrl = runtimeInfo.sut_url;
    this.runDependencyUrls = runtimeInfo.dependencies || {};
    this.sessions = new AsyncLocalStorage();
    this.manualSession = null;
    this.manageRuntime = config.manageRuntime !== false;
    this.explicitSession = Boolean(config.explicitSession);
    this.tracerProvider = config.tracerProvider;
    this.openSessions = new Set();
    this.pendingCleanup = new Set();

  }

  get activeSession() { return this.sessions.getStore()?.session ?? (this.sessions.getStore() ? null : this.manualSession); }
  set activeSession(value) {
    const scope = this.sessions.getStore();
    if (scope) scope.session = value; else this.manualSession = value;
  }

  // Framework completion hooks own the outcome independently of callback
  // settlement. Keep this handle per attempt so fixtures and retries are isolated.
  createTestAttempt(name, options = {}) {
    const scope = { session: null, closed: false };
    const ready = this.sessions.run(scope, () => this.setupTest({ ...options, name, identity: options.identity || (typeof name === "string" && name.trim() && Buffer.byteLength(name) <= 256 ? { id: name } : undefined) }));
    // A runner may execute hooks before invoking run/finish. Observe setup
    // rejection immediately while preserving it for those awaited paths.
    ready.catch(() => {});
    let finishing;
    let sessionInitialization;
    return {
      run: async (fn) => {
        const session = await ready;
        if (scope.closed) throw new Error("capture attempt is already closed");
        return this.sessions.run(scope, () => tracing.api.context.with(
          tracing.api.trace.setSpan(tracing.api.ROOT_CONTEXT, session.span),
          async () => {
            if (typeof options.onSession === "function") {
              // Separate fixture callbacks share one attempt. Initialize the
              // session once even when setup, test and cleanup activate it.
              sessionInitialization ??= Promise.resolve().then(() => options.onSession(session));
              await sessionInitialization;
            }
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
    const attempt = this.createTestAttempt(name, options);
    let result, failure, failed = false;
    try { result = await attempt.run(fn); }
    catch (err) { failed = true; failure = err; }
    try { await attempt.finish({ status: failed ? "fail" : "pass", error: failure }); }
    catch (err) { if (!failed) { failed = true; failure = err; } }
    if (failed) throw failure;
    return result;
  }

  dependencyUrl(assetId) {
    if (typeof assetId !== "string" || !assetId.trim()) throw new Error("dependency assetId is required");
    const selected = assetId.trim();
    const dependencies = this.activeSession?.dependencyUrls;
    if (dependencies && dependencies[selected]) return dependencies[selected];
    if (this.runDependencyUrls[selected]) return this.runDependencyUrls[selected];
    if (!this.defaultDependencies.some((dependency) => dependency.asset_id === selected)) {
      throw new Error(`dependency "${selected}" is not configured`);
    }
    const proxyBase = this.sutUrl;
    if (!proxyBase) throw new Error("proxy listen address is not available");
    const base = proxyBase.startsWith("http") ? proxyBase : `http://${proxyBase}`;
    return `${base.replace(/\/$/, "")}/__wiretap/dep/${encodeURIComponent(selected)}`;
  }

  async setupTest(options = {}) {
    const owner = this.sessions.getStore() || this;
    if (owner.closed) throw new Error("capture attempt is already closed");
    if (this.activeSession || owner.setupPending) throw new Error("a capture session is already active");
    const selectedSchemas = Array.isArray(options.schemas) ? options.schemas : this.defaultSchemas;
    const sutProtocol = options.sutProtocol || this.sutProtocol;
    const schemaConfig = schemaConfiguration({ schemas: selectedSchemas, schemaRequired: options.schemaRequired ?? this.schemaRequired, sutProtocol });
    const dependencies = options.dependencies === undefined
      ? this.defaultDependencies
      : normalizeDependencies(options.dependencies);
    const payload = {
      application: options.application ?? this.application,
      test_identity: options.identity ? { id: options.identity.id, variant: options.identity.variant, attempt_ordinal: options.identity.attemptOrdinal } : undefined,
      name: pickFirstString(options.name) || undefined,
      tags: options.tags,
      meta: options.meta,
      api_id: pickFirstString(options.apiId, this.apiId) || undefined,
      sut_target_kind: pickFirstString(options.sutTargetKind, this.sutTargetKind) || undefined,
      test_source: options.testSource || this.testSource,
      sut_source: options.sutSource || this.sutSource,
      sut_base_url: options.sutBaseUrl || this.defaultSutBaseUrl,
      ...(sutProtocol === "grpc" ? { sut_protocol: "grpc" } : {}),
      explicit_session: Boolean(options.explicitSession ?? this.explicitSession),
      schemas: schemaConfig.schemas,
      schema_required: options.schemaRequired ?? this.schemaRequired,
      redactions: options.redactions ?? this.defaultRedactions,
      dependencies
    };
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
      proxyListen: session.proxy_listen,
      sutUrl: session.sut_url,
      runId: session.run_id,
      bundlePath: session.bundle_path,
      dependencyUrls: session.dependencies,
    };
    return this.activeSession;
  }

  async teardownTest(options = {}) {
    const sessionId = options.sessionId || this.activeSession?.sessionId;
    if (!sessionId) return { status: "idle" };
    const session = this.activeSession;
    if (session?.sessionId === sessionId && this.sessions.getStore()) this.sessions.getStore().closed = true;
    let failure;
    try {
      const response = await this.client.teardownTest({ session_id: sessionId, status: options.status || "pass",
        ...(options.drainTimeoutMs !== undefined ? { drain_timeout_ms: options.drainTimeoutMs } : {}) });
      this.openSessions.delete(sessionId);
      if (session?.sessionId === sessionId) this.activeSession = null;
      const validationStatus = response?.capture?.schema_validation?.status;
      if (validationStatus === "fail") {
        const errors = response.capture.schema_validation.errors || [];
        const err = new Error(errors.map((entry) => entry.message || "schema violation").join("; ") || "schema validation failed");
        err.schemaValidation = response.capture.schema_validation;
        err.response = response;
        throw err;
      }
      if (response?.capture?.capture_incomplete || (response?.capture?.testcase?.status === "fail" && options.status !== "fail")) {
        const err = new Error(response.capture.capture_diagnostics?.join("; ") || "captured test failed");
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
    return this.client.report({ bundle_path: options.bundlePath });
  }

  async finishRun(options = {}) {
    await Promise.all(this.pendingCleanup);
    if (this.openSessions.size) throw new Error("teardown all capture sessions before closing the runtime");
    if (!this.manageRuntime) return { status: "shared" };
    return this.client.finishRun({ outputs: options.outputs ?? this.outputs });
  }
}

async function startCapture(config = {}) {
  if (!config || typeof config !== "object" || Array.isArray(config)) throw new Error("startCapture config must be an object");
  tracing.initializeTracing({ instrumentation: config.instrumentation !== false });
  schemaConfiguration(config, true);
  if (config.dependencies !== undefined) normalizeDependencies(config.dependencies);
  const client = new CoreClient({ baseUrl: config.baseUrl, timeoutMs: config.timeoutMs });
  const runtimeInfo = await client.startRun({ proxy: normalizeProxyConfig(config), application: config.application });
  const defaults = runtimeInfo.defaults || {};
  const resolved = {
    application: defaults.application,
    sutBaseUrl: defaults.sut_base_url,
    sutProtocol: defaults.sut_protocol,
    apiId: defaults.api_id,
    sutTargetKind: defaults.sut_target_kind,
    schemas: defaults.schemas,
    schemaRequired: defaults.schema_required,
    redactions: defaults.redactions,
    testSource: defaults.test_source,
    sutSource: defaults.sut_source,
    dependencies: defaults.dependencies?.map(dependency => ({
      assetId: dependency.asset_id,
      assetName: dependency.asset_name,
      upstreamUrl: dependency.upstream_url,
      upstreamKind: dependency.upstream_kind,
      protocol: dependency.protocol,
      schemas: dependency.schemas,
      schema: dependency.schema,
      schemaRequired: dependency.schema_required
    })),
    ...config
  };
  return new Runtime(client, resolved, runtimeInfo);
}

function bindTest(runtimeOrGetter, testFn, defaults = {}) {
  const resolveRuntime = typeof runtimeOrGetter === "function" ? runtimeOrGetter : () => runtimeOrGetter;
  if (typeof testFn !== "function") throw new Error("bindTest requires a test function");
  return function wiretapTest(name, optionsOrFn, maybeFn) {
    const fn = typeof optionsOrFn === "function" ? optionsOrFn : maybeFn;
    const testOptions = typeof optionsOrFn === "function" ? undefined : optionsOrFn;
    const run = async (t) => {
      const runtime = resolveRuntime();
      if (!runtime) throw new Error("bindTest could not resolve a Wiretap runtime");
      if (typeof t?.test === "function" && typeof t?.after === "function") {
        return require("./node-test").runNodeTest(runtime, t, name, fn, defaults);
      }
      return runtime.runTest(name, session => fn(t, session), defaults);
    };
    return testOptions === undefined ? testFn(name, run) : testFn(name, testOptions, run);
  };
}

function createNodeTestSuite(config = {}) {
  schemaConfiguration(config, true);
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
      return active.finishRun();
    },
    get runtime() { if (!runtime) throw new Error("Wiretap capture runtime is not started yet"); return runtime; },
    get sutUrl() { return this.runtime.sutUrl; },
    dependencyUrl(assetId) { return this.runtime.dependencyUrl(assetId); },
    report(options) { return this.runtime.report(options); }
  };
  suite.test = bindTest(() => runtime, testApi.test, config.testDefaults || {});
  if (config.autoHooks !== false) {
    testApi.before(() => suite.start());
    testApi.after(() => suite.close());
  }
  return suite;
}

module.exports = { startCapture, bindTest, createNodeTestSuite, initializeTracing: tracing.initializeTracing };
