// Install the packed SDK in a clean project and exercise real runner lifecycles.
// Intentional failures must retain their framework outcomes and still export.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs/promises';
import http from 'node:http';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
assert.ok(process.argv.length === 2 || (process.argv.length === 4 && process.argv[2] === '--binary'),
  'Usage: node scripts/runner-smoke.mjs [--binary path]');
const scratch = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-runner-smoke-'));
const consumer = path.join(scratch, 'external project');
const active = new Set();
let interrupted, upstream, lastRunnerResult, interruptDeadline;

function stop(child, signal = 'SIGTERM') {
  if (!child.pid) return;
  if (process.platform === 'win32') {
    const killer = spawn('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
    killer.on('error', () => { child.kill('SIGKILL'); });
    const timer = setTimeout(() => killer.kill('SIGKILL'), 5000);
    killer.once('close', () => clearTimeout(timer));
  } else {
    try { process.kill(-child.pid, signal); }
    catch (error) { if (error.code !== 'ESRCH') throw error; }
  }
}
const interrupt = signal => {
  interrupted = signal;
  for (const child of active) { child.smokeCancelled = true; stop(child); }
  interruptDeadline ??= setTimeout(() => { for (const child of active) stop(child, 'SIGKILL'); }, 5000);
};
const onInterrupt = () => interrupt('SIGINT');
const onTerminate = () => interrupt('SIGTERM');
process.once('SIGINT', onInterrupt);
process.once('SIGTERM', onTerminate);

async function execute(command, args, options = {}) {
  assert.equal(interrupted, undefined, `runner smoke interrupted by ${interrupted}`);
  if (process.platform === 'win32' && command === 'npm') {
    args = [process.env.npm_execpath || path.join(path.dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'), ...args];
    command = process.execPath;
  }
  const child = spawn(command, args, { cwd: root, env: process.env, detached: process.platform !== 'win32', ...options });
  active.add(child);
  let stdout = '', stderr = '', forced;
  child.stdout.on('data', chunk => { stdout += chunk; });
  child.stderr.on('data', chunk => { stderr += chunk; });
  const timeout = setTimeout(() => {
    child.smokeCancelled = true;
    stop(child);
    forced = setTimeout(() => stop(child, 'SIGKILL'), 5000);
  }, 300000);
  try {
    const [code, signal] = await once(child, 'close');
    return { code, signal, stdout, stderr };
  } finally {
    clearTimeout(timeout);
    clearTimeout(forced);
    if (child.smokeCancelled) stop(child, 'SIGKILL');
    active.delete(child);
  }
}
async function mustRun(command, args, options) {
  const result = await execute(command, args, options);
  assert.equal(result.code, 0, `${command} failed (${result.signal || result.code}):\n${result.stderr}\n${result.stdout}`);
  return result;
}
const readJSON = async filename => JSON.parse(await fs.readFile(filename, 'utf8'));
const application = `module.exports = (req, res) => {
  const id = new URL(req.url, 'http://localhost').pathname.split('/').at(-1);
  res.setHeader('content-type', 'application/json');
  res.end(JSON.stringify({ id, label: 'Item ' + id, header: req.headers['x-existing'] || '' }));
};\n`;
const contract = `openapi: 3.0.3
info: {title: Runner smoke, version: '1.0'}
paths:
  /items/{id}:
    get:
      operationId: getItem
      parameters:
        - name: id
          in: path
          required: true
          schema: {type: string, pattern: '^[a-z]+$'}
      responses:
        '200':
          description: An item
          content:
            application/json:
              schema:
                type: object
                required: [id, label, header]
                properties:
                  id: {type: string}
                  label: {type: string}
                  header: {type: string}
`;
const projectConfig = `version: 1
application: {id: runner-smoke}
output_dir: artifacts
sut:
  id: smoke-api
  kind: service
  schema: openapi.yaml
`;
const marker = `const fs = require('node:fs'); const path = require('node:path');
const mark = event => fs.appendFileSync(path.join(__dirname, 'hooks.jsonl'), JSON.stringify(event) + '\\n');\n`;
const typedConsumer = `
import type { RequestListener } from 'node:http';
import { startCapture, createNodeTestSuite, bindTest, initializeTracing,
  type CaptureOptions, type Config, type Runtime, type Session } from '@wiretap/sdk';
import '@wiretap/sdk/tracing';
import { startManagedRuntime, type ManagedRuntimeOptions } from '@wiretap/sdk/managed-runtime';
import { withWiretap as jestConfig, captureJest, createJestSuite } from '@wiretap/sdk/jest';
import type { Config as JestConfig } from 'jest';
import { withWiretap as mochaConfig, mochaHooks, mochaGlobalTeardown, type MochaWiretapOptions } from '@wiretap/sdk/mocha';
import type { MochaOptions } from 'mocha';
import { withWiretap as playwrightConfig, capturePlaywright, test, expect,
  type WiretapPlaywrightOptions } from '@wiretap/sdk/playwright';
import { test as playwrightBase, type PlaywrightTestConfig } from '@playwright/test';

const app: RequestListener = (_request, response) => { response.end('ok'); };
const capture: CaptureOptions = { tags: ['smoke'], schemaRequired: false, testDefaults: { drainTimeoutMs: 10 },
  outputs: { coverage: ['json', 'text'], postman: true } };
const managed: ManagedRuntimeOptions = { binary: 'wiretap', config: '.wiretap.yaml', outputDir: 'artifacts', timeoutMs: 10000 };
const plain: Config = { ...capture, manageRuntime: false };
const existingJest: JestConfig = { testEnvironment: 'node', reporters: ['default'] };
const configuredJest: JestConfig = jestConfig(existingJest, { ...managed, app: '<rootDir>/app.cjs', capture, explicitSession: false });
const existingMocha: MochaOptions = { reporter: 'json', timeout: 1000 };
const mochaOptions: MochaWiretapOptions = { ...managed, app, capture };
const configuredMocha: MochaOptions = mochaConfig(existingMocha, mochaOptions);
const existingPlaywright: PlaywrightTestConfig = { use: { baseURL: 'http://localhost:3000' }, reporter: [['list']] };
const playwrightOptions: WiretapPlaywrightOptions = { ...managed, capture, explicitSession: false };
const configuredPlaywright: PlaywrightTestConfig = playwrightConfig(existingPlaywright, playwrightOptions);
void [configuredJest, configuredMocha, configuredPlaywright];

async function lifecycle(): Promise<void> {
  initializeTracing({ instrumentation: true });
  const owner = await startManagedRuntime(managed);
  const runtime: Runtime = await startCapture({ ...plain, baseUrl: owner.baseUrl });
  const attempt = runtime.createTestAttempt('case', { identity: { id: 'stable-id', attemptOrdinal: 1 } });
  const url: string = await attempt.run((session: Session) => session.sutUrl);
  await attempt.finish({ status: 'pass' });
  const result: number = await runtime.runTest('callback', async session => (await fetch(session.sutUrl)).status);
  const bound = bindTest(runtime, (_name: string, callback: () => unknown) => callback(), { tags: ['bound'] });
  bound('bound case', async (_context, session) => { await fetch(session.sutUrl); });
  const suite = createNodeTestSuite(plain);
  suite.test('node case', async (context, session) => { context.assert.ok(session.sessionId); });
  await suite.start();
  await suite.close();
  await runtime.finishRun();
  const receipt = await owner.close();
  const archive: string | undefined = receipt.bundle_path;
  void [url, result, archive];
}
void lifecycle;

function customJestFixture(): void {
  captureJest({ ...plain, app, onSession(session) { const id: string = session.sessionId; void id; } });
  const suite = createJestSuite(plain);
  suite.test('selected case', async () => { await fetch(suite.sutUrl); });
  suite.test.each(['one', 'two'])('parameter %s', async value => { void value; });
}
void customJestFixture;
async function mochaLifecycle(): Promise<void> {
  const hooks = await mochaHooks();
  void [hooks.beforeAll, hooks.beforeEach];
  await mochaGlobalTeardown();
}
void mochaLifecycle;

const existingTest = playwrightBase.extend<{ customerId: string }>({
  customerId: async ({}, use) => { await use('synthetic-customer'); },
});
const capturedTest = capturePlaywright(existingTest);
capturedTest('retains custom and standard fixtures', async ({ request, customerId, wiretap }) => {
  const id: string = customerId;
  const context: string = wiretap.traceContext.traceparent;
  expect((await request.get('/items/one')).status()).toBe(200);
  void [id, context];
});
test.use({ wiretapOptions: { headers: { 'x-synthetic': 'yes' }, drainTimeoutMs: 10 } });
test('default captured fixtures', async ({ request, wiretap }) => {
  expect(wiretap.sessionId).toBeTruthy();
  expect((await request.get('/items/one')).ok()).toBe(true);
});
`;

try {
  const binary = process.argv[3] ? path.resolve(process.argv[3]) : path.join(scratch, process.platform === 'win32' ? 'wiretap.exe' : 'wiretap');
  if (!process.argv[3]) await mustRun('go', ['build', '-mod=readonly', '-trimpath', '-o', binary, './cmd/wiretap']);
  const packed = JSON.parse((await mustRun('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', scratch], { cwd: path.join(root, 'sdk/js') })).stdout)[0];
  const contents = new Set(packed.files.map(file => file.path));
  for (const filename of ['jest.js', 'jest.d.ts', 'mocha.js', 'mocha.d.ts', 'playwright.js', 'playwright.d.ts',
    'src/jest-app-setup.js', 'src/mocha-adapter.js', 'src/playwright-global-setup.js', 'docs/jest.md', 'docs/mocha.md', 'docs/playwright.md']) {
    assert.ok(contents.has(filename), `packed SDK omitted ${filename}`);
  }
  await fs.mkdir(consumer);
  await fs.writeFile(path.join(consumer, 'package.json'), JSON.stringify({ name: 'wiretap-runner-consumer', version: '1.0.0', private: true }));
  await mustRun('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', path.join(scratch, packed.filename),
    'jest@30.5.2', 'mocha@12.0.3', '@playwright/test@1.64.1', 'postman-collection@5.3.1',
    'typescript@7.0.2', '@types/node@26.6.5', '@types/mocha@10.0.10'],
  { cwd: consumer, env: { ...process.env, PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD: '1' } });
  const require = createRequire(path.join(consumer, 'package.json'));
  const installation = await fs.realpath(path.join(consumer, 'node_modules'));
  for (const module of ['@wiretap/sdk', '@wiretap/sdk/jest', '@wiretap/sdk/mocha', '@wiretap/sdk/playwright', 'jest', 'mocha', '@playwright/test']) {
    assert.ok(require.resolve(module).startsWith(installation + path.sep), `${module} escaped the clean installation`);
  }
  await mustRun(process.execPath, ['-e', `
    const assert = require('node:assert/strict');
    const Module = require('node:module');
    const load = Module._load;
    Module._load = function (specifier, ...args) {
      assert.ok(!/^(?:jest(?:-|\\/|$)|@jest\\/|mocha(?:\\/|$)|@playwright\\/|playwright(?:-core)?(?:\\/|$))/.test(specifier),
        'plain JavaScript entry loaded an optional runner: ' + specifier);
      return Reflect.apply(load, this, [specifier, ...args]);
    };
    const sdk = require('@wiretap/sdk');
    for (const name of ['startCapture', 'createNodeTestSuite', 'bindTest', 'initializeTracing']) assert.equal(typeof sdk[name], 'function');
    import('@wiretap/sdk').then(esm => {
      assert.equal(esm.startCapture, sdk.startCapture);
      Module._load = load;
    }).catch(error => { console.error(error); process.exitCode = 1; });
  `], { cwd: consumer });
  await Promise.all([
    fs.writeFile(path.join(consumer, 'consumer.cts'), typedConsumer),
    fs.writeFile(path.join(consumer, 'consumer.mts'), typedConsumer),
    fs.writeFile(path.join(consumer, 'tsconfig.json'), JSON.stringify({
      compilerOptions: { target: 'ES2022', module: 'NodeNext', moduleResolution: 'NodeNext',
        strict: true, noEmit: true, skipLibCheck: false, types: ['node'], esModuleInterop: true },
      files: ['consumer.cts', 'consumer.mts']
    }))
  ]);
  const typescriptPackage = require.resolve('typescript/package.json');
  const compiler = path.resolve(path.dirname(typescriptPackage), (await readJSON(typescriptPackage)).bin.tsc);
  await mustRun(process.execPath, [compiler, '--project', 'tsconfig.json'], { cwd: consumer });
  console.log('Clean CommonJS/ESM TypeScript consumers passed strict NodeNext checks; plain JavaScript loads no optional runners.');
  const { Collection } = require('postman-collection');
  for (const runner of ['jest', 'mocha', 'playwright']) {
    const directory = path.join(consumer, runner);
    await fs.mkdir(directory);
    const write = (filename, text) => fs.writeFile(path.join(directory, filename), text);
    await Promise.all([write('app.cjs', application), write('openapi.yaml', contract), write('.wiretap.yaml', projectConfig)]);
    const env = { ...process.env, WIRETAP_BINARY: binary };
    delete env.NODE_TEST_CONTEXT;
    let args;
    if (runner === 'jest') {
      await Promise.all([
        write('jest.config.cjs', `module.exports = require('@wiretap/sdk/jest').withWiretap({
          testMatch: ['**/outcomes.spec.cjs'], transform: {}, globalSetup: './setup.cjs', globalTeardown: './teardown.cjs'
        }, { app: '<rootDir>/app.cjs', capture: { outputs: { coverage: ['json', 'text'], postman: true } } });\n`),
        write('setup.cjs', marker + `module.exports = async () => { mark('setup'); process.env.USER_SETUP = 'ready'; };\n`),
        write('teardown.cjs', marker + `module.exports = async () => { mark('teardown'); };\n`),
        write('outcomes.spec.cjs', `
          const request = id => fetch(process.env.WIRETAP_SUT_URL + '/items/' + id);
          jest.retryTimes(1, { retryImmediately: true });
          beforeEach(async () => { expect(process.env.USER_SETUP).toBe('ready'); await request('setup'); });
          afterEach(async () => {
            await request('cleanup');
            if (expect.getState().currentTestName === 'cleanup failure') throw new Error('intentional cleanup failure');
          });
          test('passes', async () => { expect((await request('one')).status).toBe(200); });
          let retries = 0;
          test('retries', async () => { await request('retry'); expect(++retries).toBe(2); });
          test('cleanup failure', async () => { expect((await request('last')).status).toBe(200); });
        `)
      ]);
      args = [require.resolve('jest/bin/jest'), '--config', 'jest.config.cjs', '--runInBand', '--json', '--outputFile=runner-results.json'];
    } else if (runner === 'mocha') {
      await Promise.all([
        write('.mocharc.cjs', `module.exports = require('@wiretap/sdk/mocha').withWiretap({
          spec: ['outcomes.spec.cjs'], require: ['./user-hooks.cjs'], reporter: 'json', timeout: 10000
        }, { app: './app.cjs', capture: { outputs: { coverage: ['json', 'text'], postman: true } } });\n`),
        write('user-hooks.cjs', marker + `exports.mochaHooks = {
          beforeAll() { mark('setup'); },
          afterAll() { mark('teardown'); }
        };\n`),
        write('outcomes.spec.cjs', `
          const assert = require('node:assert/strict');
          const request = id => fetch(process.env.WIRETAP_SUT_URL + '/items/' + id);
          beforeEach(async () => { await request('setup'); });
          afterEach(async function () {
            await request('cleanup');
            if (this.currentTest.title === 'cleanup failure') throw new Error('intentional cleanup failure');
          });
          it('passes', async () => { assert.equal((await request('one')).status, 200); });
          it('retries', async function () { this.retries(1); await request('retry'); assert.equal(this.test.currentRetry(), 1); });
          it('cleanup failure', async () => { assert.equal((await request('last')).status, 200); });
        `)
      ]);
      args = [require.resolve('mocha/bin/mocha.js'), '--config', '.mocharc.cjs'];
    } else {
      upstream = http.createServer(require(path.join(directory, 'app.cjs')));
      await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve));
      env.SMOKE_UPSTREAM_URL = `http://127.0.0.1:${upstream.address().port}`;
      await Promise.all([
        write('playwright.config.cjs', `module.exports = require('@wiretap/sdk/playwright').withWiretap({
          testDir: '.', testMatch: 'outcomes.spec.cjs', globalSetup: './setup.cjs', globalTeardown: './teardown.cjs',
          reporter: [['json', { outputFile: 'runner-results.json' }]],
          use: { baseURL: process.env.SMOKE_UPSTREAM_URL, extraHTTPHeaders: { 'x-existing': 'kept' } }
        }, { capture: { outputs: { coverage: ['json', 'text'], postman: true } } });\n`),
        write('setup.cjs', marker + `module.exports = async () => {
          mark('setup'); process.env.USER_SETUP = 'ready'; return async () => { mark('returned cleanup'); };
        };\n`),
        write('teardown.cjs', marker + `module.exports = async () => { mark('teardown'); };\n`),
        write('outcomes.spec.cjs', `
          const { test, expect } = require('@wiretap/sdk/playwright');
          test.beforeEach(async ({ request }) => { expect(process.env.USER_SETUP).toBe('ready'); await request.get('/items/setup'); });
          test.afterEach(async ({ request }, info) => {
            await request.get('/items/cleanup');
            if (info.title === 'cleanup failure') throw new Error('intentional cleanup failure');
          });
          test('passes', async ({ request }) => {
            const response = await request.get('/items/one');
            expect(response.status()).toBe(200);
            expect((await response.json()).header).toBe('kept');
          });
          test.describe('retry group', () => {
            test.describe.configure({ retries: 1 });
            test('retries', async ({ request }, info) => { await request.get('/items/retry'); expect(info.retry).toBe(1); });
          });
          test('cleanup failure', async ({ request }) => { expect((await request.get('/items/last')).status()).toBe(200); });
        `)
      ]);
      args = [require.resolve('@playwright/test/cli'), 'test', '--config', 'playwright.config.cjs'];
    }
    const result = await execute(process.execPath, args, { cwd: directory, env });
    lastRunnerResult = { runner, ...result };
    assert.equal(result.code, 1, `${runner} changed the expected failure exit:\n${result.stderr}\n${result.stdout}`);
    const report = runner === 'mocha' ? JSON.parse(result.stdout) : await readJSON(path.join(directory, 'runner-results.json'));
    if (runner === 'jest') {
      assert.equal(report.numPassedTests, 2);
      assert.equal(report.numFailedTests, 1);
    } else if (runner === 'mocha') {
      assert.equal(report.stats.failures, 1);
      assert.match(report.failures[0].err.message, /intentional cleanup failure/);
    } else {
      assert.equal(report.stats.expected, 1);
      assert.equal(report.stats.flaky, 1);
      assert.equal(report.stats.unexpected, 1);
    }
    const markers = (await fs.readFile(path.join(directory, 'hooks.jsonl'), 'utf8')).trim().split('\n').map(JSON.parse);
    assert.equal(markers[0], 'setup');
    assert.ok(markers.includes('teardown'), `${runner} lost the user's teardown`);
    if (runner === 'playwright') assert.ok(markers.includes('returned cleanup'));
    const receipt = await readJSON(path.join(directory, 'artifacts', 'last-run.json'));
    assert.equal(receipt.status, 'complete', `${runner}: ${JSON.stringify(receipt)}`);
    assert.deepEqual(receipt.outputs.errors, []);
    const bundle = await readJSON(path.join(receipt.bundle_path, 'bundle.json'));
    assert.equal(bundle.run_state, 'complete');
    const attempts = await Promise.all(bundle.tests.map(entry => readJSON(path.join(receipt.bundle_path, entry.path))));
    const cases = attempts.map(attempt => attempt.capture.testcase);
    const named = name => cases.filter(testcase => testcase.name.endsWith(name));
    assert.equal(cases.length, runner === 'jest' ? 5 : 4, `${runner}: ${JSON.stringify(cases)}`);
    assert.deepEqual(named('passes').map(testcase => testcase.status), ['pass']);
    assert.deepEqual(named('retries').map(testcase => testcase.status).sort(), ['fail', 'pass']);
    assert.equal(new Set(named('retries').map(testcase => testcase.identity.id)).size, 1);
    assert.equal(new Set(cases.map(testcase => testcase.identity.id)).size, 3);
    assert.ok(named('cleanup failure').every(testcase => testcase.status === 'fail'), `${runner} lost the cleanup outcome`);
    assert.match(await fs.readFile(receipt.outputs.coverage.text, 'utf8'), /smoke-api: 1\/1 operations exercised/);
    assert.ok(await readJSON(receipt.outputs.coverage.json));
    let requests = 0;
    assert.equal(receipt.outputs.postman.length, 1);
    for (const filename of receipt.outputs.postman) {
      const original = await readJSON(filename);
      const imported = new Collection(original);
      for (const folder of original.item) {
        const parsed = imported.items.one(folder.id);
        assert.equal(parsed.items.count(), folder.item.length);
        for (const [index, item] of folder.item.entries()) {
          const roundTrip = parsed.items.idx(index);
          assert.equal(roundTrip.request.method, item.request.method);
          assert.equal(roundTrip.request.url.toString(), item.request.url);
          assert.deepEqual(roundTrip.request.body?.toJSON(), item.request.body);
          assert.deepEqual(roundTrip.responses.all().map(response => [response.code, response.body]), item.response.map(response => [response.code, response.body]));
          requests++;
        }
      }
    }
    assert.ok(requests >= cases.length * 3, `${runner}: missing setup, body or cleanup traffic (${requests} requests)`);
    console.log(`${runner}: clean package, existing hooks, retry, cleanup failure, ${cases.length} attempts, coverage and ${requests} Postman requests verified`);
  }
  console.log('Jest, Mocha and Playwright packed SDK smoke passed; no packages published and no browsers installed.');
} catch (error) {
  if (lastRunnerResult) console.error(`${lastRunnerResult.runner} output:\n${lastRunnerResult.stderr}\n${lastRunnerResult.stdout}`);
  throw error;
} finally {
  for (const child of active) stop(child, 'SIGKILL');
  if (upstream?.listening) await new Promise(resolve => { upstream.close(resolve); upstream.closeAllConnections(); });
  process.removeListener('SIGINT', onInterrupt);
  process.removeListener('SIGTERM', onTerminate);
  clearTimeout(interruptDeadline);
  await fs.rm(scratch, { recursive: true, force: true });
}
