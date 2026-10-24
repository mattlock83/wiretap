const path = require('node:path');
const { pathToFileURL } = require('node:url');

const ownerKey = Symbol.for('wiretap.jest.managedOwners');
function owners() { return globalThis[ownerKey] ??= new Map(); }
function projectKey(projectConfig) { return projectConfig.id || projectConfig.rootDir; }

async function callHook(specifier, globalConfig, projectConfig) {
  if (!specifier) return;
  const rootDir = projectConfig.rootDir || process.cwd();
  specifier = specifier.replaceAll('<rootDir>', rootDir);
  const absolute = path.isAbsolute(specifier) ? specifier : require.resolve(specifier.startsWith('.') ? path.resolve(rootDir, specifier) : specifier, { paths: [rootDir] });
  let hook;
  // Jest keeps its project transformer registered while awaiting this global
  // hook, so requiring the original hook retains its configured transforms.
  try { hook = require(absolute); }
  catch (error) {
    if (!['ERR_REQUIRE_ESM', 'ERR_REQUIRE_ASYNC_MODULE'].includes(error.code)) throw error;
    hook = await import(pathToFileURL(absolute).href);
  }
  hook = hook.default || hook;
  if (typeof hook !== 'function') throw new Error(`Jest hook ${specifier} must export a function`);
  return hook(globalConfig, projectConfig);
}

module.exports = { owners, projectKey, callHook };
