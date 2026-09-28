// Jest evaluates modules in a VM with its own loader. Install instrumentation
// in the host so Node builtins and native fetch share the SDK's OTel context.
const { TestEnvironment } = require('jest-environment-node');
const sdk = require('./src');

class PhrondEnvironment extends TestEnvironment {
  attempts = new WeakMap();

  async setup() {
    await super.setup();
    sdk.initializeTracing({ instrumentation: this.global.process.env.PHROND_EXISTING_OTEL !== '1' });
    this.global.__phrondSDK = sdk;
  }
  async handleTestEvent(event) {
    const test = event.test;
    if (event.name === 'test_started' && test.fn?.__phrondStart) {
      const names = [];
      for (let current = test; current?.parent; current = current.parent) names.unshift(current.name);
      const pending = test.fn.__phrondStart(names.join(' '));
      test.fn.__phrondAttempt = pending;
      this.attempts.set(test, pending);
      // Observe setup errors through the test callback/completion path.
      pending.catch(() => {});
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
module.exports = PhrondEnvironment;
