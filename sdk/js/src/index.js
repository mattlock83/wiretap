const tracing = require("./tracing");
const { CoreClient } = require("./control-client");

function rejectRemovedRedactions(options) {
  if (options && Object.hasOwn(options, "redactions")) {
    throw new Error("The redactions option was removed. Wiretap retains test traffic unchanged; use synthetic data and dummy secrets.");
  }
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
  const proxy = config.proxy === true ? {} : (config.proxy || {});
  return {
    listen_addr: proxy.listenAddr,
    target_url: proxy.targetUrl || config.sutBaseUrl
  };
}

function schemaConfiguration(config = {}, allowInherited = false) {
  const schemas = Array.isArray(config.schemas) ? config.schemas : [];
  if (Object.hasOwn(config, "sutProtocol")) throw new Error("sutProtocol was removed; Wiretap supports HTTP APIs");
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
    if (Object.hasOwn(dependency, "protocol")) throw new Error(`dependency "${assetId}" protocol was removed; Wiretap supports HTTP APIs`);
    let parsed;
    try { parsed = new URL(upstreamUrl); } catch (_) { /* validated below */ }
    if (!parsed || !["http:", "https:"].includes(parsed.protocol)) {
      throw new Error(`dependency "${assetId}" requires an absolute http or https upstreamUrl`);
    }
    const schemas = Array.isArray(dependency.schemas) ? [...dependency.schemas] : [];
    if (dependency.schema) schemas.push(dependency.schema);
    if (dependency.schemaRequired != null && typeof dependency.schemaRequired !== "boolean") {
      throw new Error(`dependency "${assetId}" schemaRequired must be a boolean`);
    }
    if (schemas.length === 0 && dependency.schemaRequired === true) {
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
      ...(typeof dependency.schemaRequired === "boolean" ? { schema_required: dependency.schemaRequired } : {}),
      upstream_url: upstreamUrl,
      ...(schemas.length > 0 ? { schemas } : {})
    };
  });
}

// The Jest environment owns attempts; the managed owner alone finalizes runs.
class Runtime {
  constructor(client, config = {}) {
    this.client = client;
    this.config = config;
    this.schemas = schemaConfiguration(config).schemas;
    this.dependencies = normalizeDependencies(config.dependencies);
  }

  createTestAttempt(name, options = {}) {
    rejectRemovedRedactions(options);
    const ready = this.#startSession({ ...options, name });
    // Observe setup errors even when a fixture fails before running the callback.
    ready.catch(() => {});
    let closed = false, finishing, sessionInitialization;
    return {
      run: async (fn) => {
        const session = await ready;
        if (closed) throw new Error("capture attempt is already closed");
        return tracing.api.context.with(
          tracing.api.trace.setSpan(tracing.api.ROOT_CONTEXT, session.span),
          async () => {
            if (typeof options.onSession === "function") {
              sessionInitialization ??= Promise.resolve().then(() => options.onSession(session));
              await sessionInitialization;
            }
            // A timeout may finish an attempt while its async fixture is settling.
            if (closed) throw new Error("capture attempt is already closed");
            return fn(session);
          }
        );
      },
      finish: (outcome = {}) => {
        if (!finishing) {
          closed = true;
          finishing = ready.then(async session => {
            let failure;
            try {
              const response = await this.client.teardownTest({
                session_id: session.sessionId,
                status: outcome.status || "pass",
                ...(options.drainTimeoutMs !== undefined ? { drain_timeout_ms: options.drainTimeoutMs } : {})
              });
              if (response?.capture?.capture_incomplete) {
                const error = new Error(response.capture.capture_diagnostics?.join("; ") || "Wiretap capture incomplete");
                error.response = response;
                throw error;
              }
              return response;
            } catch (error) { failure = error; throw error; }
            finally {
              tracing.finish(session.span, failure || outcome.error || (outcome.status === "fail" ? new Error("test failed") : null));
            }
          });
        }
        return finishing;
      }
    };
  }

  async #startSession(options) {
    if (Object.hasOwn(options, "sutProtocol")) throw new Error("sutProtocol was removed; Wiretap supports HTTP APIs");
    const defaults = this.config;
    const { schemas } = schemaConfiguration({
      schemas: Array.isArray(options.schemas) ? options.schemas : this.schemas,
      schemaRequired: options.schemaRequired ?? defaults.schemaRequired
    });
    const identity = options.identity;
    const payload = {
      application: options.application ?? defaults.application,
      test_identity: identity ? { id: identity.id, variant: identity.variant, attempt_ordinal: identity.attemptOrdinal } : undefined,
      name: pickFirstString(options.name) || undefined,
      tags: options.tags,
      meta: options.meta,
      api_id: pickFirstString(options.apiId, defaults.apiId) || undefined,
      sut_target_kind: pickFirstString(options.sutTargetKind, defaults.sutTargetKind) || undefined,
      test_source: options.testSource || defaults.testSource,
      sut_source: options.sutSource || defaults.sutSource,
      sut_base_url: options.sutBaseUrl || defaults.sutBaseUrl,
      explicit_session: Boolean(options.explicitSession ?? defaults.explicitSession),
      schemas,
      schema_required: options.schemaRequired ?? defaults.schemaRequired,
      dependencies: options.dependencies === undefined ? this.dependencies : normalizeDependencies(options.dependencies)
    };
    const span = tracing.newTestSpan(payload.name || "test", defaults.tracerProvider);
    payload.trace_context = tracing.serialize(span);
    let session;
    try { session = await this.client.setupTest(payload); }
    catch (error) { tracing.finish(span, error); throw error; }
    return {
      span,
      traceContext: payload.trace_context,
      sessionId: session.session_id,
      name: session.name,
      sutUrl: session.sut_url,
      runId: session.run_id,
      bundlePath: session.bundle_path,
      dependencyUrls: session.dependencies || {}
    };
  }
}

async function startCapture(config = {}) {
  if (!config || typeof config !== "object" || Array.isArray(config)) throw new Error("startCapture config must be an object");
  rejectRemovedRedactions(config);
  tracing.initializeTracing({ instrumentation: config.instrumentation !== false });
  schemaConfiguration(config, true);
  if (config.dependencies !== undefined) normalizeDependencies(config.dependencies);
  const client = new CoreClient({ baseUrl: config.baseUrl, timeoutMs: config.timeoutMs });
  const runtimeInfo = await client.startRun({ proxy: normalizeProxyConfig(config), application: config.application });
  const defaults = runtimeInfo.defaults || {};
  const resolved = {
    application: defaults.application,
    sutBaseUrl: defaults.sut_base_url,
    apiId: defaults.api_id,
    sutTargetKind: defaults.sut_target_kind,
    schemas: defaults.schemas,
    schemaRequired: defaults.schema_required,
    testSource: defaults.test_source,
    sutSource: defaults.sut_source,
    dependencies: defaults.dependencies?.map(dependency => ({
      assetId: dependency.asset_id,
      assetName: dependency.asset_name,
      upstreamUrl: dependency.upstream_url,
      upstreamKind: dependency.upstream_kind,
      schemas: dependency.schemas,
      schema: dependency.schema,
      schemaRequired: dependency.schema_required
    })),
    ...config
  };
  return new Runtime(client, resolved);
}

module.exports = { startCapture, initializeTracing: tracing.initializeTracing };
