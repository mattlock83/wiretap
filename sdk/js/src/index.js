const tracing = require("./tracing");
const { CoreClient } = require("./control-client");

function rejectRemovedRedactions(options) {
  if (options && Object.hasOwn(options, "redactions")) {
    throw new Error("The redactions option was removed. Wiretap retains test traffic unchanged; use synthetic data and dummy secrets.");
  }
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

function normalizeSchemas(value) {
  if (value === undefined) return undefined;
  if (!Array.isArray(value)) throw new Error("schemas must be an array");
  for (const schema of value) {
    if (!schema || typeof schema !== "object" || Array.isArray(schema)) throw new Error("each schema must be an object");
    if (Object.hasOwn(schema, "role")) throw new Error("schema role is assigned by its SUT or dependency location");
  }
  return value;
}

function normalizeDependencies(value) {
  if (value === undefined) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("dependencies must be an object keyed by dependency asset ID");
  }
  return Object.entries(value).map(([assetId, dependency]) => {
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(assetId)) throw new Error(`dependency "${assetId}" requires a valid asset ID`);
    if (!dependency || typeof dependency !== "object" || Array.isArray(dependency)) throw new Error(`dependency "${assetId}" must be an object`);
    if (Object.hasOwn(dependency, "assetId")) throw new Error("dependency asset ID is its object key");
    if (Object.hasOwn(dependency, "schema")) throw new Error(`dependency "${assetId}" uses schemas, an array of OpenAPI schemas`);
    if (Object.hasOwn(dependency, "protocol")) throw new Error(`dependency "${assetId}" protocol was removed; Wiretap supports HTTP APIs`);
    return {
      asset_id: assetId,
      asset_name: dependency.assetName,
      upstream_kind: dependency.upstreamKind,
      upstream_url: dependency.upstreamUrl,
      schemas: normalizeSchemas(dependency.schemas),
      schema_required: dependency.schemaRequired
    };
  });
}

function capturePayload(options) {
  if (Object.hasOwn(options, "sutProtocol")) throw new Error("sutProtocol was removed; Wiretap supports HTTP APIs");
  const identity = options.identity;
  return {
    application: options.application,
    test_identity: identity ? { id: identity.id, variant: identity.variant, attempt_ordinal: identity.attemptOrdinal } : undefined,
    name: options.name,
    tags: options.tags,
    meta: options.meta,
    api_id: options.apiId,
    sut_target_kind: options.sutTargetKind,
    test_source: options.testSource,
    sut_source: options.sutSource,
    sut_base_url: options.sutBaseUrl,
    explicit_session: options.explicitSession,
    schemas: normalizeSchemas(options.schemas),
    schema_required: options.schemaRequired,
    dependencies: normalizeDependencies(options.dependencies)
  };
}

// The Jest environment owns attempts; the managed owner alone finalizes runs.
class Runtime {
  constructor(client, config = {}) {
    this.client = client;
    this.config = config;
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
    // Only explicitly supplied SDK options travel over the control API. Go
    // applies project defaults after decoding, including omitted versus empty.
    const payload = capturePayload({ ...this.config, ...options });
    const span = tracing.newTestSpan(payload.name || "test", this.config.tracerProvider);
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
  capturePayload(config);
  const client = new CoreClient({ baseUrl: config.baseUrl, timeoutMs: config.timeoutMs });
  await client.startRun({ proxy: normalizeProxyConfig(config), application: config.application });
  return new Runtime(client, config);
}

module.exports = { startCapture, initializeTracing: tracing.initializeTracing };
