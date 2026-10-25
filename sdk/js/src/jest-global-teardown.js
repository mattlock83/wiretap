const { owners, projectKey, callHook } = require('./jest-hooks');
const { printCompletionSummary } = require('./completion-summary');

module.exports = async function wiretapGlobalTeardown(globalConfig, projectConfig) {
  const key = projectKey(projectConfig);
  const entry = owners().get(key);
  if (!entry) return;
  if (!entry.closing) entry.closing = (async () => {
    let failure;
    try {
      const runtime = entry.runtime || await entry.starting;
      printCompletionSummary(await runtime?.close());
    } catch (error) { failure = error; }
    try { await callHook(projectConfig.testEnvironmentOptions.wiretap.globalTeardown, globalConfig, projectConfig); }
    catch (error) {
      if (failure) failure.cleanupError = error;
      else failure = error;
    } finally {
      for (const [name, value] of Object.entries(entry.previousEnv)) {
        if (value === undefined) delete process.env[name];
        else process.env[name] = value;
      }
      process.removeListener('SIGINT', entry.onInterrupt);
      process.removeListener('SIGTERM', entry.onTerminate);
      owners().delete(key);
    }
    if (failure) throw failure;
  })();
  return entry.closing;
};
