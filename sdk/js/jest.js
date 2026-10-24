const { startCapture } = globalThis.__wiretapSDK || require('./src');

// Jest workers share an externally owned Wiretap runtime. Each callback gets
// its own async context, including test.concurrent and each generated case.
function createJestSuite(config = {}, api = globalThis) {
  if (!globalThis.__wiretapSDK) throw new Error("Set Jest testEnvironment to @wiretap/sdk/jest-environment so native clients share the test trace context");
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
        const attempt = await callback.__wiretapAttempt;
        if (!attempt) throw new Error("Wiretap Jest environment did not start this attempt");
        return attempt.run(session => fn(...args, session));
      };
      callback.__wiretapStart = async (fullName) => {
        const active = await getRuntime();
        return active.createTestAttempt(fullName, {
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
    get runtime() { if (!runtime) throw new Error('Wiretap capture runtime is not started yet'); return runtime; },
    get sutUrl() { return this.runtime.sutUrl; },
    dependencyUrl(assetId) { return this.runtime.dependencyUrl(assetId); }
  };
}
// Register once from setupFilesAfterEnv. Existing tests keep their native Jest
// callbacks; the environment owns each attempt and its final framework outcome.
function captureJest(config = {}, api = globalThis) {
  const { app } = config;
  if (!globalThis.__wiretapSDK) throw new Error('Use withWiretap in your Jest configuration before calling captureJest');
  if (globalThis.__wiretapCaptureConfig) throw new Error('captureJest must be registered once per test file');
  if (app !== undefined && typeof app !== 'function') throw new TypeError('captureJest app must be a Node HTTP request handler, such as an Express app');
  let server, address;
  const descriptors = Object.getOwnPropertyDescriptors(config);
  delete descriptors.app;
  if (app) descriptors.sutBaseUrl = { enumerable: true, get: () => address || config.sutBaseUrl };
  globalThis.__wiretapCaptureConfig = Object.defineProperties({}, descriptors);
  if (app) {
    const http = require('node:http');
    api.beforeAll(async () => {
      server = http.createServer(app);
      await new Promise((resolve, reject) => {
        server.once('error', reject);
        server.listen(0, '127.0.0.1', resolve);
      });
      address = `http://127.0.0.1:${server.address().port}`;
    });
    // The environment closes the owned host after every user afterAll hook.
    // A hook registered here would run before cleanup hooks in test files.
    globalThis.__wiretapCleanupApp = async () => {
      if (!server?.listening) return;
      await new Promise((resolve, reject) => {
        server.close(error => error ? reject(error) : resolve());
        server.closeAllConnections();
      });
    };
  }
}

module.exports = { createJestSuite, captureJest, withWiretap: require('./src/jest-config').withWiretap };
