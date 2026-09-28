const { createJestSuite } = require('../../jest');
let server;
beforeAll(async () => { server = await require('./outcomes-server.cjs')(); });
const suite = createJestSuite({ get baseUrl() { return server.url; }, generateSchema: true });
suite.test('pass', () => {});
suite.test('timeout', async () => { await new Promise(resolve => setTimeout(resolve, 180)); }, 60);
suite.test('never settles', () => new Promise(() => {}), 60);
suite.test('assertion count', () => { expect.assertions(1); });
suite.test('validation', () => {});
describe('hooks', () => {
  afterEach(() => { throw Error('cleanup failed'); });
  suite.test('hook failure', () => {});
});
afterAll(async () => {
  await new Promise(resolve => setTimeout(resolve, 200));
  await server.finish({ open: suite.runtime.openSessions.size });
});
