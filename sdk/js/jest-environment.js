// Jest evaluates modules in a VM with its own loader. Install instrumentation
// in the host so Node builtins and native fetch share the SDK's OTel context.
const { TestEnvironment } = require('jest-environment-node');
const sdk = require('./src');

class WiretapEnvironment extends TestEnvironment {
  attempts = new WeakMap();
  wrappedHooks = new WeakMap();

  async setup() {
    await super.setup();
    sdk.initializeTracing({ instrumentation: this.global.process.env.WIRETAP_EXISTING_OTEL !== '1' });
    this.global.__wiretapSDK = sdk;
  }
  async handleTestEvent(event, state) {
    const test = event.test;
    if (event.name === 'test_started' && test.fn?.__wiretapStart) {
      const names = [];
      for (let current = test; current?.parent; current = current.parent) names.unshift(current.name);
      const pending = test.fn.__wiretapStart(names.join(' '));
      test.fn.__wiretapAttempt = pending;
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
        if (original.constructor?.name === 'GeneratorFunction' && !original.length) {
          hook.fn = function*(...args) {
            const attempt = yield pending;
            const iterator = original.apply(this, args);
            let state = yield attempt.run(() => iterator.next());
            while (!state.done) {
              try {
                const value = yield state.value;
                state = yield attempt.run(() => iterator.next(value));
              } catch (error) {
                state = yield attempt.run(() => iterator.throw(error));
              }
            }
            return state.value;
          };
        } else if (original.length) {
          hook.fn = function(done) {
            const receiver = this;
            pending.then(attempt => attempt.run(() => {
              const returned = original.call(receiver, done);
              if (returned !== undefined) throw new Error("Jest hooks must either accept done or return a promise, not both");
            })).catch(done);
          };
        } else {
          hook.fn = function(...args) {
            const receiver = this;
            return pending.then(attempt => attempt.run(() => original.apply(receiver, args)));
          };
        }
      }
    }
    if ((event.name === 'hook_success' || event.name === 'hook_failure') && this.wrappedHooks.has(event.hook)) {
      event.hook.fn = this.wrappedHooks.get(event.hook);
      this.wrappedHooks.delete(event.hook);
    }
    if (event.name === 'test_done' && this.attempts.has(test)) {
      const pending = this.attempts.get(test);
      this.attempts.delete(test);
      try {
        const attempt = await pending;
        const failure = test.errors[0];
        await attempt.finish({
          status: test.errors.length ? 'fail' : 'pass',
          error: Array.isArray(failure) ? failure[0] : failure
        });
      } catch (error) {
        // Preserve the runner's original failure and add a cleanup failure only
        // when it is the reason an otherwise successful test must fail.
        if (!test.errors.length) test.errors.push(error);
      }
    }
  }
}
module.exports = WiretapEnvironment;
