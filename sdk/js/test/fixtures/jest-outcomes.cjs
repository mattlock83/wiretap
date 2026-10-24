const { createJestSuite } = require('../../jest');
let server;
beforeAll(async () => { server = await require('./outcomes-server.cjs')(); });
const suite = createJestSuite({ get baseUrl() { return server.url; }, schemaRequired: false });
suite.test('pass', () => {});
suite.test('timeout', async () => { await new Promise(resolve => setTimeout(resolve, 180)); }, 60);
suite.test('never settles', () => new Promise(() => {}), 60);
suite.test('assertion count', () => { expect.assertions(1); });
suite.test('validation', () => {});
describe('hooks', () => {
  beforeEach(async () => { await fetch(suite.sutUrl + '/hook/setup'); });
  afterEach(async () => {
    await fetch(suite.sutUrl + '/hook/cleanup');
    throw Error('cleanup failed');
  });
  suite.test('hook failure', () => {});
});
describe('failed setup', () => {
  beforeEach(async () => {
    await fetch(suite.sutUrl + '/failed-setup/setup');
    throw Error('setup failed');
  });
  afterEach(done => { fetch(suite.sutUrl + '/failed-setup/cleanup').then(() => done(), done); });
  suite.test('body never runs', async () => { await fetch(suite.sutUrl + '/must-not-run'); });
});
afterAll(async () => {
  await new Promise(resolve => setTimeout(resolve, 200));
  await server.finish({ open: suite.runtime.openSessions.size });
});
