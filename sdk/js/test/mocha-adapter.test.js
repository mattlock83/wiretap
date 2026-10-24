const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { execFile, spawn } = require('node:child_process');
const { once } = require('node:events');
const { promisify } = require('node:util');
const execute = promisify(execFile);
const { withWiretap } = require('../mocha');

async function fixture(t, source, options = {}, runnerConfig = {}) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-mocha-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const resultPath = path.join(dir, 'result.json');
  const file = source || path.join(__dirname, 'fixtures/mocha-outcomes.cjs');
  const configPath = path.join(dir, '.mocharc.cjs');
  await fs.writeFile(configPath, `require(${JSON.stringify(path.join(__dirname, 'fixtures/mocha-runtime.cjs'))});
  module.exports = require(${JSON.stringify(require.resolve('../mocha'))}).withWiretap({
    require: [${JSON.stringify(path.join(__dirname, 'fixtures/mocha-runtime.cjs'))}],
    reporter: 'json', timeout: 2000, spec: [${JSON.stringify(file)}], ...${JSON.stringify(runnerConfig)}
  }, ${JSON.stringify(options)});`);
  return { dir, resultPath, configPath, async run(args = [], env = {}, mochaPackage = 'mocha') {
    try {
      const result = await execute(process.execPath, [require.resolve(`${mochaPackage}/bin/mocha.js`), '--config', configPath, ...args], {
        env: { ...process.env, NODE_TEST_CONTEXT: '', WIRETAP_MOCHA_RESULT: resultPath, WIRETAP_MOCHA_PACKAGE: mochaPackage, ...env }, timeout: 30000, maxBuffer: 1024 * 1024
      });
      return { ...result, code: 0 };
    } catch (error) { if (error.killed || typeof error.code !== 'number') throw error; return error; }
  } };
}

test('Mocha config keeps reporters and test selection and rejects unsupported modes', () => {
  const config = { reporter: 'dot', spec: ['test/**/*.js'], require: ['existing.cjs'] };
  const wrapped = withWiretap(config, { app: './app.js' });
  assert.equal(wrapped.reporter, 'dot');
  assert.equal(wrapped.spec, config.spec);
  assert.deepEqual(wrapped.require, ['existing.cjs', require.resolve('../mocha')]);
  assert.deepEqual(config.require, ['existing.cjs']);
  assert.throws(() => withWiretap({ parallel: true }), /serial/);
  assert.throws(() => withWiretap({ watch: true }), /single run/);
  assert.throws(() => withWiretap({ enableGlobalTeardown: false }), /global teardown/);
});

for (const mochaPackage of ['mocha', 'mocha-11']) test(`real ${mochaPackage} preserves final outcomes, hook attribution, callbacks, skips and retries`, async t => {
  const f = await fixture(t);
  const child = await f.run([], {}, mochaPackage);
  assert.equal(child.code, 6, child.stderr || child.stdout);
  const report = JSON.parse(child.stdout);
  assert.equal(report.stats.failures, 6);
  assert.equal(report.stats.pending, 2);
  const result = JSON.parse(await fs.readFile(f.resultPath));
  assert.equal(result.closed, true);
  assert.deepEqual(result.cleanup, ['user afterAll']);
  const setups = result.calls.filter(call => call.path === '/v1/setupTest');
  const teardowns = result.calls.filter(call => call.path === '/v1/teardownTest');
  assert.equal(setups.length, 13);
  assert.equal(teardowns.length, setups.length);
  const outcomes = setups.map((setup, index) => ({ name: setup.body.name, status: teardowns[index].body.status, identity: setup.body.test_identity }));
  for (const name of ['pass', 'callback', 'duplicate']) assert.ok(outcomes.filter(entry => entry.name === name).every(entry => entry.status === 'pass'), name);
  for (const name of ['assertion failure', 'timeout', 'never settles', 'nested fixtures cleanup failure', 'failed setup body never runs', 'root failure last root cleanup failure']) {
    assert.equal(outcomes.find(entry => entry.name === name)?.status, 'fail', name);
  }
  assert.equal(outcomes.find(entry => entry.name === 'skip at runtime')?.status, 'skip');
  assert.equal(outcomes.some(entry => entry.name === 'static skip'), false);
  const retries = outcomes.filter(entry => entry.name === 'retry');
  assert.deepEqual(retries.map(entry => entry.status), ['fail', 'pass']);
  assert.deepEqual(retries.map(entry => entry.identity.attempt_ordinal), [1, 2]);
  assert.equal(retries[0].identity.id, retries[1].identity.id);
  const duplicates = outcomes.filter(entry => entry.name === 'duplicate');
  assert.notEqual(duplicates[0].identity.id, duplicates[1].identity.id);
  assert.equal(result.calls.some(call => call.path === '/must-not-run'), false);
  for (let i = 0; i < setups.length; i++) {
    const start = result.calls.indexOf(setups[i]);
    const end = result.calls.indexOf(teardowns[i]);
    const traffic = result.calls.slice(start + 1, end).filter(call => !call.path.startsWith('/v1/'));
    assert.ok(traffic.length >= 2, `${setups[i].body.name}: root setup and cleanup`);
    for (const call of traffic) assert.equal(call.traceparent?.split('-')[1], setups[i].body.trace_context.traceparent.split('-')[1], call.path);
  }
  const suiteCleanup = result.calls.find(call => call.path === '/suite/cleanup');
  assert.ok(setups.every(call => call.body.trace_context.traceparent.split('-')[1] !== suiteCleanup.traceparent?.split('-')[1]));
  assert.ok(result.calls.indexOf(suiteCleanup) > result.calls.indexOf(teardowns.at(-1)));
});

test('Mocha finalizes an empty filtered run and rejects CLI parallel/watch overrides', async t => {
  const f = await fixture(t);
  const child = await f.run(['--grep', '^does not exist$']);
  assert.equal(child.code, 0, child.stderr);
  const result = JSON.parse(await fs.readFile(f.resultPath));
  assert.equal(result.closed, true);
  assert.equal(result.calls.some(call => call.path === '/v1/setupTest'), false);
  for (const flag of ['--parallel', '--watch', '-p', '-w']) {
    const failure = await f.run([flag]);
    assert.notEqual(failure.code, 0);
    assert.match(failure.stderr, /Wiretap Mocha capture requires/);
  }
});

for (const mochaPackage of ['mocha', 'mocha-11']) test(`${mochaPackage} owns a handler module through user global teardown`, async t => {
  const f = await fixture(t);
  const appPath = path.join(f.dir, 'app.mjs');
  const testPath = path.join(f.dir, 'app.test.cjs');
  const pluginPath = path.join(f.dir, 'plugin.cjs');
  await fs.writeFile(appPath, "export default function app(req, res) { res.end('hello'); }\n");
  await fs.writeFile(testPath, "it('handler stays open', () => {});\n");
  await fs.writeFile(pluginPath, `module.exports = {
    mochaGlobalTeardown: async function () {
      globalThis.__wiretapMochaCleanup = ['user global teardown'];
      require('node:assert/strict').equal(await (await fetch(globalThis.__wiretapMochaSutTarget)).text(), 'hello');
      if (process.env.WIRETAP_FAIL_GLOBAL_TEARDOWN) throw new Error('user global teardown failed');
    },
    mochaGlobalSetup: function () { if (process.env.WIRETAP_FAIL_GLOBAL_SETUP) throw new Error('user global setup failed'); }
  };`);
  const configured = await fixture(t, testPath, { app: appPath }, { require: [pluginPath] });
  const child = await configured.run(['--exit'], {}, mochaPackage);
  assert.equal(child.code, 0, child.stderr);
  const result = JSON.parse(await fs.readFile(configured.resultPath));
  assert.deepEqual(result.cleanup, ['user global teardown']);
  const setup = result.calls.find(call => call.path === '/v1/setupTest');
  assert.match(setup.body.sut_base_url, /^http:\/\/127\.0\.0\.1:\d+$/);
  await assert.rejects(fetch(setup.body.sut_base_url), /fetch failed/);
  for (const variable of ['WIRETAP_FAIL_GLOBAL_SETUP', 'WIRETAP_FAIL_GLOBAL_TEARDOWN']) {
    const failure = await configured.run([], { [variable]: '1' }, mochaPackage);
    assert.notEqual(failure.code, 0, failure.stderr);
    const failedResult = JSON.parse(await fs.readFile(configured.resultPath));
    assert.equal(failedResult.closed, true);
    if (variable === 'WIRETAP_FAIL_GLOBAL_SETUP') assert.equal(failedResult.calls.some(call => call.path === '/v1/setupTest'), false);
  }
});

test('Mocha export errors fail the run and still close the owned runtime', async t => {
  const f = await fixture(t);
  const child = await f.run(['--grep', '^pass$'], { WIRETAP_MOCHA_EXPORT_FAIL: '1' });
  assert.notEqual(child.code, 0, child.stderr);
  const result = JSON.parse(await fs.readFile(f.resultPath));
  assert.equal(result.closed, true);
  assert.equal(result.calls.filter(call => call.path === '/v1/teardownTest').length, 1);
});

test('Mocha applies top-level capture defaults with per-test defaults taking precedence', async t => {
  const f = await fixture(t, undefined, {
    capture: { tags: ['top-level'], meta: { source: 'top-level' }, drainTimeoutMs: 321,
      testDefaults: { tags: ['per-test'], drainTimeoutMs: 123 } }
  });
  const child = await f.run(['--grep', '^pass$']);
  assert.equal(child.code, 0, child.stderr);
  const result = JSON.parse(await fs.readFile(f.resultPath));
  const setup = result.calls.find(call => call.path === '/v1/setupTest').body;
  const teardown = result.calls.find(call => call.path === '/v1/teardownTest').body;
  assert.deepEqual(setup.tags, ['per-test']);
  assert.deepEqual(setup.meta, { source: 'top-level' });
  assert.equal(teardown.drain_timeout_ms, 123);
});

for (const mochaPackage of ['mocha', 'mocha-11']) test(`${mochaPackage} records an SDK setup timeout as a failed attempt`, async t => {
  const f = await fixture(t);
  const testPath = path.join(f.dir, 'setup-timeout.test.cjs');
  const pluginPath = path.join(f.dir, 'short-hook.cjs');
  await fs.writeFile(testPath, "it('body must not run', () => { throw new Error('body ran'); }); after(() => new Promise(resolve => setTimeout(resolve, 100)));\n");
  await fs.writeFile(pluginPath, `exports.mochaHooks = { beforeAll() {
    globalThis.__wiretapSetupHook = this.test.parent._beforeEach.find(hook => hook.fn.name === 'beforeEach');
  } };`);
  const configured = await fixture(t, testPath, {}, { require: [pluginPath] });
  const source = await fs.readFile(configured.configPath, 'utf8');
  for (const initialization of ['new Promise(() => {})', 'new Promise(resolve => setTimeout(resolve, 80))']) {
    await fs.writeFile(configured.configPath, source.replace('}, {});', `}, { capture: { onSession() {
      // Shorten only this native Mocha hook's clock to keep the regression quick.
      globalThis.__wiretapSetupHook.timeout(30);
      return ${initialization};
    } } });`));
    const child = await configured.run([], {}, mochaPackage);
    assert.equal(child.code, 1, child.stderr || child.stdout);
    const report = JSON.parse(child.stdout);
    assert.match(report.failures[0].err.message, /Timeout/);
    const result = JSON.parse(await fs.readFile(configured.resultPath));
    assert.equal(result.calls.filter(call => call.path === '/v1/setupTest').length, 1);
    assert.equal(result.calls.find(call => call.path === '/v1/teardownTest').body.status, 'fail');
    assert.equal(result.closed, true);
  }
});

for (const mochaPackage of ['mocha', 'mocha-11']) test(`${mochaPackage} closes the runtime when a test module cannot load`, async t => {
  const f = await fixture(t);
  const file = path.join(f.dir, 'broken.test.cjs');
  for (const source of ["throw new Error('module initialization failed');", 'function invalid syntax']) {
    await fs.writeFile(file, source);
    const configured = await fixture(t, file);
    const child = await configured.run([], {}, mochaPackage);
    assert.notEqual(child.code, 0);
    assert.match(child.stderr, /module initialization failed|SyntaxError/);
    const result = JSON.parse(await fs.readFile(configured.resultPath));
    assert.equal(result.closed, true);
    assert.equal(result.calls.some(call => call.path === '/v1/setupTest'), false);
  }
});

test('Mocha cancellation closes the active attempt and owned runtime before exiting', {
  skip: process.platform === 'win32' ? 'Windows child.kill(SIGTERM) forcibly terminates Node instead of delivering its signal handler' : false
}, async t => {
  const f = await fixture(t);
  const testPath = path.join(f.dir, 'interrupt.test.cjs');
  const readyPath = path.join(f.dir, 'ready');
  await fs.writeFile(testPath, `it('interrupted', async function () {
    this.timeout(0);
    require('node:fs').writeFileSync(${JSON.stringify(readyPath)}, 'ready');
    await new Promise(() => {});
  });`);
  const configured = await fixture(t, testPath);
  const child = spawn(process.execPath, [require.resolve('mocha/bin/mocha.js'), '--config', configured.configPath], {
    env: { ...process.env, NODE_TEST_CONTEXT: '', WIRETAP_MOCHA_RESULT: configured.resultPath }, stdio: ['ignore', 'pipe', 'pipe']
  });
  let stderr = ''; child.stderr.on('data', chunk => { stderr += chunk; });
  child.stdout.resume();
  const closed = once(child, 'close');
  t.after(() => { if (child.exitCode === null && !child.signalCode) child.kill('SIGKILL'); });
  const deadline = Date.now() + 10000;
  for (;;) {
    try { await fs.access(readyPath); break; } catch {}
    assert.ok(Date.now() < deadline, `child did not start: ${stderr}`);
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  child.kill('SIGTERM');
  const timer = setTimeout(() => child.kill('SIGKILL'), 10000);
  const [code] = await closed;
  clearTimeout(timer);
  assert.equal(code, 143, stderr);
  const result = JSON.parse(await fs.readFile(configured.resultPath));
  assert.equal(result.closed, true);
  assert.equal(result.calls.find(call => call.path === '/v1/teardownTest').body.status, 'fail');
});
