const { withWiretap, validateCaptureOptions } = require('./src/jest-config');

// Register once from setupFilesAfterEnv. Existing tests keep their native Jest
// callbacks; the environment owns each attempt and its final framework outcome.
function captureJest(config = {}, api = globalThis) {
  validateCaptureOptions(config);
  const { app } = config;
  if (app !== undefined && typeof app !== 'function') throw new TypeError('captureJest app must be a Node HTTP request handler, such as an Express app');
  require('./src/jest-app').registerCapture(config, api, app ? () => app : undefined);
}

module.exports = { captureJest, withWiretap };
