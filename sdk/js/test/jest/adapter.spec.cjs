const http = require('node:http');
const { once } = require('node:events');
const { createJestSuite } = require('../../jest');
const calls = [], requests = [];
let server, url;
beforeAll(async () => {
  server = http.createServer(async (req, res) => {
    let raw = ''; for await (const chunk of req) raw += chunk;
    res.setHeader('content-type', 'application/json');
    if (!req.url.startsWith('/v1/')) { requests.push(req.headers.traceparent); res.end('{}'); return; }
    const body = JSON.parse(raw || '{}'); calls.push({ path: req.url, body });
    if (req.url === '/v1/setupTest') res.end(JSON.stringify({ session_id: `s-${calls.length}`, name: body.name, sut_url: url, proxy_listen: url }));
    else if (req.url === '/v1/teardownTest') res.end(JSON.stringify({ capture: { schema_validation: { status: 'skipped' } } }));
    else res.end(JSON.stringify({ sut_url: url }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  url = `http://127.0.0.1:${server.address().port}`;
});
// Defer resolving the dynamically allocated address until the suite starts.
const config = { get baseUrl() { return url; }, generateSchema: true };
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
afterAll(async () => {
  await new Promise(resolve => server.close(resolve));
  const setups = calls.filter(call => call.path === '/v1/setupTest').map(call => call.body);
  const teardowns = calls.filter(call => call.path === '/v1/teardownTest').map(call => call.body);
  expect(setups.map(call => call.name)).toEqual(expect.arrayContaining(['concurrent alpha', 'concurrent beta', 'row first', 'row second']));
  expect(setups.some(call => call.name === 'skipped callback')).toBe(false);
  expect(setups.filter(call => call.name === 'retried callback')).toHaveLength(2);
  expect(new Set(setups.map(call => call.trace_context.traceparent.split('-')[1])).size).toBe(setups.length);
  expect(teardowns).toHaveLength(setups.length);
  expect(teardowns.filter(call => call.status === 'fail')).toHaveLength(1);
  for (const parent of requests) expect(parent).toMatch(/^00-[a-f0-9]{32}-[a-f0-9]{16}-01$/);
  expect(calls.some(call => call.path === '/v1/finishRun')).toBe(false);
});
