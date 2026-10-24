const assert = require('node:assert/strict');
const childProcess = require('node:child_process');
const { EventEmitter } = require('node:events');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { PassThrough } = require('node:stream');
const test = require('node:test');
const { startManagedRuntime } = require('../src/managed-runtime');
const { withWiretap } = require('../src/jest-config');
const setup = require('../src/jest-global-setup');
const teardown = require('../src/jest-global-teardown');

async function fixture(t, behavior = {}) {
  const outputDir = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-managed-test-'));
  t.after(() => fs.rm(outputDir, { recursive: true, force: true }));
  const calls = [], signals = [];
  let spawned;
  const receipt = { status: 'complete', run_id: 'run-1', bundle_path: path.join(outputDir, 'run-1'), outputs: { coverage: { json: path.join(outputDir, 'run-1', 'coverage.json') }, errors: [] } };
  t.mock.method(childProcess, 'spawn', (binary, args, options) => {
    const child = new EventEmitter();
    let exited = false;
    child.pid = 10101;
    child.stdin = new PassThrough();
    child.stdout = new PassThrough();
    child.stderr = new PassThrough();
    child.once('exit', () => { exited = true; });
    child.once('error', () => { exited = true; });
    child.stdin.once('close', () => {
      if (!exited && !behavior.ignoreEOF) child.emit('exit', 0, null);
    });
    child.kill = signal => {
      signals.push(signal);
      queueMicrotask(() => child.emit('exit', null, signal));
      return true;
    };
    spawned = { binary, args, options, child };
    queueMicrotask(async () => {
      if (behavior.startError) return child.emit('error', new Error('executable missing'));
      if (behavior.exitEarly) {
        child.stderr.write('invalid project configuration');
        child.stdin.emit('error', Object.assign(new Error('broken pipe'), { code: 'EPIPE' }));
        return child.emit('exit', 1, null);
      }
      if (behavior.neverReady) return;
      await fs.writeFile(options.env.WIRETAP_READY_FILE, JSON.stringify({ control_url: 'http://127.0.0.1:12345', pid: child.pid }));
    });
    return child;
  });
  t.mock.method(global, 'fetch', async (url, options) => {
    calls.push({ path: url.pathname, body: JSON.parse(options.body) });
    if (url.pathname === '/v1/startRun') {
      return Response.json({ sut_url: 'http://127.0.0.1:23456', run_id: 'run-1', bundle_path: receipt.bundle_path });
    }
    if (behavior.finishError) return Response.json({ ...receipt, error: 'export failed' }, { status: 500 });
    return Response.json(receipt);
  });
  return { outputDir, calls, signals, receipt, get spawned() { return spawned; } };
}

test('managed runtime uses owned ephemeral listeners and returns the exact once-only receipt', async t => {
  const f = await fixture(t);
  const outputs = { coverage: ['text'], postman: true };
  const runtime = await startManagedRuntime({ binary: '/custom/wiretap', config: '.wiretap.yaml', cwd: f.outputDir, outputDir: f.outputDir, outputs });
  assert.equal(runtime.baseUrl, 'http://127.0.0.1:12345/');
  assert.equal(runtime.sutUrl, 'http://127.0.0.1:23456');
  assert.equal(f.spawned.binary, '/custom/wiretap');
  assert.deepEqual(f.spawned.args, ['--config', path.join(f.outputDir, '.wiretap.yaml')]);
  assert.equal(f.spawned.options.env.WIRETAP_HTTP_LISTEN, '127.0.0.1:0');
  assert.equal(f.spawned.options.env.WIRETAP_PROXY_LISTEN, '127.0.0.1:0');
  assert.equal(f.spawned.options.env.WIRETAP_EXIT_ON_STDIN_EOF, '1');
  assert.deepEqual(f.spawned.options.stdio, ['pipe', 'pipe', 'pipe']);
  assert.equal(f.spawned.child.stdin.destroyed, false);
  const first = runtime.close();
  assert.equal(runtime.close(), first);
  assert.deepEqual(await first, f.receipt);
  assert.deepEqual(f.calls.map(c => c.path), ['/v1/startRun', '/v1/finishRun']);
  assert.deepEqual(f.calls.at(-1).body.outputs, outputs);
  assert.equal(f.spawned.child.stdin.destroyed, true);
  assert.deepEqual(f.signals, []);
  assert.deepEqual(JSON.parse(await fs.readFile(path.join(f.outputDir, 'last-run.json'))), f.receipt);
  await assert.rejects(fs.stat(f.spawned.options.env.WIRETAP_READY_FILE), { code: 'ENOENT' });
});

test('export failure preserves partial receipt and force kills a child that ignores EOF', async t => {
  const f = await fixture(t, { finishError: true, ignoreEOF: true });
  const runtime = await startManagedRuntime({ outputDir: f.outputDir, shutdownTimeoutMs: 5 });
  await assert.rejects(runtime.close(), error => {
    assert.equal(error.body.error, 'export failed');
    assert.equal(error.body.bundle_path, f.receipt.bundle_path);
    return true;
  });
  assert.equal(f.spawned.child.stdin.destroyed, true);
  assert.deepEqual(f.signals, ['SIGKILL']);
  assert.equal(JSON.parse(await fs.readFile(path.join(f.outputDir, 'last-run.json'))).error, 'export failed');
});

for (const [name, behavior, expected] of [
  ['missing executable', { startError: true }, /executable missing/],
  ['early process exit', { exitEarly: true }, /invalid project configuration/],
  ['startup timeout', { neverReady: true }, /did not become ready/]
]) {
  test(`managed startup cleans up after ${name}`, async t => {
    const f = await fixture(t, behavior);
    await assert.rejects(startManagedRuntime({ outputDir: f.outputDir, startupTimeoutMs: 10, shutdownTimeoutMs: 10 }), expected);
    assert.deepEqual(f.calls, []);
    await assert.rejects(fs.stat(path.dirname(f.spawned.options.env.WIRETAP_READY_FILE)), { code: 'ENOENT' });
    assert.equal(f.spawned.child.stdin.destroyed, true);
    assert.deepEqual(f.signals, []);
  });
}

test('aborting startup stops the child and preserves the cancellation error', async t => {
  const f = await fixture(t, { neverReady: true });
  const controller = new AbortController();
  const pending = startManagedRuntime({ signal: controller.signal, outputDir: f.outputDir });
  setTimeout(() => controller.abort(new Error('suite cancelled')), 10);
  await assert.rejects(pending, /suite cancelled/);
  assert.equal(f.spawned.child.stdin.destroyed, true);
  assert.deepEqual(f.signals, []);
});

test('aborting an active owner finalizes once and stops the child', async t => {
  const f = await fixture(t);
  const controller = new AbortController();
  const runtime = await startManagedRuntime({ signal: controller.signal, outputDir: f.outputDir });
  controller.abort();
  await runtime.close();
  assert.deepEqual(f.calls.map(c => c.path), ['/v1/startRun', '/v1/finishRun']);
  assert.equal(f.spawned.child.stdin.destroyed, true);
  assert.deepEqual(f.signals, []);
});

test('withWiretap preserves the existing runner configuration and guards unsafe modes', () => {
  const original = { rootDir: '/project', reporters: ['default'], testMatch: ['**/*.spec.js'], transform: {}, globalSetup: './setup.cjs', globalTeardown: './teardown.cjs', testEnvironmentOptions: { custom: true } };
  const config = withWiretap(original, { binary: '/bin/wiretap' });
  assert.equal(config.reporters, original.reporters);
  assert.equal(config.testMatch, original.testMatch);
  assert.equal(config.transform, original.transform);
  assert.equal(config.maxWorkers, 1);
  assert.equal(config.testEnvironmentOptions.custom, true);
  assert.equal(config.testEnvironmentOptions.wiretap.globalSetup, './setup.cjs');
  assert.equal(Object.hasOwn(config.testEnvironmentOptions.wiretap.managed, 'cwd'), false);
  assert.equal(original.globalSetup, './setup.cjs');
  assert.throws(() => withWiretap({ maxWorkers: 2 }), /explicitSession/);
  assert.equal(withWiretap({ maxWorkers: 2 }, { explicitSession: true }).maxWorkers, 2);
  assert.throws(() => withWiretap({ testEnvironment: 'jsdom' }), /Node test environment/);
  assert.throws(() => withWiretap({ watch: true }), /single Jest run/);
});

test('Jest composes existing hooks, restores environment and stops Wiretap after export failure', async t => {
  const f = await fixture(t, { finishError: true });
  globalThis.__wiretapHookOrder = [];
  t.after(() => { delete globalThis.__wiretapHookOrder; });
  await fs.writeFile(path.join(f.outputDir, 'setup.cjs'), "module.exports = async (globalConfig, projectConfig) => { globalThis.__wiretapHookOrder.push(['setup', globalConfig.marker, projectConfig.rootDir]); };\n");
  await fs.writeFile(path.join(f.outputDir, 'teardown.cjs'), "module.exports = async () => { globalThis.__wiretapHookOrder.push(['teardown']); throw new Error('original teardown failed'); };\n");
  const originalBase = process.env.WIRETAP_BASE_URL;
  const sigtermListeners = process.listenerCount('SIGTERM');
  const config = withWiretap({ rootDir: f.outputDir, globalSetup: './setup.cjs', globalTeardown: './teardown.cjs' }, { outputDir: f.outputDir });
  await setup({ marker: 'preserved', maxWorkers: 1 }, config);
  assert.equal(process.env.WIRETAP_BASE_URL, 'http://127.0.0.1:12345/');
  assert.equal(process.env.WIRETAP_SUT_URL, 'http://127.0.0.1:23456');
  await assert.rejects(teardown({}, config), error => {
    assert.equal(error.body.error, 'export failed');
    assert.match(error.cleanupError.message, /original teardown failed/);
    return true;
  });
  assert.deepEqual(globalThis.__wiretapHookOrder, [['setup', 'preserved', f.outputDir], ['teardown']]);
  assert.equal(f.spawned.child.stdin.destroyed, true);
  assert.deepEqual(f.signals, []);
  assert.equal(process.env.WIRETAP_BASE_URL, originalBase);
  assert.equal(process.listenerCount('SIGTERM'), sigtermListeners);
  await teardown({}, config);
  assert.equal(globalThis.__wiretapHookOrder.length, 2);
});

test('failed Wiretap startup still invokes the existing Jest teardown', async t => {
  const f = await fixture(t, { exitEarly: true });
  globalThis.__wiretapHookOrder = [];
  t.after(() => { delete globalThis.__wiretapHookOrder; });
  await fs.writeFile(path.join(f.outputDir, 'teardown.cjs'), "module.exports = async () => { globalThis.__wiretapHookOrder.push('teardown'); };\n");
  const config = withWiretap({ rootDir: f.outputDir, globalTeardown: './teardown.cjs' }, { outputDir: f.outputDir });
  await assert.rejects(setup({ maxWorkers: 1 }, config), /invalid project configuration/);
  assert.deepEqual(globalThis.__wiretapHookOrder, ['teardown']);
});

test('managed startup uses the normalized Jest project root for a nested config', async t => {
  const f = await fixture(t);
  const projectDir = path.join(f.outputDir, 'nested');
  await fs.mkdir(projectDir);
  const configPath = path.join(projectDir, 'jest.config.cjs');
  await fs.writeFile(configPath, `const { withWiretap } = require(${JSON.stringify(require.resolve('../src/jest-config'))});\nmodule.exports = withWiretap({ transform: {} }, { config: '.wiretap.yaml', outputDir: 'artifacts' });\n`);
  const { projectConfig, globalConfig } = await require('jest-config').readConfig({ config: configPath }, process.cwd());
  await setup(globalConfig, projectConfig);
  try {
    const normalizedDir = await fs.realpath(projectDir);
    assert.equal(f.spawned.options.cwd, normalizedDir);
    assert.deepEqual(f.spawned.args, ['--config', path.join(normalizedDir, '.wiretap.yaml')]);
    assert.equal(f.spawned.options.env.WIRETAP_OUTPUT_DIR, path.join(normalizedDir, 'artifacts'));
  } finally { await teardown(globalConfig, projectConfig); }
});

test('composed Jest hooks retain the project transformer while awaiting setup and teardown', async t => {
  const f = await fixture(t);
  globalThis.__wiretapHookOrder = [];
  t.after(() => { delete globalThis.__wiretapHookOrder; });
  await fs.writeFile(path.join(f.outputDir, 'setup.ts'), 'HOOK_SOURCE_REQUIRES_TRANSFORM');
  await fs.writeFile(path.join(f.outputDir, 'teardown.ts'), 'HOOK_SOURCE_REQUIRES_TRANSFORM');
  await fs.writeFile(path.join(f.outputDir, 'transform.cjs'), `module.exports = {
    process(source, filename) {
      return { code: 'module.exports = async () => { await Promise.resolve(); globalThis.__wiretapHookOrder.push(' + JSON.stringify(filename.endsWith('setup.ts') ? 'setup' : 'teardown') + '); };' };
    }
  };\n`);
  const config = withWiretap({
    rootDir: f.outputDir, globalSetup: './setup.ts', globalTeardown: './teardown.ts',
    transform: { '\\.ts$': path.join(f.outputDir, 'transform.cjs') },
    cacheDirectory: path.join(f.outputDir, 'cache')
  }, { outputDir: f.outputDir });
  const { projectConfig, globalConfig } = await require('jest-config').readConfig({ config: JSON.stringify(config) }, f.outputDir);
  const transformer = await require('@jest/transform').createScriptTransformer(projectConfig);
  await transformer.requireAndTranspileModule(config.globalSetup, hook => hook(globalConfig, projectConfig));
  await transformer.requireAndTranspileModule(config.globalTeardown, hook => hook(globalConfig, projectConfig));
  assert.deepEqual(globalThis.__wiretapHookOrder, ['setup', 'teardown']);
  assert.equal(f.spawned.child.stdin.destroyed, true);
  assert.deepEqual(f.signals, []);
});
