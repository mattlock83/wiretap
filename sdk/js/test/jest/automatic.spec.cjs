const http = require('node:http');
const { once } = require('node:events');
const { captureJest } = require('../../jest');

const calls = [], requests = [], sessions = [];
let controlServer, controlUrl, appUrl, appServer;

beforeAll(async () => {
  controlServer = http.createServer(async (req, res) => {
    let raw = '';
    for await (const chunk of req) raw += chunk;
    const body = JSON.parse(raw || '{}');
    const call = { path: req.url, body };
    calls.push(call);
    res.setHeader('content-type', 'application/json');
    if (req.url === '/v1/startRun') {
      appUrl = body.proxy.target_url;
      res.end(JSON.stringify({ sut_url: appUrl }));
    } else if (req.url === '/v1/setupTest') {
      call.sessionId = `automatic-${calls.length}`;
      res.end(JSON.stringify({ session_id: call.sessionId, name: body.name, sut_url: appUrl, proxy_listen: appUrl }));
    } else if (req.url === '/v1/teardownTest') {
      res.end(JSON.stringify({ capture: { schema_validation: { status: 'skipped' } } }));
    } else {
      res.statusCode = 404;
      res.end(JSON.stringify({ error: 'unexpected control request' }));
    }
  });
  controlServer.listen(0, '127.0.0.1');
  await once(controlServer, 'listening');
  controlUrl = `http://127.0.0.1:${controlServer.address().port}`;
});

captureJest({
  // The control URL is deliberately unavailable when captureJest registers.
  get baseUrl() { return controlUrl; },
  schemaRequired: false,
  app: function app(req, res) {
    appServer = this;
    requests.push({ path: req.url, parent: req.headers.traceparent });
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ path: req.url }));
  },
  onSession(session) { sessions.push(session); }
});

jest.retryTimes(1, { retryImmediately: true });

test('ordinary async callback', async () => {
  const response = await fetch(appUrl + '/async');
  expect(await response.json()).toEqual({ path: '/async' });
});

test('ordinary done callback', done => {
  http.get(appUrl + '/done', response => {
    response.resume();
    response.on('end', () => {
      try {
        expect(response.statusCode).toBe(200);
        done();
      } catch (error) { done(error); }
    });
    response.on('error', done);
  }).on('error', done);
});

test('ordinary generator callback', function* () {
  const response = yield fetch(appUrl + '/generator/test');
  expect(yield response.json()).toEqual({ path: '/generator/test' });
  try {
    yield Promise.reject(new Error('generator rejection'));
  } catch (error) {
    expect(error.message).toBe('generator rejection');
    yield fetch(appUrl + '/generator/recovery');
  }
});

test.each(['first', 'second'])('ordinary row %s', async value => {
  const response = await fetch(`${appUrl}/row/${value}`);
  expect(await response.json()).toEqual({ path: `/row/${value}` });
});

describe('duplicate registrations', () => {
  test('same name', async () => { await fetch(appUrl + '/duplicate/first'); });
  test('same name', async () => { await fetch(appUrl + '/duplicate/second'); });
});

const longTestName = 'long Unicode identity ' + '界'.repeat(100);
test(longTestName, async () => { await fetch(appUrl + '/long-identity'); });

describe('ordinary fixture hooks', () => {
  beforeEach(done => { fetch(appUrl + '/fixture/setup').then(() => done(), done); });
  afterEach(function* () { yield fetch(appUrl + '/fixture/cleanup'); });
  test('shares the test trace', async () => { await fetch(appUrl + '/fixture/test'); });
});

describe('ordinary retry', () => {
  let attempts = 0;
  beforeEach(async () => { await fetch(appUrl + '/retry/setup'); });
  afterEach(done => { fetch(appUrl + '/retry/cleanup').then(() => done(), done); });
  test('captures both outcomes', async () => {
    await fetch(appUrl + '/retry/test');
    if (++attempts === 1) throw new Error('retry this automatic attempt');
  });
});

describe('ordinary failed setup', () => {
  let attempts = 0;
  beforeEach(async () => {
    await fetch(appUrl + '/failed-setup/setup');
    if (++attempts === 1) throw new Error('retry this automatic fixture');
  });
  afterEach(async () => { await fetch(appUrl + '/failed-setup/cleanup'); });
  test('skips the body until setup succeeds', async () => { await fetch(appUrl + '/failed-setup/test'); });
});

test.skip('ordinary skipped callback', () => { throw new Error('must not run'); });
test.todo('ordinary future callback');

afterAll(async () => {
  // User suite cleanup can still reach the app; the environment owns shutdown.
  try {
    expect(appServer).toBeDefined();
    expect(appServer.listening).toBe(true);
    expect(appServer.address()).not.toBeNull();
    const starts = calls.filter(call => call.path === '/v1/startRun');
    const setups = calls.filter(call => call.path === '/v1/setupTest');
    const teardowns = calls.filter(call => call.path === '/v1/teardownTest');
    expect(starts).toHaveLength(1);
    expect(starts[0].body.proxy.target_url).toMatch(/^http:\/\/127\.0\.0\.1:\d+$/);
    expect(setups).toHaveLength(13);
    expect(setups.map(call => call.body.name)).toEqual(expect.arrayContaining([
      'ordinary async callback', 'ordinary done callback', 'ordinary generator callback',
      'ordinary row first', 'ordinary row second', 'ordinary fixture hooks shares the test trace'
    ]));
    expect(setups.some(call => /skipped|future/.test(call.body.name))).toBe(false);
    expect(setups.every(call => call.body.sut_base_url === appUrl && call.body.schema_required === false)).toBe(true);
    expect(teardowns).toHaveLength(setups.length);
    expect(teardowns.filter(call => call.body.status === 'fail')).toHaveLength(2);
    expect(sessions).toHaveLength(setups.length);
    expect(new Set(sessions.map(session => session.sessionId)).size).toBe(setups.length);
    expect(new Set(setups.map(call => call.body.trace_context.traceparent.split('-')[1])).size).toBe(setups.length);
    const duplicates = setups.filter(call => call.body.name === 'duplicate registrations same name');
    expect(duplicates).toHaveLength(2);
    expect(duplicates[0].body.test_identity.id).not.toBe(duplicates[1].body.test_identity.id);
    for (const duplicate of duplicates) expect(duplicate.body.test_identity.id).toContain('test/jest/automatic.spec.cjs');
    const duplicateIds = new Map(duplicates.map(call => {
      const traceId = call.body.trace_context.traceparent.split('-')[1];
      return [requests.find(request => request.parent.split('-')[1] === traceId).path, call.body.test_identity.id];
    }));
    expect(duplicateIds.get('/duplicate/first')).toMatch(/#1$/);
    expect(duplicateIds.get('/duplicate/second')).toMatch(/#2$/);
    const longIdentity = setups.find(call => call.body.name === longTestName).body.test_identity.id;
    expect(longIdentity).toMatch(/^jest:sha256:[a-f0-9]{64}$/);
    expect(setups.every(call => Buffer.byteLength(call.body.test_identity.id) <= 256)).toBe(true);
    for (const request of requests) {
      expect(request.parent).toMatch(/^00-[a-f0-9]{32}-[a-f0-9]{16}-01$/);
      expect(setups.some(call => call.body.trace_context.traceparent.split('-')[1] === request.parent.split('-')[1])).toBe(true);
    }
    function assertAttempt(name, expectedPaths, expectedOutcomes = ['pass']) {
      const attempts = setups.filter(call => call.body.name === name);
      expect(attempts).toHaveLength(expectedPaths.length);
      attempts.forEach((attempt, index) => {
        const traceId = attempt.body.trace_context.traceparent.split('-')[1];
        expect(requests.filter(request => request.parent.split('-')[1] === traceId).map(request => request.path)).toEqual(expectedPaths[index]);
        expect(teardowns.find(call => call.body.session_id === attempt.sessionId)?.body.status).toBe(expectedOutcomes[index]);
      });
    }
    assertAttempt('ordinary generator callback', [['/generator/test', '/generator/recovery']]);
    assertAttempt('ordinary fixture hooks shares the test trace', [['/fixture/setup', '/fixture/test', '/fixture/cleanup']]);
    assertAttempt('ordinary retry captures both outcomes', [
      ['/retry/setup', '/retry/test', '/retry/cleanup'],
      ['/retry/setup', '/retry/test', '/retry/cleanup']
    ], ['fail', 'pass']);
    for (const name of ['ordinary retry captures both outcomes', 'ordinary failed setup skips the body until setup succeeds']) {
      expect(new Set(setups.filter(call => call.body.name === name).map(call => call.body.test_identity.id)).size).toBe(1);
    }
    assertAttempt('ordinary failed setup skips the body until setup succeeds', [
      ['/failed-setup/setup', '/failed-setup/cleanup'],
      ['/failed-setup/setup', '/failed-setup/test', '/failed-setup/cleanup']
    ], ['fail', 'pass']);
    expect(calls.some(call => call.path === '/v1/finishRun')).toBe(false);
    const cleanupResponse = await fetch(appUrl + '/suite/cleanup');
    expect(cleanupResponse.status).toBe(200);
    expect(await cleanupResponse.json()).toEqual({ path: '/suite/cleanup' });
  } finally {
    await new Promise(resolve => {
      controlServer.close(resolve);
      controlServer.closeAllConnections();
    });
  }
});
