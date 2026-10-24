// Exercise the packed SDK from a clean external project. No package is published.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs/promises';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const scratch = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-package-smoke-'));
const npm = 'npm';
let runtime, service;

async function execute(command, args, options = {}) {
  if (process.platform === 'win32' && command === npm) {
    // Invoke npm's JavaScript entry point directly: .cmd requires shell parsing
    // and would mishandle an external project's paths containing spaces.
    args = [process.env.npm_execpath || path.join(path.dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'), ...args];
    command = process.execPath;
  }
  const child = spawn(command, args, {
    cwd: root, env: process.env, ...options
  });
  let stdout = '', stderr = '';
  child.stdout?.on('data', chunk => { stdout += chunk; });
  child.stderr?.on('data', chunk => { stderr += chunk; });
  const timer = setTimeout(() => child.kill('SIGKILL'), 300_000);
  try {
    const [code, signal] = await once(child, 'close');
    return { code, signal, stdout, stderr };
  } finally { clearTimeout(timer); }
}

async function mustRun(command, args, options) {
  const result = await execute(command, args, options);
  assert.equal(result.code, 0, `${command} ${args.join(' ')} failed (${result.signal || result.code}):\n${result.stderr}\n${result.stdout}`);
  return result;
}

async function reservePort() {
  const listener = net.createServer();
  listener.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  const port = listener.address().port;
  await new Promise(resolve => listener.close(resolve));
  return port;
}

try {
  let binary;
  if (process.argv[2] === '--binary' && process.argv[3] && process.argv.length === 4) {
    binary = path.resolve(process.argv[3]);
  } else {
    assert.equal(process.argv.length, 2, 'Usage: node scripts/package-smoke.mjs [--binary path]');
    binary = path.join(scratch, process.platform === 'win32' ? 'wiretap.exe' : 'wiretap');
    await mustRun('go', ['build', '-mod=readonly', '-trimpath', '-o', binary, './cmd/wiretap']);
  }
  const producer = JSON.parse((await mustRun(binary, ['version', '--json'])).stdout);
  assert.equal(producer.name, 'wiretap');
  assert.ok(producer.version);

  const packed = JSON.parse((await mustRun(npm, ['pack', '--ignore-scripts', '--json', '--pack-destination', scratch], { cwd: path.join(root, 'sdk/js') })).stdout)[0];
  const packedPaths = new Set(packed.files.map(file => file.path));
  for (const required of ['LICENSE', 'src/index.js', 'src/index.d.ts', 'src/node-test.js', 'tracing.js', 'tracing.d.ts', 'jest.js', 'jest.d.ts', 'jest-environment.js']) {
    assert.ok(packedPaths.has(required), `packed SDK omitted ${required}`);
  }
  assert.ok([...packedPaths].every(name => !name.startsWith('test/') && !name.includes('node_modules/') && !name.endsWith('package-lock.json')), 'packed SDK includes development files');

  const consumer = path.join(scratch, 'external-project');
  await fs.mkdir(consumer);
  await fs.writeFile(path.join(consumer, 'package.json'), JSON.stringify({ name: 'wiretap-clean-consumer', private: true }));
  await mustRun(npm, ['install', '--ignore-scripts', '--omit=dev', '--no-audit', '--no-fund', path.join(scratch, packed.filename)], { cwd: consumer });
  await mustRun(process.execPath, ['-e', `
    const assert = require('node:assert/strict');
    assert.equal(typeof require('@wiretap/sdk').startCapture, 'function');
    assert.equal(typeof require('@wiretap/sdk/jest').createJestSuite, 'function');
    assert.ok(require.resolve('@wiretap/sdk/jest-environment'));
    assert.equal(require('@wiretap/sdk/package.json').license, 'MIT');
  `], { cwd: consumer });

  service = http.createServer((_request, response) => {
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end('{"status":"ok"}');
  });
  service.listen(0, '127.0.0.1');
  await once(service, 'listening');
  const controlPort = await reservePort();
  let proxyPort = await reservePort();
  while (proxyPort === controlPort) proxyPort = await reservePort();
  const baseUrl = `http://127.0.0.1:${controlPort}`;
  await fs.writeFile(path.join(consumer, 'openapi.json'), JSON.stringify({
    openapi: '3.0.3', info: { title: 'Package smoke', version: '1' },
    paths: { '/status': { get: { operationId: 'status', responses: { '200': {
      description: 'Healthy', content: { 'application/json': { schema: {
        type: 'object', required: ['status'], properties: { status: { type: 'string', enum: ['ok'] } }
      } } }
    } } } } }
  }));
  await fs.writeFile(path.join(consumer, '.wiretap.yaml'), `version: 1
application:
  id: package-smoke
output_dir: artifacts
outputs:
  coverage: [json]
  postman: true
http_listen: 127.0.0.1:${controlPort}
proxy_listen: 127.0.0.1:${proxyPort}
sut:
  id: status-api
  url: http://127.0.0.1:${service.address().port}
  kind: service
  schema: openapi.json
`);
  await fs.writeFile(path.join(consumer, 'test.cjs'), `
const { test, before, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const { startCapture } = require('@wiretap/sdk');
let capture;
before(async () => { capture = await startCapture(); });
after(async () => {
  if (capture) fs.writeFileSync(process.env.WIRETAP_RESULT_FILE, JSON.stringify(await capture.finishRun()));
});
test('existing API test', () => capture.runTest('existing API test', async session => {
  const response = await fetch(session.sutUrl + '/status');
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { status: 'ok' });
  if (process.env.WIRETAP_SMOKE_FAIL === '1') throw Error('original test failure');
}));
`);
  runtime = spawn(binary, ['--config', '.wiretap.yaml'], { cwd: consumer, stdio: ['ignore', 'ignore', 'pipe'] });
  let runtimeLog = '';
  runtime.stderr.on('data', chunk => { runtimeLog = (runtimeLog + chunk).slice(-65536); });
  let ready = false;
  for (let attempt = 0; attempt < 100; attempt++) {
    if (runtime.exitCode !== null) throw Error(`runtime exited ${runtime.exitCode}: ${runtimeLog}`);
    try { ready = (await fetch(`${baseUrl}/healthz`, { signal: AbortSignal.timeout(500) })).ok; } catch {}
    if (ready) break;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  assert.ok(ready, `runtime did not become ready: ${runtimeLog}`);
  for (const failure of [false, true]) {
    const receiptPath = path.join(consumer, failure ? 'failed-result.json' : 'passed-result.json');
    const result = await execute(process.execPath, ['--require', '@wiretap/sdk/tracing', '--test', 'test.cjs'], {
      cwd: consumer,
      env: { ...process.env, WIRETAP_BASE_URL: baseUrl, WIRETAP_RESULT_FILE: receiptPath, WIRETAP_SMOKE_FAIL: failure ? '1' : '0' }
    });
    assert.equal(result.code, failure ? 1 : 0, `test exit status changed: ${result.stdout}\n${result.stderr}`);
    const receipt = JSON.parse(await fs.readFile(receiptPath, 'utf8'));
    assert.equal(receipt.status, 'complete');
    assert.deepEqual(receipt.outputs.errors, []);
    assert.equal(receipt.outputs.postman.length, 1);
    const run = JSON.parse(await fs.readFile(path.join(receipt.bundle_path, 'bundle.json'), 'utf8'));
    assert.equal(run.run_state, 'complete');
    const recording = JSON.parse(await fs.readFile(path.join(receipt.bundle_path, run.tests[0].path), 'utf8'));
    assert.equal(recording.capture.testcase.status, failure ? 'fail' : 'pass');
    const coverage = JSON.parse(await fs.readFile(receipt.outputs.coverage.json, 'utf8'));
    assert.ok(coverage);
    const collection = JSON.parse(await fs.readFile(receipt.outputs.postman[0], 'utf8'));
    assert.equal(collection.info.schema, 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json');
  }
  console.log(`Packed SDK and ${producer.name} ${producer.version}: external pass/fail capture, coverage and Postman smoke passed`);
} finally {
  if (runtime && runtime.exitCode === null) {
    const stopped = once(runtime, 'exit');
    runtime.kill('SIGTERM');
    const killTimer = setTimeout(() => runtime.kill('SIGKILL'), 5000);
    await stopped;
    clearTimeout(killTimer);
  }
  if (service) {
    service.closeAllConnections();
    await new Promise(resolve => service.close(resolve));
  }
  await fs.rm(scratch, { recursive: true, force: true });
}
