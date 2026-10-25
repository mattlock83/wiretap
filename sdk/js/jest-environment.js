// Jest evaluates modules in a VM with its own loader. Install instrumentation
// in the host so Node builtins and native fetch share the SDK's OTel context.
const { TestEnvironment } = require('jest-environment-node');
const { createHash } = require('node:crypto');
const path = require('node:path');
const sdk = require('./src');

function fullTestName(test) {
  const names = [];
  for (let current = test; current?.parent; current = current.parent) names.unshift(current.name);
  return names.join(' ');
}

function automaticIdentity(file, name, ordinal) {
  // Encode the components so punctuation in a filename or test name cannot
  // collide with the duplicate-registration suffix.
  const key = `jest:${JSON.stringify([file, name])}${ordinal ? `#${ordinal}` : ''}`;
  return { id: Buffer.byteLength(key) <= 256 ? key : `jest:sha256:${createHash('sha256').update(key).digest('hex')}` };
}

function inAttempt(original, pending) {
  if (original.constructor?.name === 'GeneratorFunction' && !original.length) {
    return function*(...args) {
      const attempt = yield pending;
      const iterator = original.apply(this, args);
      let step = yield attempt.run(() => iterator.next());
      while (!step.done) {
        try {
          const value = yield step.value;
          step = yield attempt.run(() => iterator.next(value));
        } catch (error) {
          step = yield attempt.run(() => iterator.throw(error));
        }
      }
      return step.value;
    };
  }
  if (original.length) {
    return function(done) {
      const receiver = this;
      pending.then(attempt => attempt.run(() => {
        const returned = original.call(receiver, done);
        if (returned !== undefined) throw new Error('Jest callbacks must either accept done or return a promise, not both');
      })).catch(done);
    };
  }
  return function(...args) {
    const receiver = this;
    return pending.then(attempt => attempt.run(() => original.apply(receiver, args)));
  };
}

class WiretapEnvironment extends TestEnvironment {
  attempts = new WeakMap();
  wrappedHooks = new WeakMap();
  wrappedTests = new WeakMap();
  automaticIdentities = new WeakMap();

  constructor(config, context) {
    super(config, context);
    this.relativeTestPath = path.relative(config.projectConfig.rootDir, context.testPath).split(path.sep).join('/');
    this.captureOptions = config.projectConfig.testEnvironmentOptions?.wiretap || {};
    this.projectRoot = config.projectConfig.rootDir;
    if (this.captureOptions.autoCapture && config.globalConfig.maxWorkers > 1 && !this.captureOptions.explicitSession) {
      throw new Error('Wiretap Jest capture uses one worker by default; parallel workers require explicitSession: true and trace propagation from the SUT');
    }
  }

  async setup() {
    await super.setup();
    sdk.initializeTracing({ instrumentation: this.global.process.env.WIRETAP_EXISTING_OTEL !== '1' });
    this.global.__wiretapSDK = sdk;
    this.global.__wiretapJestApp = { module: this.captureOptions.app, rootDir: this.projectRoot };
  }
  async teardown() {
    try {
      await this.global.__wiretapCleanupApp?.();
    } finally {
      await super.teardown();
    }
  }
  registerIdentities(root) {
    const tests = [];
    const visit = block => {
      for (const child of block.children) {
        if (child.type === 'test') tests.push(child);
        else visit(child);
      }
    };
    visit(root);
    const totals = new Map();
    for (const test of tests) {
      const name = fullTestName(test);
      totals.set(name, (totals.get(name) || 0) + 1);
    }
    const ordinals = new Map();
    for (const test of tests) {
      const name = fullTestName(test);
      const ordinal = (ordinals.get(name) || 0) + 1;
      ordinals.set(name, ordinal);
      this.automaticIdentities.set(test, automaticIdentity(this.relativeTestPath, name, totals.get(name) > 1 ? ordinal : undefined));
    }
  }
  async handleTestEvent(event, state) {
    // Capture registration order before Jest randomizes execution or retries a
    // test. Each TestEntry keeps its identity for all of its attempts.
    if (event.name === 'run_start') this.registerIdentities(state.rootDescribeBlock);
    const test = event.test;
    if (event.name === 'test_started' && test.fn && (test.fn.__wiretapStart || this.captureOptions.autoCapture || this.global.__wiretapCaptureConfig)) {
      const name = fullTestName(test);
      let pending;
      if (test.fn.__wiretapStart) {
        pending = test.fn.__wiretapStart(name);
        test.fn.__wiretapAttempt = pending;
      } else {
        const { autoCapture, app, managed, globalSetup, globalTeardown, ...defaults } = this.captureOptions;
        const config = { ...defaults, ...this.global.__wiretapCaptureConfig, manageRuntime: false };
        this.runtime ||= sdk.startCapture(config);
        pending = this.runtime.then(runtime => runtime.createTestAttempt(name, {
          ...config, ...config.testDefaults,
          identity: config.testDefaults?.identity ?? config.identity ?? this.automaticIdentities.get(test),
          explicitSession: test.concurrent || (config.testDefaults?.explicitSession ?? config.explicitSession ?? false)
        }));
        this.wrappedTests.set(test, test.fn);
        test.fn = inAttempt(test.fn, pending);
      }
      this.attempts.set(test, pending);
      // Observe setup errors through the test callback/completion path.
      pending.catch(() => {});
    }
    if (event.name === 'hook_start' && ['beforeEach', 'afterEach'].includes(event.hook.type)) {
      // Jest 30 runs ordinary per-test hooks after test_started. Concurrent
      // tests do not run these hooks. Wrap the invoked hook only, restoring it
      // on completion so nested suites, retries and unwrapped tests keep their
      // original registrations and ordering.
      const pending = this.attempts.get(state.currentlyRunningTest);
      if (pending) {
        const hook = event.hook;
        const original = hook.fn;
        this.wrappedHooks.set(hook, original);
        hook.fn = inAttempt(original, pending);
      }
    }
    if ((event.name === 'hook_success' || event.name === 'hook_failure') && this.wrappedHooks.has(event.hook)) {
      event.hook.fn = this.wrappedHooks.get(event.hook);
      this.wrappedHooks.delete(event.hook);
    }
    if (event.name === 'test_done' && this.attempts.has(test)) {
      const pending = this.attempts.get(test);
      this.attempts.delete(test);
      if (this.wrappedTests.has(test)) {
        test.fn = this.wrappedTests.get(test);
        this.wrappedTests.delete(test);
      }
      try {
        const attempt = await pending;
        const failure = test.errors[0];
        await attempt.finish({
          status: test.errors.length ? 'fail' : 'pass',
          error: Array.isArray(failure) ? failure[0] : failure
        });
      } catch (error) {
        // Keep the runner's original failure first, while retaining cleanup
        // diagnostics even when the test or a user hook already failed.
        test.errors.push(error);
      }
    }
  }
}
module.exports = WiretapEnvironment;
