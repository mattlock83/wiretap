const assert = require('node:assert/strict');
const url = process.env.WIRETAP_SUT_URL;
assert.ok(url, 'the proxy URL must be available when test modules load');
globalThis.__wiretapMochaCleanup = [];

beforeEach(async function () { await fetch(url + '/root/setup'); });
afterEach(async function () {
  await fetch(url + '/root/cleanup');
  if (this.currentTest.title === 'root cleanup failure') throw new Error('root cleanup failed');
});
after(async () => {
  globalThis.__wiretapMochaCleanup.push('user afterAll');
  await fetch(url + '/suite/cleanup');
});

it('pass', async () => { assert.equal((await fetch(url + '/test/pass')).status, 200); });
it('callback', done => { fetch(url + '/test/callback').then(() => done(), done); });
it('assertion failure', () => { assert.fail('expected assertion failure'); });
it('timeout', async function () { this.timeout(30); await new Promise(resolve => setTimeout(resolve, 90)); });
it('never settles', function () { this.timeout(30); return new Promise(() => {}); });
it('skip at runtime', function () { this.skip(); });
it.skip('static skip', () => { throw new Error('must not run'); });
it('retry', function () { this.retries(1); if (this.test.currentRetry() === 0) throw new Error('retry once'); });
it('duplicate', () => {});
it('duplicate', () => {});

describe('nested fixtures', () => {
  beforeEach(done => { fetch(url + '/nested/setup').then(() => done(), done); });
  afterEach(async () => { await fetch(url + '/nested/cleanup'); throw new Error('nested cleanup failed'); });
  it('cleanup failure', async () => { await fetch(url + '/test/nested'); });
});
describe('failed setup', () => {
  beforeEach(async () => { await fetch(url + '/failed/setup'); throw new Error('setup failed'); });
  afterEach(async () => { await fetch(url + '/failed/cleanup'); });
  it('body never runs', async () => { await fetch(url + '/must-not-run'); });
});
describe('root failure last', () => {
  it('root cleanup failure', async () => { await fetch(url + '/test/root'); });
});
