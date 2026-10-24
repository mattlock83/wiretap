// One harness owns service startup, all workers, finalization and shutdown.
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs/promises';
import net from 'node:net';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { startCapture } from '@wiretap/sdk';
import { createService } from './service.mjs';

const project = path.dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
if (args.length && (args.length !== 2 || args[0] !== '--binary')) {
  throw new Error('Usage: node run-tests.mjs [--binary /absolute/path/to/wiretap]');
}
const binary = args[1] ? path.resolve(args[1]) : process.env.WIRETAP_BINARY || 'wiretap';
const artifacts = path.resolve(process.env.WIRETAP_OUTPUT_DIR || path.join(project, 'artifacts'));
await fs.mkdir(artifacts, { recursive: true });
let service, runtime, owner, tests, spawnError, testExitCode = 0, runtimeLog = '';

async function freePort() {
  const server = net.createServer();
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}

async function waitForHealth(url) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (spawnError) throw spawnError;
    if (runtime?.exitCode !== null && runtime?.exitCode !== undefined) {
      throw new Error(`Wiretap exited before readiness: ${runtimeLog}`);
    }
    try { if ((await fetch(url, { signal: AbortSignal.timeout(250) })).ok) return; } catch {}
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`Service was not ready: ${url}\n${runtimeLog}`);
}

async function stop(child) {
  if (!child?.pid || child.exitCode !== null || child.signalCode !== null) return;
  const closed = once(child, 'close');
  child.kill('SIGTERM');
  const timer = setTimeout(() => child.kill('SIGKILL'), 5000);
  try { await closed; } finally { clearTimeout(timer); }
}

async function cleanup(label, action) {
  try { await action(); }
  catch (error) {
    console.error(`${label}:`, error);
    if (!testExitCode) testExitCode = 1;
  }
}

try {
  service = createService();
  service.listen(0, '127.0.0.1');
  await once(service, 'listening');
  const serviceUrl = `http://127.0.0.1:${service.address().port}`;
  await waitForHealth(`${serviceUrl}/healthz`);
  const controlAddress = `127.0.0.1:${await freePort()}`;
  const baseUrl = `http://${controlAddress}`;
  runtime = spawn(binary, ['--config', '.wiretap.yaml'], {
    cwd: project, stdio: ['ignore', 'ignore', 'pipe'],
    env: { ...process.env, WIRETAP_HTTP_LISTEN: controlAddress,
      WIRETAP_PROXY_LISTEN: '127.0.0.1:0', SUT_BASE_URL: serviceUrl, WIRETAP_OUTPUT_DIR: artifacts }
  });
  // Observe spawn errors before polling readiness, and keep startup diagnostics.
  runtime.on('error', error => { spawnError = error; runtimeLog += error.message; });
  runtime.stderr.on('data', chunk => { runtimeLog = (runtimeLog + chunk).slice(-65536); });
  await waitForHealth(`${baseUrl}/healthz`);
  owner = await startCapture({ baseUrl, instrumentation: false });
  tests = spawn(process.execPath, ['--require', '@wiretap/sdk/tracing', '--test', '--test-concurrency=1',
    'test/orders.test.cjs', 'test/invalid-order.test.cjs'], {
    cwd: project, stdio: 'inherit', env: { ...process.env, WIRETAP_BASE_URL: baseUrl }
  });
  const [code, signal] = await once(tests, 'close');
  testExitCode = code ?? (signal === 'SIGINT' ? 130 : 1);
} catch (error) {
  console.error(error);
  if (!testExitCode) testExitCode = 1;
} finally {
  // A failed test still produces exports. A cleanup failure never replaces the
  // test process's original nonzero exit code.
  let receipt;
  if (owner) {
    try { receipt = await owner.finishRun(); }
    catch (error) {
      receipt = error.body;
      console.error('Wiretap finalization failed:', error.message);
      if (!testExitCode) testExitCode = 1;
    }
  }
  try {
    if (receipt) {
      await fs.writeFile(path.join(artifacts, 'last-run.json'), JSON.stringify(receipt, null, 2) + '\n');
      if (receipt.bundle_path) console.log(`Raw capture: ${receipt.bundle_path}`);
      if (receipt.outputs?.coverage?.text) console.log(`Text summary: ${receipt.outputs.coverage.text}`);
      if (receipt.outputs?.coverage?.json) console.log(`JSON coverage: ${receipt.outputs.coverage.json}`);
      for (const collection of receipt.outputs?.postman || []) console.log(`Postman collection: ${collection}`);
      if (receipt.bundle_path) {
        const summary = spawn(binary, ['report', '--bundle', receipt.bundle_path, '--format', 'text'], { stdio: 'inherit' });
        const [code] = await once(summary, 'close');
        if (code !== 0 && !testExitCode) testExitCode = 1;
      }
    }
  } catch (error) {
    console.error('Saving Wiretap results failed:', error);
    if (!testExitCode) testExitCode = 1;
  }
  await cleanup('Stopping test workers failed', () => stop(tests));
  await cleanup('Stopping Wiretap failed', () => stop(runtime));
  await cleanup('Saving runtime log failed', () => fs.writeFile(path.join(artifacts, 'runtime.log'), runtimeLog));
  if (service) await cleanup('Stopping the service failed', async () => {
    service.closeAllConnections();
    await new Promise(resolve => service.close(resolve));
  });
}
process.exitCode = testExitCode;
