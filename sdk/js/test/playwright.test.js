const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { promisify } = require('node:util');
const execute = promisify(require('node:child_process').execFile);
const { spawn } = require('node:child_process');
const { withWiretap } = require('../src/playwright-config');

test('Playwright configuration preserves hooks, reporters, projects and client options', () => {
  const config = {
    use: { baseURL: 'http://localhost:3000', extraHTTPHeaders: { authorization: 'dummy' }, ignoreHTTPSErrors: true },
    projects: [{ name: 'api', use: { baseURL: 'http://localhost:3001', locale: 'en-AU' } }],
    globalSetup: './setup.js', globalTeardown: './teardown.js', reporter: [['json', { outputFile: 'result.json' }]],
  };
  const wrapped = withWiretap(config);
  assert.equal(wrapped.workers, 1);
  assert.equal(wrapped.globalSetup[0], './setup.js');
  assert.equal(wrapped.globalTeardown, './teardown.js');
  assert.deepEqual(wrapped.reporter[0], config.reporter[0]);
  assert.equal(wrapped.use.wiretapOptions.sutBaseUrl, config.use.baseURL);
  assert.equal(wrapped.use.ignoreHTTPSErrors, true);
  assert.equal(wrapped.projects[0].use.wiretapOptions.sutBaseUrl, 'http://localhost:3001');
  assert.deepEqual(wrapped.projects[0].use.wiretapOptions.headers, { authorization: 'dummy' });
  assert.equal(config.use.baseURL, 'http://localhost:3000');
  assert.throws(() => withWiretap({}, { capture: { instrumentation: true } }), /fixtures/);
  assert.throws(() => withWiretap({ workers: 2 }), /explicitSession/);
  assert.throws(() => withWiretap({ fullyParallel: true }), /explicitSession/);
  assert.equal(withWiretap({ workers: 2 }, { explicitSession: true }).workers, 2);
  const provider = {}; provider.circular = provider;
  assert.throws(() => withWiretap({}, { capture: { tracerProvider: provider } }), /serializable/);
  assert.throws(() => withWiretap({}, { capture: { onSession() {} } }), /serializable/);
  assert.throws(() => withWiretap({}, { capture: { testDefaults: { onSession() {} } } }), /serializable/);
});

test('Playwright rejects interactive UI flags before starting capture', () => {
  const argv = process.argv;
  try {
    for (const flag of ['--ui', '--ui-host', '--ui-host=127.0.0.1', '--ui-port', '--ui-port=8080']) {
      process.argv = [argv[0], 'playwright', 'test', flag];
      assert.throws(() => withWiretap({}), /single Playwright run/);
    }
  } finally { process.argv = argv; }
});

test('Playwright uses final runner outcomes after hooks, timeouts, retries and all custom fixtures', { timeout: 45000 }, async t => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-playwright-'));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const configPath = path.join(directory, 'playwright.config.cjs');
  const config = withWiretap({
    testDir: path.join(__dirname, 'fixtures', 'playwright'), testMatch: '**/outcomes.spec.cjs',
    outputDir: path.join(directory, 'test-results'),
    globalSetup: path.join(__dirname, 'fixtures', 'playwright', 'setup.cjs'),
    use: { baseURL: 'http://original.example:3000', extraHTTPHeaders: { 'x-existing': 'kept' } },
    reporter: [['json', { outputFile: path.join(directory, 'report.json') }]],
  }, { binary: 'wiretap-test-runtime', outputDir: directory, capture: { tags: ['suite'], testDefaults: { drainTimeoutMs: 123 } } });
  await fs.writeFile(configPath, `module.exports = ${JSON.stringify(config)};\n`);
  const env = { ...process.env, WIRETAP_PLAYWRIGHT_RESULT: directory };
  delete env.NODE_TEST_CONTEXT;
  let result;
  try { result = await execute(process.execPath, [require.resolve('@playwright/test/cli'), 'test', '--config', configPath], { env, timeout: 40000 }); }
  catch (error) { result = error; }
  assert.equal(result.code, 1, result.stdout + result.stderr);
  const calls = JSON.parse(await fs.readFile(path.join(directory, 'calls.json'), 'utf8'));
  const setups = calls.filter(call => call.path === '/v1/setupTest');
  const ends = calls.filter(call => call.path === '/v1/teardownTest');
  assert.equal(setups.length, 10, result.stdout + result.stderr);
  assert.equal(ends.length, setups.length);
  const statusFor = (name, ordinal = 1) => {
    const setup = setups.find(call => call.body.name.endsWith(name) && call.body.test_identity.attempt_ordinal === ordinal);
    assert.ok(setup, `missing setup for ${name} attempt ${ordinal}`);
    return ends.find(call => call.body.session_id === `${setup.body.test_identity.id}:${ordinal}`).body.status;
  };
  assert.equal(statusFor('pass'), 'pass');
  for (const name of ['assertion failure', 'timeout', 'before hook failure', 'after hook failure', 'late fixture failure', 'expected failure']) assert.equal(statusFor(name), 'fail', name);
  assert.equal(statusFor('runtime skip'), 'skip');
  assert.equal(statusFor('retrying test', 1), 'fail');
  assert.equal(statusFor('retrying test', 2), 'pass');
  assert.ok(setups.every(call => call.body.sut_base_url === 'http://original.example:3000'));
  assert.ok(setups.every(call => call.body.explicit_session === false));
  assert.ok(setups.every(call => call.body.tags?.[0] === 'suite'));
  assert.ok(ends.every(call => call.body.drain_timeout_ms === 123));
  assert.equal(calls.some(call => call.path === '/api/must-not-run'), false);
  const traces = new Set(setups.map(call => call.body.trace_context.traceparent));
  for (const call of calls.filter(call => call.path.startsWith('/api/'))) assert.ok(traces.has(call.headers.traceparent), call.path);
  assert.equal(calls.at(-1).path, '/v1/finishRun');
  assert.equal(JSON.parse(await fs.readFile(path.join(directory, 'last-run.json'), 'utf8')).status, 'complete');
});

async function runFixture(t, file, settings = {}, wiretapSettings = {}) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-playwright-'));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const configPath = path.join(directory, 'playwright.config.cjs');
  const config = withWiretap({
    testDir: path.join(__dirname, 'fixtures', 'playwright'), testMatch: file,
    outputDir: path.join(directory, 'test-results'),
    globalSetup: path.join(__dirname, 'fixtures', 'playwright', 'setup.cjs'),
    use: { baseURL: 'http://original.example:3000', extraHTTPHeaders: { 'x-existing': 'kept' } },
    reporter: [['json', { outputFile: path.join(directory, 'report.json') }]], ...settings,
  }, { binary: 'wiretap-test-runtime', outputDir: directory, ...wiretapSettings });
  await fs.writeFile(configPath, `module.exports = ${JSON.stringify(config)};\n`);
  const env = { ...process.env, WIRETAP_PLAYWRIGHT_RESULT: directory };
  delete env.NODE_TEST_CONTEXT;
  const child = spawn(process.execPath, [require.resolve('@playwright/test/cli'), 'test', '--config', configPath], { env });
  t.after(() => { if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL'); });
  let output = '';
  child.stdout.on('data', data => { output += data; }); child.stderr.on('data', data => { output += data; });
  const complete = new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', (code, signal) => resolve({ code, signal, output })); });
  return { directory, child, complete, calls: async () => JSON.parse(await fs.readFile(path.join(directory, 'calls.json'), 'utf8')) };
}

test('Playwright parallel API requests retain their own trace and baseURL path semantics', { timeout: 20000 }, async t => {
  const run = await runFixture(t, 'parallel.spec.cjs', { workers: 2, fullyParallel: true }, { explicitSession: true });
  const result = await run.complete;
  assert.equal(result.code, 0, result.output);
  const calls = await run.calls();
  const setups = calls.filter(call => call.path === '/v1/setupTest');
  assert.equal(setups.length, 2);
  assert.ok(setups.every(call => call.body.explicit_session));
  assert.ok(setups.every(call => call.body.sut_base_url === 'http://original.example:3000'));
  for (const name of ['first', 'second']) {
    const setup = setups.find(call => call.body.name.endsWith(name));
    const traffic = calls.filter(call => call.headers['x-case'] === name);
    assert.deepEqual(traffic.map(call => call.path), ['/api/nested', '/api/nested', '/api/nested', '/root']);
    assert.ok(traffic.every(call => call.headers.traceparent === setup.body.trace_context.traceparent));
  }
});

for (const signal of ['SIGINT', 'SIGTERM']) test(`Playwright ${signal} finalizes the interrupted attempt and exports`, { timeout: 20000, skip: process.platform === 'win32' }, async t => {
  const run = await runFixture(t, 'cancel.spec.cjs');
  const deadline = Date.now() + 10000;
  for (;;) {
    try { await fs.stat(path.join(run.directory, 'ready')); break; }
    catch (error) { if (error.code !== 'ENOENT' || Date.now() > deadline) throw error; }
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  run.child.kill(signal);
  const result = await run.complete;
  assert.notEqual(result.code, 0, result.output);
  assert.equal(result.signal, null, result.output);
  const calls = await run.calls();
  assert.equal(calls.find(call => call.path === '/v1/teardownTest').body.status, 'fail');
  assert.equal(calls.at(-1).path, '/v1/finishRun');
  assert.equal(JSON.parse(await fs.readFile(path.join(run.directory, 'last-run.json'), 'utf8')).status, 'complete');
});

test('Playwright fails clearly when the shared test import misses the adapter', { timeout: 15000 }, async t => {
  const run = await runFixture(t, 'bypass.spec.cjs');
  const result = await run.complete;
  assert.equal(result.code, 1, result.output);
  assert.match(result.output, /did not use Wiretap fixtures/);
});

test('Playwright rejects per-test parallel origin changes before creating a session', { timeout: 15000 }, async t => {
  const run = await runFixture(t, 'target.spec.cjs', { workers: 2 }, { explicitSession: true });
  const result = await run.complete;
  assert.equal(result.code, 1, result.output);
  assert.match(result.output, /same SUT origin/);
  assert.equal((await run.calls()).some(call => call.path === '/v1/setupTest'), false);
});
