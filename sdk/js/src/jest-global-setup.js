const { startManagedRuntime } = require('./managed-runtime');
const { owners, projectKey, callHook } = require('./jest-hooks');

module.exports = async function wiretapGlobalSetup(globalConfig, projectConfig) {
  const options = projectConfig.testEnvironmentOptions.wiretap;
  if (globalConfig.projects?.length > 1) throw new Error('Managed Wiretap supports a single Jest project; run projects separately');
  if (globalConfig.watch || globalConfig.watchAll) throw new Error('Managed Wiretap capture requires a single Jest run; disable watch mode');
  if (globalConfig.maxWorkers > 1 && !options.explicitSession) {
    throw new Error('Parallel Wiretap workers require explicitSession: true and propagated trace context; otherwise use --runInBand');
  }
  if (owners().size) throw new Error('Managed Wiretap supports one Jest project per process');
  await callHook(options.globalSetup, globalConfig, projectConfig);
  const previousEnv = { WIRETAP_BASE_URL: process.env.WIRETAP_BASE_URL, WIRETAP_SUT_URL: process.env.WIRETAP_SUT_URL };
  const abort = new AbortController();
  const entry = { previousEnv, abort };
  owners().set(projectKey(projectConfig), entry);
  const stopForSignal = signal => {
    if (entry.interrupted) return;
    entry.interrupted = true;
    abort.abort(new Error(`Wiretap interrupted by ${signal}`));
    const teardown = require('./jest-global-teardown');
    const exitCode = signal === 'SIGINT' ? 130 : 143;
    // Runtime requests and process shutdown have their own deadlines. Also
    // bound caller teardown hooks during cancellation so an interrupted runner
    // cannot stay alive forever after the owned process has been stopped.
    const deadline = setTimeout(() => process.exit(exitCode),
      (options.managed.timeoutMs ?? 30000) + 2 * (options.managed.shutdownTimeoutMs ?? 5000) + 1000);
    deadline.unref();
    // Complete owned cleanup before preserving the conventional interrupted exit.
    void teardown(globalConfig, projectConfig).catch(error => console.error(error)).finally(() => {
      clearTimeout(deadline);
      process.exit(exitCode);
    });
  };
  entry.onInterrupt = () => stopForSignal('SIGINT');
  entry.onTerminate = () => stopForSignal('SIGTERM');
  process.once('SIGINT', entry.onInterrupt);
  process.once('SIGTERM', entry.onTerminate);
  try {
    entry.starting = startManagedRuntime({ ...options.managed, outputs: options.managed.outputs ?? options.outputs, cwd: projectConfig.rootDir, signal: abort.signal });
    entry.runtime = await entry.starting;
    process.env.WIRETAP_BASE_URL = entry.runtime.baseUrl;
    process.env.WIRETAP_SUT_URL = entry.runtime.sutUrl;
  } catch (error) {
    try { await require('./jest-global-teardown')(globalConfig, projectConfig); }
    catch (cleanupError) { if (cleanupError !== error) error.cleanupError = cleanupError; }
    throw error;
  }
};
