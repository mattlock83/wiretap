const http = require('node:http');
const { once } = require('node:events');
const { createJestSuite } = require('../../jest');
const calls = [], requests = [];
let server, url;
beforeAll(async () => {
  server = http.createServer(async (req, res) => {
    let raw = ''; for await (const chunk of req) raw += chunk;
    res.setHeader('content-type', 'application/json');
    if (!req.url.startsWith('/v1/')) { requests.push({ path: req.url, parent: req.headers.traceparent }); res.end('{}'); return; }
    const body = JSON.parse(raw || '{}'); calls.push({ path: req.url, body });
    if (req.url === '/v1/setupTest') res.end(JSON.stringify({ session_id: `s-${calls.length}`, name: body.name, sut_url: url, proxy_listen: url }));
    else if (req.url === '/v1/teardownTest') res.end(JSON.stringify({ capture: { schema_validation: { status: 'skipped' } } }));
    else res.end(JSON.stringify({ sut_url: url }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  url = `http://127.0.0.1:${server.address().port}`;
});
// Defer resolving the dynamically allocated address until the suite starts.
const config = { get baseUrl() { return url; }, schemaRequired: false };
const suite = createJestSuite(config);
jest.retryTimes(1, { retryImmediately: true });
let attempts = 0;
suite.test('normal callback', async () => { await fetch(suite.sutUrl + '/normal'); });
suite.test.concurrent.each(['alpha', 'beta'])('concurrent %s', async () => {
  await new Promise(resolve => setTimeout(resolve, 5));
  await fetch(suite.sutUrl + '/concurrent');
  expect(suite.runtime.activeSession).not.toBeNull();
});
suite.test.each([{ value: 'first' }, { value: 'second' }])('row $value', async () => { await fetch(`${suite.sutUrl}/table`); });
suite.test('retried callback', async () => {
  if (++attempts === 1) throw new Error('retry me');
  await fetch(suite.sutUrl + '/retry');
});
suite.test.skip('skipped callback', () => { throw new Error('must not run'); });
describe('normal fixture hooks', () => {
  beforeEach(async () => { await fetch(suite.sutUrl + '/fixture/setup'); });
  afterEach(done => { fetch(suite.sutUrl + '/fixture/cleanup').then(() => done(), done); });
  describe('nested', () => {
    beforeEach(() => { expect(suite.runtime.activeSession).not.toBeNull(); });
    afterEach(async () => { await fetch(suite.sutUrl + '/fixture/nested-cleanup'); });
    suite.test.each(['one', 'two'])('test %s', async () => { await fetch(suite.sutUrl + '/fixture/test'); });
  });
});
describe('retry fixture hooks', () => {
  let retryAttempts = 0;
  beforeEach(done => { fetch(suite.sutUrl + '/retry-fixture/setup').then(() => done(), done); });
  afterEach(async () => { await fetch(suite.sutUrl + '/retry-fixture/cleanup'); });
  suite.test('retried', async () => {
    if (++retryAttempts === 1) throw Error('retry fixture attempt');
    await fetch(suite.sutUrl + '/retry-fixture/test');
  });
});
describe('generator fixture hooks', () => {
  beforeEach(function* () { yield fetch(suite.sutUrl + '/generator/setup'); });
  afterEach(function* () { yield fetch(suite.sutUrl + '/generator/cleanup'); });
  suite.test('generator fixture test', async () => { await fetch(suite.sutUrl + '/generator/test'); });
});
afterAll(async () => {
  await new Promise(resolve => server.close(resolve));
  const setups = calls.filter(call => call.path === '/v1/setupTest').map(call => call.body);
  const teardowns = calls.filter(call => call.path === '/v1/teardownTest').map(call => call.body);
  expect(setups.map(call => call.name)).toEqual(expect.arrayContaining(['concurrent alpha', 'concurrent beta', 'row first', 'row second']));
  expect(setups.some(call => call.name === 'skipped callback')).toBe(false);
  expect(setups.filter(call => call.name === 'retried callback')).toHaveLength(2);
  expect(new Set(setups.map(call => call.trace_context.traceparent.split('-')[1])).size).toBe(setups.length);
  expect(teardowns).toHaveLength(setups.length);
  expect(teardowns.filter(call => call.status === 'fail')).toHaveLength(2);
  for (const { parent } of requests) expect(parent).toMatch(/^00-[a-f0-9]{32}-[a-f0-9]{16}-01$/);
  const fixtures = requests.filter(request => request.path.startsWith('/fixture/'));
  expect(fixtures.map(request => request.path)).toEqual([
    '/fixture/setup', '/fixture/test', '/fixture/nested-cleanup', '/fixture/cleanup',
    '/fixture/setup', '/fixture/test', '/fixture/nested-cleanup', '/fixture/cleanup'
  ]);
  for (let offset = 0; offset < fixtures.length; offset += 4) {
    const traceIds = fixtures.slice(offset, offset + 4).map(request => request.parent.split('-')[1]);
    expect(new Set(traceIds).size).toBe(1);
  }
  expect(fixtures[0].parent.split('-')[1]).not.toBe(fixtures[4].parent.split('-')[1]);
  const retrySetups = setups.filter(call => call.name === 'retry fixture hooks retried');
  expect(retrySetups).toHaveLength(2);
  for (let attempt = 0; attempt < 2; attempt++) {
    const traceId = retrySetups[attempt].trace_context.traceparent.split('-')[1];
    const paths = requests.filter(request => request.parent.split('-')[1] === traceId).map(request => request.path);
    expect(paths).toEqual(attempt === 0 ? ['/retry-fixture/setup', '/retry-fixture/cleanup']
      : ['/retry-fixture/setup', '/retry-fixture/test', '/retry-fixture/cleanup']);
  }
  const generator = requests.filter(request => request.path.startsWith('/generator/'));
  expect(generator.map(request => request.path)).toEqual(['/generator/setup', '/generator/test', '/generator/cleanup']);
  expect(new Set(generator.map(request => request.parent.split('-')[1])).size).toBe(1);
  expect(calls.some(call => call.path === '/v1/finishRun')).toBe(false);
});
