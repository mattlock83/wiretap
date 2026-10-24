// The parent owns application startup and the shared runtime; workers own attempts.
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { startManagedRuntime } from '@wiretap/sdk/managed-runtime';
import { createService } from './service.mjs';

const project = path.dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
if (args.length && (args.length !== 2 || args[0] !== '--binary')) {
  throw new Error('Usage: node run-tests.mjs [--binary /absolute/path/to/wiretap]');
}
const binary = args[1] ? path.resolve(args[1]) : process.env.WIRETAP_BINARY || 'wiretap';
const artifacts = path.resolve(process.env.WIRETAP_OUTPUT_DIR || path.join(project, 'artifacts'));
const previousSUT = process.env.SUT_BASE_URL;
let service, owner, tests, testExitCode = 0;

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
  process.env.SUT_BASE_URL = `http://127.0.0.1:${service.address().port}`;
  owner = await startManagedRuntime({ binary, cwd: project, outputDir: artifacts });
  tests = spawn(process.execPath, ['--require', '@wiretap/sdk/tracing', '--test', '--test-concurrency=1',
    'test/orders.test.cjs', 'test/invalid-order.test.cjs'], {
    cwd: project, stdio: 'inherit', env: { ...process.env, WIRETAP_BASE_URL: owner.baseUrl }
  });
  const [code, signal] = await once(tests, 'close');
  testExitCode = code ?? (signal === 'SIGINT' ? 130 : 1);
} catch (error) {
  console.error(error);
  if (!testExitCode) testExitCode = 1;
} finally {
  // Keep application fixtures alive until every attempt and export is finished.
  // A cleanup failure must not replace the test process's original nonzero code.
  await cleanup('Stopping test workers failed', () => stop(tests));
  if (owner) await cleanup('Wiretap finalization failed', async () => {
    const receipt = await owner.close();
    console.log(`Receipt: ${path.join(artifacts, 'last-run.json')}`);
    console.log(`Raw capture: ${receipt.bundle_path}`);
    for (const collection of receipt.outputs?.postman || []) console.log(`Postman collection: ${collection}`);
    if (receipt.outputs?.coverage?.text) console.log(await fs.readFile(receipt.outputs.coverage.text, 'utf8'));
  });
  if (service) await cleanup('Stopping the service failed', async () => {
    service.closeAllConnections();
    await new Promise(resolve => service.close(resolve));
  });
  if (previousSUT === undefined) delete process.env.SUT_BASE_URL;
  else process.env.SUT_BASE_URL = previousSUT;
}
process.exitCode = testExitCode;
