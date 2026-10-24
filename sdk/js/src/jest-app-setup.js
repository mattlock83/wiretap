const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { registerCapture } = require('./jest-app');

const { module: appModule, rootDir } = globalThis.__wiretapJestApp || {};
if (!appModule) throw new Error('Wiretap app setup requires withWiretap({ ... }, { app: "<rootDir>/app.js" })');
const modulePath = appModule.startsWith('<rootDir>')
  ? path.join(rootDir, appModule.slice('<rootDir>'.length))
  : appModule.startsWith('.') ? path.resolve(rootDir, appModule) : appModule;
registerCapture({}, globalThis, async () => {
  // This require belongs to Jest's VM: transforms, module isolation and mocks
  // work just as they do when a test imports the application itself.
  let loaded;
  try { loaded = require(modulePath); }
  catch (error) {
    if (error.code !== 'ERR_REQUIRE_ESM') throw error;
    // Native ESM uses the same VM loader, including Jest's configured ESM mocks.
    loaded = await import(pathToFileURL(require.resolve(modulePath)).href);
  }
  return typeof loaded === 'function' ? loaded : loaded?.default;
});
