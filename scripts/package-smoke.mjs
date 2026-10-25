// Exercise the packed SDK from a clean external project. No package is published.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { readFileSync } from 'node:fs';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const scratch = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-package-smoke-'));
const npm = 'npm';

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

function processExists(pid) {
  if (!Number.isSafeInteger(pid) || pid < 1) return false;
  try {
    process.kill(pid, 0);
    if (process.platform === 'linux') {
      // An orphan can be exited but briefly remain as an unreaped zombie.
      // Treat it as stopped; PID existence alone would falsely report a leak.
      try {
        const status = readFileSync(`/proc/${pid}/stat`, 'utf8');
        if (['Z', 'X'].includes(status[status.lastIndexOf(')') + 2])) return false;
      } catch (error) {
        if (error.code === 'ENOENT') return false;
        throw error;
      }
    }
    return true;
  }
  catch (error) {
    if (error.code === 'ESRCH') return false;
    if (error.code === 'EPERM') return true;
    throw error;
  }
}

async function verifyParentDeath(consumer, binary) {
  const parentFile = path.join(consumer, 'managed-parent.cjs');
  await fs.writeFile(path.join(consumer, 'parent-death.yaml'), `version: 1
output_dir: artifacts-parent-death
sut:
  id: orders
  kind: service
  schema: orders.openapi.yaml
`);
  await fs.writeFile(parentFile, `
const childProcess = require('node:child_process');
const path = require('node:path');
const originalSpawn = childProcess.spawn;
// Observe the real owned process without changing how the SDK launches it.
childProcess.spawn = function (...args) {
  const child = Reflect.apply(originalSpawn, this, args);
  if (args[0] === process.env.WIRETAP_SMOKE_BINARY) {
    process.send({ kind: 'spawned', pid: child.pid,
      readyDirectory: path.dirname(args[2].env.WIRETAP_READY_FILE) });
  }
  return child;
};
const { startManagedRuntime } = require('@wiretap/sdk/managed-runtime');
startManagedRuntime({ binary: process.env.WIRETAP_SMOKE_BINARY,
  config: 'parent-death.yaml', outputDir: path.resolve('artifacts-parent-death'),
  startupTimeoutMs: 20000 }).then(owner => {
  process.send({ kind: 'ready', bundlePath: owner.runtimeInfo.bundle_path });
  setInterval(() => {}, 1000);
}).catch(error => { console.error(error); process.exitCode = 1; });
`);

  let parent, ownedPID, readyDirectory, diagnostic = '', interrupted, readyTimer;
  let rejectReady;
  const killOwned = () => {
    if (parent && parent.exitCode === null && parent.signalCode === null) parent.kill('SIGKILL');
    if (processExists(ownedPID)) {
      try { process.kill(ownedPID, 'SIGKILL'); }
      catch (error) { if (error.code !== 'ESRCH') throw error; }
    }
  };
  const onInterrupt = signal => {
    interrupted = new Error(`Parent-death check interrupted by ${signal}`);
    killOwned();
    rejectReady?.(interrupted);
  };
  const onSIGINT = () => onInterrupt('SIGINT');
  const onSIGTERM = () => onInterrupt('SIGTERM');
  process.once('SIGINT', onSIGINT);
  process.once('SIGTERM', onSIGTERM);
  let closed;
  try {
    parent = spawn(process.execPath, [parentFile], { cwd: consumer,
      env: { ...process.env, WIRETAP_SMOKE_BINARY: binary },
      stdio: ['ignore', 'pipe', 'pipe', 'ipc'], windowsHide: true });
    closed = once(parent, 'close').catch(() => [null, 'spawn-error']);
    parent.stdout.on('data', chunk => { diagnostic = (diagnostic + chunk).slice(-65536); });
    parent.stderr.on('data', chunk => { diagnostic = (diagnostic + chunk).slice(-65536); });
    const ready = new Promise((resolve, reject) => {
      rejectReady = reject;
      readyTimer = setTimeout(() => reject(new Error(`Managed parent did not become ready:\n${diagnostic}`)), 30000);
      parent.once('error', reject);
      parent.once('exit', (code, signal) => reject(new Error(`Managed parent exited before readiness (${signal || code}):\n${diagnostic}`)));
      parent.on('message', message => {
        if (message.kind === 'spawned') {
          ownedPID = message.pid;
          readyDirectory = message.readyDirectory;
        } else if (message.kind === 'ready') resolve(message);
      });
    });
    const { bundlePath } = await ready;
    clearTimeout(readyTimer);
    assert.ok(processExists(ownedPID), 'managed runtime must be alive before parent termination');
    const manifestPath = path.join(bundlePath, 'bundle.json');
    assert.equal(JSON.parse(await fs.readFile(manifestPath, 'utf8')).run_state, 'open');

    // Terminate only the JavaScript owner. Group/tree killing would hide a
    // missing runtime lifeline. Do not probe HTTP: a request can trigger SIGPIPE
    // in an orphan whose parent previously consumed its stdout/stderr pipes.
    parent.kill('SIGKILL');
    let parentTimer;
    try {
      await Promise.race([closed, new Promise((_resolve, reject) => {
        parentTimer = setTimeout(() => reject(new Error('Managed parent did not exit after SIGKILL')), 5000);
      })]);
    } finally { clearTimeout(parentTimer); }

    const deadline = Date.now() + 15000;
    let state, running;
    while (Date.now() < deadline) {
      if (interrupted) throw interrupted;
      state = JSON.parse(await fs.readFile(manifestPath, 'utf8')).run_state;
      running = processExists(ownedPID);
      if (!running && state === 'interrupted') break;
      await delay(100);
    }
    assert.equal(running, false, `Wiretap survived its JavaScript parent (pid ${ownedPID}, archive ${state})`);
    assert.equal(state, 'interrupted', 'parent death must retain an interrupted archive');
    console.log('Packed SDK: killing only the JavaScript parent stops its runtime and preserves an interrupted archive.');
  } finally {
    clearTimeout(readyTimer);
    killOwned();
    if (closed) await Promise.race([closed, delay(5000, undefined, { ref: false })]);
    const cleanupDeadline = Date.now() + 5000;
    while (processExists(ownedPID) && Date.now() < cleanupDeadline) await delay(50);
    process.removeListener('SIGINT', onSIGINT);
    process.removeListener('SIGTERM', onSIGTERM);
    if (readyDirectory) await fs.rm(readyDirectory, { recursive: true, force: true });
  }
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
  for (const required of ['LICENSE', 'src/index.js', 'src/index.d.ts', 'src/node-test.js', 'tracing.js', 'tracing.d.ts', 'jest.js', 'jest.d.ts', 'jest-environment.js', 'src/managed-runtime.js', 'src/managed-runtime.d.ts', 'src/jest-config.js', 'src/jest-config.d.ts', 'src/jest-global-setup.js', 'src/jest-global-teardown.js']) {
    assert.ok(packedPaths.has(required), `packed SDK omitted ${required}`);
  }
  assert.ok([...packedPaths].every(name => !name.startsWith('test/') && !name.includes('node_modules/') && !name.endsWith('package-lock.json')), 'packed SDK includes development files');

  const consumer = path.join(scratch, 'external-project');
  // Test the same complete harness users copy, including two separate workers.
  await fs.cp(path.join(root, 'examples/node-suite'), consumer, { recursive: true,
    filter: source => !['node_modules', 'artifacts', 'package-lock.json'].includes(path.basename(source)) });
  await mustRun(npm, ['install', '--ignore-scripts', '--omit=dev', '--no-audit', '--no-fund', path.join(scratch, packed.filename)], { cwd: consumer });
  await mustRun(process.execPath, ['-e', `
    const assert = require('node:assert/strict');
    assert.equal(typeof require('@wiretap/sdk').startCapture, 'function');
    assert.equal(typeof require('@wiretap/sdk/jest').createJestSuite, 'function');
    assert.equal(typeof require('@wiretap/sdk/jest').withWiretap, 'function');
    assert.equal(typeof require('@wiretap/sdk/jest').captureJest, 'function');
    assert.equal(typeof require('@wiretap/sdk/managed-runtime').startManagedRuntime, 'function');
    assert.ok(require.resolve('@wiretap/sdk/jest-environment'));
    assert.equal(require('@wiretap/sdk/package.json').license, 'MIT');
  `], { cwd: consumer });

  for (const failure of [false, true]) {
    const artifactRoot = path.join(consumer, failure ? 'artifacts-failed' : 'artifacts-passed');
    const result = await execute(process.execPath, ['run-tests.mjs', '--binary', binary], {
      cwd: consumer,
      env: { ...process.env, WIRETAP_OUTPUT_DIR: artifactRoot, WIRETAP_EXAMPLE_FAIL: failure ? '1' : '0' }
    });
    assert.equal(result.code, failure ? 1 : 0, `test exit status changed: ${result.stdout}\n${result.stderr}`);
    const latest = JSON.parse(await fs.readFile(path.join(artifactRoot, 'last-run.json'), 'utf8'));
    const receipt = JSON.parse(await fs.readFile(path.join(latest.bundle_path, 'last-run.json'), 'utf8'));
    assert.deepEqual(receipt, latest, 'the latest snapshot must reference an archive-local receipt');
    await fs.access(path.join(receipt.bundle_path, 'wiretap.log'));
    assert.equal(receipt.status, 'complete');
    assert.deepEqual(receipt.outputs.errors, []);
    assert.equal(receipt.outputs.postman.length, 1);
    const run = JSON.parse(await fs.readFile(path.join(receipt.bundle_path, 'bundle.json'), 'utf8'));
    assert.equal(run.run_state, 'complete');
    assert.equal(run.tests.length, 2, 'both test files must share one finalized run');
    const recordings = await Promise.all(run.tests.map(async test =>
      JSON.parse(await fs.readFile(path.join(receipt.bundle_path, test.path), 'utf8'))));
    const positive = recordings.find(recording => recording.capture.testcase.name === 'gets an order');
    const negative = recordings.find(recording => recording.capture.testcase.name === 'rejects an invalid order ID');
    assert.equal(positive.capture.testcase.status, failure ? 'fail' : 'pass', 'framework cleanup outcome was lost');
    assert.equal(negative.capture.testcase.status, 'pass', 'deliberate negative request changed framework outcome');
    assert.equal(negative.capture.schema_validation.status, 'fail', 'negative-test validation evidence was lost');
    const coverage = JSON.parse(await fs.readFile(receipt.outputs.coverage.json, 'utf8'));
    assert.ok(coverage);
    assert.match(await fs.readFile(receipt.outputs.coverage.text, 'utf8'), /Attempts: 2/);
    const collection = JSON.parse(await fs.readFile(receipt.outputs.postman[0], 'utf8'));
    assert.equal(collection.info.schema, 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json');
  }
  await verifyParentDeath(consumer, binary);
  console.log(`Packed SDK and ${producer.name} ${producer.version}: external two-file suite, negative test, cleanup failure, coverage and Postman smoke passed`);
} finally {
  await fs.rm(scratch, { recursive: true, force: true });
}
