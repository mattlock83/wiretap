function withWiretap(config = {}, options = {}) {
  if (config.projects) throw new Error('Managed Wiretap supports a single Jest project; run projects separately');
  if (config.watch || config.watchAll) throw new Error('Managed Wiretap capture requires a single Jest run; disable watch mode');
  const environment = require.resolve('../jest-environment');
  if (config.testEnvironment && !['node', 'jest-environment-node', environment, '@wiretap/sdk/jest-environment'].includes(config.testEnvironment)) {
    throw new Error('withWiretap requires the Node test environment; custom environments must integrate Wiretap lifecycle explicitly');
  }
  if (config.maxWorkers !== undefined && config.maxWorkers !== 1 && !options.explicitSession) {
    throw new Error('Parallel Wiretap workers require explicitSession: true and propagated trace context; otherwise use maxWorkers: 1');
  }
  if (config.testRunner && !['jest-circus/runner', require.resolve('jest-circus/runner')].includes(config.testRunner)) {
    throw new Error('withWiretap requires the Jest Circus runner');
  }
  const { explicitSession = false, app, capture = {}, ...managed } = options;
  if (app !== undefined && (typeof app !== 'string' || !app.trim())) {
    throw new TypeError('withWiretap app must be a module path, such as <rootDir>/app.js');
  }
  if (!capture || typeof capture !== 'object' || Array.isArray(capture)) {
    throw new TypeError('withWiretap capture must be an options object');
  }
  if (capture.onSession !== undefined || capture.tracerProvider !== undefined || capture.testDefaults?.onSession !== undefined) {
    throw new TypeError('Jest configuration serializes capture options; register callbacks or tracerProvider through captureJest in setupFilesAfterEnv');
  }
  if ('signal' in managed) throw new Error('withWiretap manages cancellation through Jest; signal is only supported by startManagedRuntime');
  return {
    ...config,
    maxWorkers: config.maxWorkers ?? 1,
    testEnvironment: environment,
    globalSetup: require.resolve('./jest-global-setup'),
    globalTeardown: require.resolve('./jest-global-teardown'),
    ...(app ? { setupFilesAfterEnv: [require.resolve('./jest-app-setup'), ...(config.setupFilesAfterEnv || [])] } : {}),
    testEnvironmentOptions: {
      ...config.testEnvironmentOptions,
      wiretap: {
        ...config.testEnvironmentOptions?.wiretap,
        ...capture,
        autoCapture: true,
        explicitSession,
        app,
        managed,
        globalSetup: config.globalSetup,
        globalTeardown: config.globalTeardown
      }
    }
  };
}
module.exports = { withWiretap };
