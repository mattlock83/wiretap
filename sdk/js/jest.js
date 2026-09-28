const { startCapture } = globalThis.__phrondSDK || require('./src');

// Jest workers share an externally owned Phrond runtime. Each callback gets
// its own async context, including test.concurrent and each generated case.
function createJestSuite(config = {}, api = globalThis) {
  if (!globalThis.__phrondSDK) throw new Error("Set Jest testEnvironment to @phrond/sdk/jest-environment so native clients share the test trace context");
  let runtime;
  let starting;
  const getRuntime = async () => {
    if (!starting) starting = startCapture({ ...config, manageRuntime: false }).then(value => (runtime = value));
    return starting;
  };
  const wrap = (register, concurrent = false) => {
    const wrapped = (name, fn, timeout) => {
      if (typeof fn !== 'function') return register(name, fn, timeout);
      const callback = async (...args) => {
        // Capture the handle before an await: a retry may replace the function's
        // current handle while an earlier timed-out callback is still running.
        const attempt = await callback.__phrondAttempt;
        if (!attempt) throw new Error("Phrond Jest environment did not start this attempt");
        return attempt.run(session => fn(...args, session));
      };
      callback.__phrondStart = async (fullName) => {
        const active = await getRuntime();
        return active._createTestAttempt(fullName, {
          ...(config.testDefaults || {}),
          explicitSession: concurrent || (config.testDefaults?.explicitSession ?? config.explicitSession ?? false)
        });
      };
      return register(name, callback, timeout);
    };
    // jest-each resolves every name at registration, including concurrent rows.
    wrapped.each = require('jest-each').bind(wrapped, false);
    return wrapped;
  };
  const test = wrap(api.test);
  test.only = wrap(api.test.only);
  test.skip = api.test.skip.bind(api.test);
  test.skip.each = api.test.skip.each;
  test.todo = api.test.todo.bind(api.test);
  test.concurrent = wrap(api.test.concurrent, true);
  test.concurrent.only = wrap(api.test.concurrent.only, true);
  test.concurrent.skip = api.test.concurrent.skip.bind(api.test.concurrent);
  test.concurrent.skip.each = api.test.concurrent.skip.each;
  api.beforeAll(getRuntime);
  return {
    test,
    get runtime() { if (!runtime) throw new Error('Phrond capture runtime is not started yet'); return runtime; },
    get sutUrl() { return this.runtime.sutUrl; },
    get http() { return this.runtime.http; },
    dependencyUrl(assetId) { return this.runtime.dependencyUrl(assetId); }
  };
}
module.exports = { createJestSuite };
