const { createHash } = require('node:crypto');
const http = require('node:http');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { startCapture } = require('./index');
const { startManagedRuntime } = require('./managed-runtime');
const tracing = require('./tracing');

const stateKey = Symbol.for('wiretap.mocha.adapter');
const optionsKey = Symbol.for('wiretap.mocha.options');

function cliFlag(name, alias) {
  let enabled;
  for (const arg of process.argv.slice(2)) {
    if (arg === `--${name}` || arg === alias || arg === `--${name}=true`) enabled = true;
    if (arg === `--no-${name}` || arg === `--${name}=false`) enabled = false;
  }
  return enabled;
}

function assertSupported(config = {}) {
  if (config.parallel || cliFlag('parallel', '-p') || process.env.MOCHA_WORKER_ID !== undefined) {
    throw new Error('Wiretap Mocha capture requires serial execution; remove parallel: true and --parallel');
  }
  if (config.watch || cliFlag('watch', '-w')) {
    throw new Error('Wiretap Mocha capture requires a single run; disable watch mode');
  }
  if (config.enableGlobalTeardown === false || cliFlag('enable-global-teardown') === false) {
    throw new Error('Wiretap Mocha capture requires global teardown to finalize exports and stop the owned runtime');
  }
}

function withWiretap(config = {}, options = {}) {
  assertSupported(config);
  if (options.signal) throw new Error('withWiretap manages cancellation through Mocha; signal is only supported by startManagedRuntime');
  if (options.app !== undefined && typeof options.app !== 'string' && typeof options.app !== 'function') {
    throw new TypeError('Wiretap app must be a module path or Node HTTP request handler');
  }
  globalThis[optionsKey] = options;
  const required = config.require === undefined ? [] : Array.isArray(config.require) ? config.require : [config.require];
  // Last preserves other plugins' global teardown ordering. The capture root
  // hook explicitly runs first among beforeEach hooks once suites are loaded.
  return { ...config, require: [...required, require.resolve('../mocha')] };
}

async function loadApp(specifier, cwd) {
  if (typeof specifier === 'function') return specifier;
  const absolute = path.isAbsolute(specifier) ? specifier
    : require.resolve(specifier.startsWith('.') ? path.resolve(cwd, specifier) : specifier, { paths: [cwd] });
  let app;
  try { app = require(absolute); }
  catch (error) {
    if (!['ERR_REQUIRE_ESM', 'ERR_REQUIRE_ASYNC_MODULE'].includes(error.code)) throw error;
    app = await import(pathToFileURL(absolute).href);
  }
  app = app.default || app;
  if (typeof app !== 'function') throw new TypeError(`Wiretap app module ${specifier} must export a Node HTTP request handler`);
  return app;
}

function identity(test, cwd, ordinal) {
  const file = test.file ? path.relative(cwd, test.file).split(path.sep).join('/') : '<unknown>';
  const key = `mocha:${JSON.stringify([file, test.fullTitle()])}${ordinal ? `#${ordinal}` : ''}`;
  return {
    id: Buffer.byteLength(key) <= 256 ? key : `mocha:sha256:${createHash('sha256').update(key).digest('hex')}`,
    attemptOrdinal: test.currentRetry() + 1
  };
}

function collectSuite(suite, hooks, suiteHooks, ordinals, counts) {
  for (const hook of [...suite._beforeEach, ...suite._afterEach]) hooks.add(hook);
  for (const hook of [...suite._beforeAll, ...suite._afterAll]) suiteHooks.add(hook);
  for (const test of suite.tests) {
    const key = JSON.stringify([test.file, test.fullTitle()]);
    const ordinal = counts.get(key) || 0;
    counts.set(key, ordinal + 1);
    ordinals.set(test, ordinal);
  }
  for (const child of suite.suites) collectSuite(child, hooks, suiteHooks, ordinals, counts);
}

function installLifecycle(state, root, beforeEach) {
  state.root = root;
  const hooks = new WeakSet(), suiteHooks = new WeakSet(), ordinals = new WeakMap();
  collectSuite(root, hooks, suiteHooks, ordinals, new Map());
  for (const hook of root._beforeEach) if (hook.fn === beforeEach) hooks.delete(hook);
  // Root plugins can appear after existing plugins in --require. Capture must
  // start before their per-test fixtures, without changing their relative order.
  const index = root._beforeEach.findIndex(hook => hook.fn === beforeEach);
  if (index > 0) root._beforeEach.unshift(...root._beforeEach.splice(index, 1));

  root.afterEach('Wiretap: finish capture', async function () {
    this.timeout(state.hookTimeout);
    await state.finishAttempt();
  });
  root.afterAll('Wiretap: finish remaining capture', async function () {
    this.timeout(state.hookTimeout);
    await state.finishAttempt();
  });

  // Native Runnable.run retains Mocha's callback/promise checks, timeout clock,
  // context, skip behavior and retry cloning. Only its invocation context is
  // changed; user test functions and reporter events remain untouched.
  let prototype = Object.getPrototypeOf(root._beforeEach[0]);
  while (prototype && !Object.hasOwn(prototype, 'run')) prototype = Object.getPrototypeOf(prototype);
  if (!prototype) throw new Error('Unsupported Mocha runner: Runnable.run is unavailable');
  const original = prototype.run;
  const wrapped = function (callback) {
    let parent = this.parent;
    while (parent?.parent) parent = parent.parent;
    if (parent === root && this.fn === beforeEach) {
      // The SDK setup hook can itself time out while onSession is still
      // pending. Observe Mocha's completion callback without trying to enter
      // the not-yet-initialized attempt a second time.
      const invocation = { entry: null, closed: false };
      const previous = state.setupInvocation;
      state.setupInvocation = invocation;
      try {
        return original.call(this, error => {
          invocation.closed = true;
          if (error && invocation.entry) invocation.entry.error ??= error;
          callback(error);
        });
      } finally { state.setupInvocation = previous; }
    }
    const entry = state.active;
    if (parent === root && suiteHooks.has(this) && entry) {
      // A failing root afterEach skips later hooks, including our finalizer.
      // Close that attempt before any suite-wide cleanup or next suite setup.
      void state.finishAttempt().then(() => original.call(this, callback), callback);
      return;
    }
    if (parent !== root || !entry || !(this.type === 'test' || hooks.has(this))) return original.call(this, callback);
    const runnable = this;
    const onError = error => { entry.error ??= error; };
    runnable.on('error', onError);
    entry.listeners.push([runnable, onError]);
    const completed = error => {
      if (error) entry.error ??= error;
      tracing.api.context.with(tracing.api.ROOT_CONTEXT, () => callback(error));
    };
    // Context is captured per invocation. A timed-out callback can settle after
    // the next attempt starts without acquiring that next attempt's identity.
    void entry.attempt.run(() => original.call(runnable, completed)).catch(completed);
  };
  prototype.run = wrapped;
  state.restoreRunnable = () => { if (prototype.run === wrapped) prototype.run = original; };
  state.ordinals = ordinals;
}

function installFixtureGuards(state, beforeEach) {
  const imported = require('mocha');
  const Mocha = imported.default || imported;
  const setup = Mocha.prototype.runGlobalSetup;
  const teardown = Mocha.prototype.runGlobalTeardown;
  const loadFiles = Mocha.prototype.loadFilesAsync;
  const owns = instance => instance.suite === state.root || instance.suite?._beforeEach.some(hook => hook.fn === beforeEach);
  const guardedSetup = async function (...args) {
    try { return await setup.apply(this, args); }
    catch (error) {
      if (owns(this)) {
        process.exitCode ||= 1;
        try { await state.close(); } catch (cleanupError) { error.cleanupError = cleanupError; }
      }
      throw error;
    }
  };
  const guardedTeardown = async function (...args) {
    let result, failure;
    try { result = await teardown.apply(this, args); } catch (error) { failure = error; }
    if (owns(this)) {
      try { await state.close(); }
      catch (error) { if (failure) failure.cleanupError = error; else failure = error; }
    }
    if (failure) {
      if (owns(this)) process.exitCode ||= 1;
      throw failure;
    }
    return result;
  };
  const guardedLoad = async function (...args) {
    try { return await loadFiles.apply(this, args); }
    catch (error) {
      if (owns(this)) {
        try { await state.close(); } catch (cleanupError) { error.cleanupError = cleanupError; }
      }
      throw error;
    }
  };
  Mocha.prototype.runGlobalSetup = guardedSetup;
  Mocha.prototype.runGlobalTeardown = guardedTeardown;
  Mocha.prototype.loadFilesAsync = guardedLoad;
  state.restoreFixtures = () => {
    if (Mocha.prototype.runGlobalSetup === guardedSetup) Mocha.prototype.runGlobalSetup = setup;
    if (Mocha.prototype.runGlobalTeardown === guardedTeardown) Mocha.prototype.runGlobalTeardown = teardown;
    if (Mocha.prototype.loadFilesAsync === guardedLoad) Mocha.prototype.loadFilesAsync = loadFiles;
  };
}

async function mochaHooks() {
  assertSupported();
  if (globalThis[stateKey]) throw new Error('Wiretap Mocha must be registered once per process');
  const options = globalThis[optionsKey] || {};
  const { app, capture = {}, explicitSession = false, ...managed } = options;
  const cwd = path.resolve(options.cwd || process.cwd());
  const state = {
    cwd,
    abort: new AbortController(),
    previousEnv: { WIRETAP_BASE_URL: process.env.WIRETAP_BASE_URL, WIRETAP_SUT_URL: process.env.WIRETAP_SUT_URL },
    hookTimeout: (options.timeoutMs ?? 30000) + (capture.testDefaults?.drainTimeoutMs ?? 5000) + 10000,
    active: null
  };
  globalThis[stateKey] = state;
  state.finishAttempt = async () => {
    const entry = state.active;
    if (!entry) return;
    state.active = null;
    for (const [runnable, listener] of entry.listeners) runnable.removeListener('error', listener);
    const skipped = entry.test.pending || entry.test.state === 'pending';
    const failed = entry.error || entry.test.state === 'failed';
    await entry.attempt.finish({ status: failed ? 'fail' : skipped ? 'skip' : 'pass', error: entry.error });
  };
  state.close = () => state.closing ??= (async () => {
    let failure;
    if (state.starting && !state.owner) {
      try { state.owner = await state.starting; } catch { /* startup error is reported by the plugin loader */ }
    }
    try { await state.finishAttempt(); } catch (error) { failure = error; }
    try { await state.runtime?.finishRun(); } catch (error) { failure ??= error; }
    try {
      if (state.server?.listening) await new Promise((resolve, reject) => {
        state.server.close(error => error ? reject(error) : resolve());
        state.server.closeAllConnections();
      });
    } catch (error) { failure ??= error; }
    try { await state.owner?.close(); } catch (error) { failure ??= error; }
    state.restoreRunnable?.();
    state.restoreFixtures?.();
    process.removeListener('SIGINT', state.onInterrupt);
    process.removeListener('SIGTERM', state.onTerminate);
    for (const [key, value] of Object.entries(state.previousEnv)) {
      if (value === undefined) delete process.env[key]; else process.env[key] = value;
    }
    delete globalThis[stateKey];
    if (failure) throw failure;
  })();
  const interrupt = signal => {
    if (state.interrupted) return;
    state.interrupted = true;
    if (!state.owner) state.abort.abort(new Error(`Mocha interrupted by ${signal}`));
    if (state.active) state.active.error ??= new Error(`Mocha interrupted by ${signal}`);
    const code = signal === 'SIGINT' ? 130 : 143;
    const deadline = setTimeout(() => process.exit(code), state.hookTimeout + 2 * (options.shutdownTimeoutMs ?? 5000));
    deadline.unref();
    void state.close().catch(error => console.error(error)).finally(() => {
      clearTimeout(deadline);
      // Mocha's default exit listener sets its test-failure count. Cancellation
      // has its own conventional exit code, including after the run completed.
      process.once('exit', () => { process.exitCode = code; });
      process.exit(code);
    });
  };
  state.onInterrupt = () => interrupt('SIGINT');
  state.onTerminate = () => interrupt('SIGTERM');
  process.once('SIGINT', state.onInterrupt);
  process.once('SIGTERM', state.onTerminate);
  try {
    installFixtureGuards(state, beforeEach);
    state.starting = startManagedRuntime({ ...managed, outputs: managed.outputs ?? capture.outputs, cwd, signal: state.abort.signal });
    state.owner = await state.starting;
    if (state.interrupted) throw new Error('Mocha capture startup was interrupted');
    process.env.WIRETAP_BASE_URL = state.owner.baseUrl;
    process.env.WIRETAP_SUT_URL = state.owner.sutUrl;
    // Install tracing before loading the user's app or test modules.
    state.runtime = await startCapture({ ...capture, baseUrl: state.owner.baseUrl, manageRuntime: false, explicitSession });
    if (app !== undefined) {
      state.server = http.createServer(await loadApp(app, cwd));
      await new Promise((resolve, reject) => {
        state.server.once('error', reject);
        state.server.listen(0, '127.0.0.1', resolve);
      });
      state.sutBaseUrl = `http://127.0.0.1:${state.server.address().port}`;
    }
  } catch (error) {
    try { await state.close(); } catch (cleanupError) { error.cleanupError = cleanupError; }
    throw error;
  }
  async function beforeEach() {
    const invocation = state.setupInvocation;
    this.timeout(state.hookTimeout);
    await state.finishAttempt();
    if (invocation?.closed) throw new Error('Wiretap setup hook has already completed');
    const test = this.currentTest;
    const original = test.retriedTest?.() || test;
    const attempt = state.runtime.createTestAttempt(test.fullTitle(), {
      ...capture,
      ...capture.testDefaults,
      identity: identity(test, cwd, state.ordinals.get(original) || 0),
      ...(state.sutBaseUrl ? { sutBaseUrl: state.sutBaseUrl } : {}),
      explicitSession
    });
    const entry = { test, attempt, listeners: [] };
    state.active = entry;
    if (invocation) invocation.entry = entry;
    // Surface setup failure inside this hook, before user fixtures can run.
    try { await attempt.run(() => {}); }
    catch (error) { entry.error ??= error; throw error; }
  }
  return {
    beforeAll: function wiretapLifecycle() { installLifecycle(state, this.test.parent, beforeEach); },
    beforeEach
  };
}

async function mochaGlobalTeardown() {
  // Mocha invokes every registered global fixture through runGlobalTeardown.
  // Its guard closes after the full list, including when an earlier user
  // fixture throws and Mocha skips the remaining entries in that list.
}

module.exports = { withWiretap, mochaHooks, mochaGlobalTeardown };
