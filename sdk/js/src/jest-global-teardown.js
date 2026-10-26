const { owner, setOwner, projectKey, callHook } = require('./jest-hooks');
const { printCompletionSummary } = require('./completion-summary');

module.exports = async function wiretapGlobalTeardown(globalConfig, projectConfig) {
  const key = projectKey(projectConfig);
  const entry = owner();
  if (!entry || entry.key !== key) return;
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
      if (owner() === entry) setOwner(undefined);
    }
    if (failure) throw failure;
  })();
  return entry.closing;
};
