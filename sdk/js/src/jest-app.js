// Register the host before user hooks, while loading the handler only after
// setup files and test modules have installed their Jest mocks.
function registerCapture(config = {}, api = globalThis, loadApp) {
  if (!globalThis.__wiretapSDK) throw new Error('Use withWiretap in your Jest configuration before calling captureJest');
  if (globalThis.__wiretapCaptureConfig) throw new Error('captureJest must be registered once per test file; omit it when withWiretap already has an app option');
  let server, address;
  const descriptors = Object.getOwnPropertyDescriptors(config);
  delete descriptors.app;
  if (loadApp) descriptors.sutBaseUrl = { enumerable: true, get: () => address || config.sutBaseUrl };
  globalThis.__wiretapCaptureConfig = Object.defineProperties({}, descriptors);
  if (!loadApp) return;
  const http = require('node:http');
  api.beforeAll(async () => {
    const app = await loadApp();
    if (typeof app !== 'function') throw new TypeError('Wiretap app must export a Node HTTP request handler, such as an Express app');
    server = http.createServer(app);
    await new Promise((resolve, reject) => {
      server.once('error', reject);
      server.listen(0, '127.0.0.1', resolve);
    });
    address = `http://127.0.0.1:${server.address().port}`;
  });
  // Keep the app alive through all user afterAll hooks.
  globalThis.__wiretapCleanupApp = async () => {
    if (!server?.listening) return;
    await new Promise((resolve, reject) => {
      server.close(error => error ? reject(error) : resolve());
      server.closeAllConnections();
    });
  };
}

module.exports = { registerCapture };
