const { captureJest } = require('../../jest');
let server;
beforeAll(async () => { server = await require('./outcomes-server.cjs')(); });
captureJest({ get baseUrl() { return server.url; }, schemaRequired: false });
test('pass', () => {});
test('timeout', async () => { await new Promise(resolve => setTimeout(resolve, 180)); }, 60);
test('never settles', () => new Promise(() => {}), 60);
test('falsy failure', () => { throw 0; });
test('assertion count', () => { expect.assertions(1); });
test('validation', () => {});
test('test and capture cleanup fail', () => { throw Error('original test failure retained'); });
describe('hooks', () => {
  beforeEach(async () => { await fetch(server.url + '/hook/setup'); });
  afterEach(async () => {
    await fetch(server.url + '/hook/cleanup');
    throw Error('cleanup failed');
  });
  test('hook failure', () => {});
});
describe('failed setup', () => {
  beforeEach(async () => {
    await fetch(server.url + '/failed-setup/setup');
    throw Error('setup failed');
  });
  afterEach(done => { fetch(server.url + '/failed-setup/cleanup').then(() => done(), done); });
  test('body never runs', async () => { await fetch(server.url + '/must-not-run'); });
});
afterAll(async () => {
  await new Promise(resolve => setTimeout(resolve, 200));
  await server.finish();
});
