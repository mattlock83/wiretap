// Install the packed SDK in a clean project and exercise real Jest lifecycles.
// Intentional failures must retain their framework outcomes and still export.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs/promises';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createArazzoValidator, assertCapturedArazzo } from '../tests/arazzo-compat/arazzo-validation.cjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
assert.ok(process.argv.length === 2 || (process.argv.length === 4 && process.argv[2] === '--binary'),
  'Usage: node scripts/runner-smoke.mjs [--binary path]');
const scratch = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-runner-smoke-'));
const consumer = path.join(scratch, 'external project');
const active = new Set();
let interrupted, lastRunnerResult, interruptDeadline;

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
import { withWiretap, captureJest } from '@wiretap/sdk/jest';
import '@wiretap/sdk/tracing';
import type { Config as JestConfig } from 'jest';

const app: RequestListener = (_request, response) => { response.end('ok'); };
const existing: JestConfig = { testEnvironment: 'node', reporters: ['default'] };
const configured: JestConfig = withWiretap(existing, {
  binary: 'wiretap', config: '.wiretap.yaml', outputDir: 'artifacts',
  app: '<rootDir>/app.cjs',
  capture: { schemaRequired: false },
  outputs: { coverage: ['json', 'text', 'html'], arazzo: true },
});
void configured;

function customJestFixture(): void {
  captureJest({ schemaRequired: false, app, onSession(session) { const id: string = session.sessionId; void id; } });
}
void customJestFixture;
`;

try {
  const binary = process.argv[3] ? path.resolve(process.argv[3]) : path.join(scratch, process.platform === 'win32' ? 'wiretap.exe' : 'wiretap');
  if (!process.argv[3]) await mustRun('go', ['build', '-mod=readonly', '-trimpath', '-o', binary, './cmd/wiretap']);
  const packed = JSON.parse((await mustRun('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', scratch], { cwd: path.join(root, 'sdk/js') })).stdout)[0];
  const contents = new Set(packed.files.map(file => file.path));
  for (const filename of ['jest.js', 'jest.d.ts', 'src/jest-app-setup.js', 'docs/jest.md']) {
    assert.ok(contents.has(filename), `packed SDK omitted ${filename}`);
  }
  await fs.mkdir(consumer);
  await fs.writeFile(path.join(consumer, 'package.json'), JSON.stringify({ name: 'wiretap-runner-consumer', version: '1.0.0', private: true }));
  await mustRun('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', path.join(scratch, packed.filename),
    'jest@30.5.2', 'ajv@8.20.0', 'typescript@7.0.2', '@types/node@26.6.5'],
  { cwd: consumer });
  const require = createRequire(path.join(consumer, 'package.json'));
  const metadata = require('@wiretap/sdk/package.json');
  for (const removed of ['./mocha', './playwright', './managed-runtime']) {
    assert.equal(metadata.exports[removed], undefined, `removed integration still exported: ${removed}`);
  }
  assert.ok([...contents].every(filename => !/mocha|playwright|node-test/.test(filename)),
    'packed SDK retains a removed runner integration');
  const installation = await fs.realpath(path.join(consumer, 'node_modules'));
  for (const module of ['@wiretap/sdk/jest', 'jest', 'ajv']) {
    // Resolve junctions and compare path components; Windows paths can differ
    // in drive-letter or directory casing while identifying the same install.
    const resolved = await fs.realpath(require.resolve(module));
    const relative = path.relative(installation, resolved);
    assert.ok(relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative),
      `${module} escaped the clean installation: ${resolved}`);
  }
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
  console.log('Clean CommonJS/ESM Jest TypeScript consumers passed strict NodeNext checks.');
  const validateArazzo = createArazzoValidator(require('ajv/dist/2020'));
  const runner = 'jest';
  const directory = path.join(consumer, runner);
  await fs.mkdir(directory);
  const write = (filename, text) => fs.writeFile(path.join(directory, filename), text);
  await Promise.all([write('app.cjs', application), write('openapi.yaml', contract), write('.wiretap.yaml', projectConfig)]);
  const env = { ...process.env, WIRETAP_BINARY: binary };
  delete env.NODE_TEST_CONTEXT;
  await Promise.all([
    write('jest.config.cjs', `module.exports = require('@wiretap/sdk/jest').withWiretap({
      testMatch: ['**/outcomes.spec.cjs'], transform: {}, globalSetup: './setup.cjs', globalTeardown: './teardown.cjs'
    }, { app: '<rootDir>/app.cjs', outputs: { coverage: ['json', 'text', 'html'], arazzo: true } });\n`),
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
  const args = [require.resolve('jest/bin/jest'), '--config', 'jest.config.cjs', '--runInBand', '--json', '--outputFile=runner-results.json'];
  const result = await execute(process.execPath, args, { cwd: directory, env });
  lastRunnerResult = { runner, ...result };
  assert.equal(result.code, 1, `${runner} changed the expected failure exit:\n${result.stderr}\n${result.stdout}`);
  const report = await readJSON(path.join(directory, 'runner-results.json'));
  assert.equal(report.numPassedTests, 2);
  assert.equal(report.numFailedTests, 1);
  const markers = (await fs.readFile(path.join(directory, 'hooks.jsonl'), 'utf8')).trim().split('\n').map(JSON.parse);
  assert.equal(markers[0], 'setup');
  assert.ok(markers.includes('teardown'), `${runner} lost the user's teardown`);
  const latest = await readJSON(path.join(directory, 'artifacts', 'last-run.json'));
  const receipt = await readJSON(path.join(latest.bundle_path, 'last-run.json'));
  assert.deepEqual(receipt, latest, 'the latest snapshot must reference an archive-local receipt');
  await fs.access(path.join(receipt.bundle_path, 'wiretap.log'));
  assert.equal(receipt.status, 'complete', `${runner}: ${JSON.stringify(receipt)}`);
  assert.deepEqual(receipt.outputs.errors, []);
  const summary = [`Wiretap archive: ${receipt.bundle_path}`,
    ...Object.entries(receipt.outputs.coverage).map(([format, filename]) => `Wiretap coverage (${format}): ${filename}`),
    `Wiretap Arazzo: ${receipt.outputs.arazzo}`];
  for (const line of summary) {
    assert.equal(result.stderr.split(line).length - 1, 1, `${runner}: missing or repeated completion path: ${line}`);
    assert.ok(!result.stdout.includes(line), `${runner}: completion summary polluted reporter stdout`);
  }
  const bundle = await readJSON(path.join(receipt.bundle_path, 'bundle.json'));
  assert.equal(bundle.run_state, 'complete');
  const attempts = await Promise.all(bundle.tests.map(entry => readJSON(path.join(receipt.bundle_path, entry.path))));
  const cases = attempts.map(attempt => attempt.capture.testcase);
  const named = name => cases.filter(testcase => testcase.name.endsWith(name));
  assert.equal(cases.length, 5, `${runner}: ${JSON.stringify(cases)}`);
  assert.deepEqual(named('passes').map(testcase => testcase.status), ['pass']);
  assert.deepEqual(named('retries').map(testcase => testcase.status).sort(), ['fail', 'pass']);
  assert.equal(new Set(named('retries').map(testcase => testcase.identity.id)).size, 1);
  assert.equal(new Set(cases.map(testcase => testcase.identity.id)).size, 3);
  assert.ok(named('cleanup failure').every(testcase => testcase.status === 'fail'), `${runner} lost the cleanup outcome`);
  assert.match(await fs.readFile(receipt.outputs.coverage.text, 'utf8'), /smoke-api: 1\/1 operations exercised/);
  assert.ok(await readJSON(receipt.outputs.coverage.json));
  assert.equal(path.basename(receipt.outputs.coverage.html), 'coverage.html');
  const dashboard = await fs.readFile(receipt.outputs.coverage.html, 'utf8');
  assert.match(dashboard, /<title>[^<]*Wiretap/);
  assert.match(dashboard, /data-wiretap-report="coverage"/);
  assert.ok(dashboard.includes('Contract coverage') && dashboard.includes('Test runs'), 'HTML omitted coverage or per-test navigation');
  const document = validateArazzo(await readJSON(receipt.outputs.arazzo));
  const requests = assertCapturedArazzo(document, attempts, receipt.bundle_path, receipt.outputs.arazzo);
  assert.ok(receipt.outputs.arazzo_files.length > 0);
  assert.ok(requests >= cases.length * 3, `${runner}: missing setup, body or cleanup traffic (${requests} requests)`);
  console.log(`${runner}: clean package, existing hooks, retry, cleanup failure, ${cases.length} attempts, HTML/JSON coverage and ${requests} Arazzo requests verified`);
  console.log('Jest packed SDK smoke passed; no packages published.');
} catch (error) {
  if (lastRunnerResult) console.error(`${lastRunnerResult.runner} output:\n${lastRunnerResult.stderr}\n${lastRunnerResult.stdout}`);
  throw error;
} finally {
  for (const child of active) stop(child, 'SIGKILL');
  process.removeListener('SIGINT', onInterrupt);
  process.removeListener('SIGTERM', onTerminate);
  clearTimeout(interruptDeadline);
  await fs.rm(scratch, { recursive: true, force: true });
}
