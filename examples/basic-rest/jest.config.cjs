module.exports = {
  rootDir: '../..',
  globalTeardown: './examples/basic-rest/jest-teardown.cjs',
  testEnvironment: './sdk/js/jest-environment.js',
  testMatch: ['**/examples/basic-rest/otel.jest.cjs'],
  transform: {},
  maxWorkers: 1
};
