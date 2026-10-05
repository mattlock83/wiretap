const tracing = require('./tracing');
const { W3CTraceContextPropagator } = require('@opentelemetry/core');

const events = {
  beforeExplore: 'before_explore', beforeCase: 'before_case',
  beforeRequest: 'before_request', checkCase: 'check_case',
  afterCase: 'after_case', afterExplore: 'after_explore'
};
const propagator = new W3CTraceContextPropagator();

class ExplorationError extends Error {
  constructor(message, result, cause) {
    super(message, cause ? { cause } : undefined);
    this.name = 'ExplorationError';
    this.result = result;
  }
}

function callbacksFor(hooks = {}) {
  const descriptors = [], callbacks = new Map();
  for (const [name, definitions] of Object.entries(hooks)) {
    if (!events[name]) throw new Error(`unknown exploration hook: ${name}`);
    for (const definition of Array.isArray(definitions) ? definitions : [definitions]) {
      const hook = typeof definition === 'function' ? { handler: definition } : definition;
      if (!hook || typeof hook.handler !== 'function') throw new Error(`${name} requires a hook callback`);
      const id = `hook-${descriptors.length + 1}`;
      descriptors.push({ id, event: events[name], ...(hook.operations ? { operations: hook.operations } : {}) });
      callbacks.set(id, hook.handler);
    }
  }
  return { descriptors, callbacks };
}

function positiveTimeout(value, fallback, name) {
  const timeout = value ?? fallback;
  if (!Number.isFinite(timeout) || timeout <= 0) throw new Error(`${name} must be positive`);
  return timeout;
}
function abortError(signal) {
  return signal.reason instanceof Error ? signal.reason : new Error('exploration cancelled');
}
async function invokeHook(callback, input, outerSignal, timeoutMs) {
  const controller = new AbortController();
  const abort = () => controller.abort(abortError(outerSignal));
  if (outerSignal.aborted) abort();
  else outerSignal.addEventListener('abort', abort, { once: true });
  const timer = setTimeout(() => controller.abort(new Error('exploration hook timed out')), timeoutMs);
  const signal = controller.signal;
  let listener;
  const interrupted = new Promise((_, reject) => {
    listener = () => reject(abortError(signal));
    if (signal.aborted) listener();
    else signal.addEventListener('abort', listener, { once: true });
  });
  try {
    // A generated case owns its own trace. Inherit neither the parent's span
    // nor unrelated process context when the server does not supply one.
    const context = propagator.extract(tracing.api.ROOT_CONTEXT, input.trace_context || {}, tracing.api.defaultTextMapGetter);
    return await Promise.race([
      tracing.api.context.with(context, () => Promise.resolve().then(() => {
        if (signal.aborted) throw abortError(signal);
        return callback({ ...input, signal });
      })),
      interrupted
    ]);
  } finally {
    clearTimeout(timer);
    outerSignal.removeEventListener('abort', abort);
    signal.removeEventListener('abort', listener);
  }
}

async function explore(runtime, options = {}) {
  const session = runtime.activeSession;
  if (!session) throw new Error('explore requires an active capture test');
  if (session.explorationPending) throw new Error('an exploration is already active for this test');
  if (options.signal?.aborted) throw abortError(options.signal);
  const { descriptors, callbacks } = callbacksFor(options.hooks);
  const hookTimeout = positiveTimeout(options.hookTimeoutMs, 30000, 'hookTimeoutMs');
  const cleanupTimeout = positiveTimeout(options.cleanupTimeoutMs, 30000, 'cleanupTimeoutMs');
  if (options.requestTimeoutMs !== undefined) positiveTimeout(options.requestTimeoutMs, 15000, 'requestTimeoutMs');
  if (options.maxCases !== undefined && (!Number.isInteger(options.maxCases) || options.maxCases < 0)) throw new Error('maxCases must be a nonnegative integer');
  const bindings = options.bindings?.map(binding => ({ name: binding.name, in: binding.in, path: binding.path, ...(binding.flowId ? { flow_id: binding.flowId } : {}), ...(binding.messageIndex !== undefined ? { message_index: binding.messageIndex } : {}) }));
  const extract = options.extract?.map(value => ({ name: value.name, in: value.in, path: value.path, flow_id: value.flowId, ...(value.messageIndex !== undefined ? { message_index: value.messageIndex } : {}) }));
  session.explorationPending = true;
  let cleanupTimer, cleanupController, originalError, result, terminal;
  const controller = new AbortController();
  let resolveDone;
  session.explorationDone = new Promise(resolve => { resolveDone = resolve; });
  session.cancelExploration = () => controller.abort(new Error('enclosing test ended before exploration completed'));
  const cancel = () => controller.abort(abortError(options.signal));
  options.signal?.addEventListener('abort', cancel, { once: true });
  let signal = controller.signal;
  let ids;
  const beginCleanup = async error => {
    originalError ||= error;
    if (cleanupController) throw error;
    cleanupController = new AbortController();
    cleanupTimer = setTimeout(() => cleanupController.abort(new Error('exploration cleanup timed out')), cleanupTimeout);
    signal = cleanupController.signal;
    await runtime.client.cancelExploration(ids, { signal });
  };
  try {
    // Complete registration before observing cancellation so we always have
    // the execution ID needed to cancel server-side work.
    const registration = new AbortController();
    const registrationTimer = setTimeout(() => registration.abort(new Error('exploration registration timed out')), 30000);
    let started;
    try { started = await runtime.client.startExploration({
      session_id: session.sessionId,
      ...(options.operations ? { operations: options.operations } : {}),
      ...(options.flowId ? { flow_id: options.flowId } : {}),
      ...(options.maxCases !== undefined ? { max_cases: options.maxCases } : {}),
      ...(options.requestTimeoutMs !== undefined ? { request_timeout: `${options.requestTimeoutMs}ms` } : {}),
      hook_timeout: `${hookTimeout}ms`, hooks: descriptors,
      ...(bindings ? { bindings } : {}), ...(extract ? { extract } : {}),
      ...(options.expectedStatuses ? { expected_statuses: options.expectedStatuses } : {}),
      ...(options.expectedGrpcStatuses ? { expected_grpc_statuses: options.expectedGrpcStatuses } : {}),
      ...(options.redactions ? { redactions: options.redactions } : {})
    }, { signal: registration.signal }); }
    finally { clearTimeout(registrationTimer); }
    ids = { session_id: session.sessionId, exploration_id: started.exploration_id };
    if (!ids.exploration_id) throw new Error('exploration start did not return an execution ID');
    if (signal.aborted) await beginCleanup(abortError(signal));
    while (true) {
      let next;
      try { next = await runtime.client.nextExploration(ids, { signal }); }
      catch (error) { await beginCleanup(error); continue; }
      if (signal.aborted && !cleanupController) {
        await beginCleanup(abortError(signal));
        continue;
      }
      if (next.state !== 'running') {
        terminal = next;
        if (next.result) {
          const { bundle_path, ...counts } = next.result;
          result = { ...counts, ...(bundle_path ? { bundlePath: bundle_path } : {}) };
        }
        break;
      }
      if (!next.action) continue;
      const action = next.action;
      // A poll already in flight when cancellation completed can return an
      // expired setup/request action. Only cleanup is still dispatchable.
      if (cleanupController && !['after_case', 'after_explore'].includes(action.event)) continue;
      let output, error;
      try {
        const callback = callbacks.get(action.hook_id);
        if (!callback) throw new Error(`unregistered exploration hook: ${action.hook_id}`);
        output = await invokeHook(callback, action.input || {}, signal, hookTimeout);
      } catch (failure) {
        const callbackError = failure instanceof Error ? failure : new Error(String(failure));
        originalError ||= callbackError;
        error = callbackError.message || 'exploration hook failed';
      }
      try { await runtime.client.respondExploration({ ...ids, action_id: action.id, ...(output ? { output } : {}), ...(error ? { error } : {}) }, { signal }); }
      catch (failure) {
        if (cleanupController && failure.status === 409 && failure.body?.error === 'hook action is no longer pending') continue;
        await beginCleanup(failure);
      }
    }
    if (originalError || terminal.state !== 'complete' || result?.failed > 0) {
      throw new ExplorationError(terminal.error || originalError?.message || `exploration ${terminal.state === 'complete' ? 'checks failed' : terminal.state}`, result, originalError);
    }
    return result;
  } finally {
    clearTimeout(cleanupTimer);
    options.signal?.removeEventListener('abort', cancel);
    session.explorationPending = false;
    delete session.cancelExploration;
    resolveDone();
  }
}
module.exports = { explore, ExplorationError };
