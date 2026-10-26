const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const fs = require('node:fs/promises');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { withWiretap, captureJest } = require('../jest');

test('Jest app option preserves setup ordering without passing the handler to the runtime', () => {
  const setupFilesAfterEnv = ['<rootDir>/first.cjs', '<rootDir>/second.cjs'];
  const original = { setupFilesAfterEnv };
  const configured = withWiretap(original, { app: '<rootDir>/app.cjs', capture: { schemaRequired: false } });
  assert.deepEqual(configured.setupFilesAfterEnv.slice(1), setupFilesAfterEnv);
  assert.equal(configured.setupFilesAfterEnv[0], require.resolve('../src/jest-app-setup'));
  assert.equal(configured.testEnvironmentOptions.wiretap.app, '<rootDir>/app.cjs');
  assert.equal(Object.hasOwn(configured.testEnvironmentOptions.wiretap.managed, 'app'), false);
  assert.equal(Object.hasOwn(configured.testEnvironmentOptions.wiretap.managed, 'capture'), false);
  assert.equal(configured.testEnvironmentOptions.wiretap.schemaRequired, false);
  assert.equal(original.setupFilesAfterEnv, setupFilesAfterEnv);
  assert.equal(withWiretap(original).setupFilesAfterEnv, setupFilesAfterEnv);
  for (const app of [null, '', '  ', {}, () => {}]) assert.throws(() => withWiretap({}, { app }), /module path/);
  for (const capture of [null, [], 'invalid']) assert.throws(() => withWiretap({}, { capture }), /options object/);
  for (const capture of [{ onSession() {} }, { tracerProvider: {} }]) {
    assert.throws(() => withWiretap({}, { capture }), /captureJest in setupFilesAfterEnv/);
  }
  const outputs = { coverage: ['json', 'html'], arazzo: true };
  assert.equal(withWiretap({}, { outputs }).testEnvironmentOptions.wiretap.managed.outputs, outputs);
  for (const [capture, error] of [[{ outputs }, /withWiretap options/], [{ testDefaults: {} }, /settings directly/]]) {
    assert.throws(() => withWiretap({}, { capture }), error);
    assert.throws(() => captureJest(capture), error);
  }
});

for (const format of ['commonjs', 'esm']) test(`Jest app option loads ${format} mocked handlers inside each VM before user suite hooks`, async t => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-jest-app-'));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  let target, activeSession;
  const calls = [], traffic = [];
  const server = http.createServer(async (req, res) => {
    if (req.url.startsWith('/api/')) {
      traffic.push({ path: req.url, parent: req.headers.traceparent, session: activeSession });
      const upstream = http.request(target + req.url.slice(4), { method: req.method, headers: req.headers }, response => {
        res.writeHead(response.statusCode, response.headers);
        response.pipe(res);
      });
      upstream.on('error', error => { res.writeHead(502); res.end(error.message); });
      req.pipe(upstream);
      return;
    }
    let raw = '';
    for await (const chunk of req) raw += chunk;
    const body = JSON.parse(raw || '{}');
    calls.push({ path: req.url, body });
    res.setHeader('content-type', 'application/json');
    if (req.url === '/v1/startRun') {
      target = body.proxy.target_url;
      res.end(JSON.stringify({ sut_url: `${baseUrl}/api` }));
    } else if (req.url === '/v1/setupTest') {
      activeSession = `app-${calls.length}`;
      res.end(JSON.stringify({ session_id: activeSession, name: body.name, sut_url: `${baseUrl}/api` }));
    } else if (req.url === '/v1/teardownTest') {
      res.end(JSON.stringify({ capture: { schema_validation: { status: 'skipped' } } }));
    } else {
      res.statusCode = 404;
      res.end(JSON.stringify({ error: 'unexpected request' }));
    }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  const baseUrl = `http://127.0.0.1:${server.address().port}`;
  const write = (name, content) => fs.writeFile(path.join(directory, name), content);
  await Promise.all([
    write('dependency.cjs', "module.exports = { value: 'unmocked' };\n"),
    write(`app.${format === 'esm' ? 'mjs' : 'cjs'}`, `
      ${format === 'esm' ? "import dependency from './dependency.cjs'; const { value } = dependency;" : "if (typeof jest === 'undefined') throw new Error('app escaped the Jest VM'); const { value } = require('./dependency.cjs');"}
      let count = 0;
      ${format === 'esm' ? 'export default function app' : 'module.exports = { default'}(req, res) {
        res.setHeader('content-type', 'application/json');
        res.end(JSON.stringify({ value, count: ++count }));
      }${format === 'esm' ? '' : '}'};
    `),
    write('first.cjs', `
      globalThis.setupOrder = ['first'];
      jest.mock('./dependency.cjs', () => ({ value: 'user setup mock' }));
      beforeAll(async () => {
        // The SDK's app hook precedes existing beforeAll hooks.
        const response = await fetch(globalThis.__wiretapCaptureConfig.sutBaseUrl + '/before-all');
        expect(await response.json()).toEqual({ value: 'user setup mock', count: 1 });
      });
    `),
    write('second.cjs', "globalThis.setupOrder.push('second');\n"),
    ...['first', 'second'].map(name => write(`${name}.spec.cjs`, `
      test('captures an ordinary existing test', async () => {
        expect(setupOrder).toEqual(['first', 'second']);
        const response = await fetch(process.env.WIRETAP_SUT_URL + '/resource');
        expect(await response.json()).toEqual({ value: 'user setup mock', count: 2 });
      });
      afterAll(async () => {
        const response = await fetch(globalThis.__wiretapCaptureConfig.sutBaseUrl + '/after-all');
        expect(await response.json()).toEqual({ value: 'user setup mock', count: 3 });
      });
    `)),
    write('jest.config.cjs', `
      const { withWiretap } = require(${JSON.stringify(require.resolve('../src/jest-config'))});
      const config = withWiretap({
        rootDir: __dirname, transform: {}, testMatch: ['**/*.spec.cjs'],
        setupFilesAfterEnv: ['<rootDir>/first.cjs', '<rootDir>/second.cjs'],
        testEnvironmentOptions: { wiretap: { schemaRequired: false } }
      }, { app: '<rootDir>/app.${format === 'esm' ? 'mjs' : 'cjs'}' });
      // This regression supplies a control server; managed process ownership is
      // independently exercised by managed-runtime.test.js and the real demo.
      delete config.globalSetup;
      delete config.globalTeardown;
      module.exports = config;
    `)
  ]);
  const child = spawn(process.execPath, [...(format === 'esm' ? ['--experimental-vm-modules'] : []), require.resolve('jest/bin/jest'), '--config', path.join(directory, 'jest.config.cjs'), '--runInBand'], {
    env: { ...process.env, WIRETAP_BASE_URL: baseUrl, WIRETAP_SUT_URL: `${baseUrl}/api` },
    stdio: ['ignore', 'pipe', 'pipe']
  });
  let output = '';
  child.stdout.on('data', chunk => { output += chunk; });
  child.stderr.on('data', chunk => { output += chunk; });
  const timeout = setTimeout(() => child.kill('SIGKILL'), 30000);
  const code = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('close', resolve);
  }).finally(() => clearTimeout(timeout));
  assert.equal(code, 0, output);
  assert.equal(calls.filter(call => call.path === '/v1/startRun').length, 2);
  const setups = calls.filter(call => call.path === '/v1/setupTest');
  assert.equal(setups.length, 2);
  assert.equal(new Set(setups.map(call => call.body.test_identity.id)).size, 2);
  assert.deepEqual(calls.filter(call => call.path === '/v1/teardownTest').map(call => call.body.status), ['pass', 'pass']);
  assert.equal(traffic.length, 2);
  assert.ok(traffic.every(request => /^00-[a-f0-9]{32}-[a-f0-9]{16}-01$/.test(request.parent)));
});
