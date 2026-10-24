// Reproduce adoption against a pinned, independently maintained test suite.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '../..');
const upstream = 'https://github.com/ZiadAlMoqamar/Property-Management-API-Testing-Using-Jest.git';
const revision = '77d7d017b948c3456889f199ab7d05070b33bd4d';
assert.ok(process.argv.length <= 3, 'Usage: node examples/existing-jest/run.mjs [new-output-directory]');
const directory = process.argv[2] ? path.resolve(process.argv[2]) : await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-existing-jest-'));
await fs.mkdir(directory, { recursive: true });
const project = path.join(directory, 'demo');
const binary = path.join(directory, process.platform === 'win32' ? 'wiretap.exe' : 'wiretap');
console.log(`Demo and retained outputs: ${directory}`);

async function run(command, args, { cwd = root, env = process.env, log, allowFailure = false } = {}) {
  if (process.platform === 'win32' && command === 'npm') {
    args = [process.env.npm_execpath || path.join(path.dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'), ...args];
    command = process.execPath;
  }
  const child = spawn(command, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
  let stdout = '', stderr = '';
  child.stdout.on('data', chunk => { stdout += chunk; });
  child.stderr.on('data', chunk => { stderr += chunk; });
  const timer = setTimeout(() => child.kill('SIGKILL'), 300000);
  const code = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('close', (code, signal) => resolve(signal ? -1 : code));
  }).finally(() => clearTimeout(timer));
  if (log) await fs.writeFile(path.join(directory, log), stdout + stderr);
  if (!allowFailure) assert.equal(code, 0, `${command} failed: ${stderr}\n${stdout}`);
  return { code, stdout };
}
const readJSON = async file => JSON.parse(await fs.readFile(file, 'utf8'));
await run('git', ['clone', '--quiet', upstream, project]);
await run('git', ['checkout', '--quiet', '--detach', revision], { cwd: project });
await run('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund'], { cwd: project, log: 'install.log' });
const baselinePath = path.join(directory, 'baseline.json');
const baseline = await run(process.execPath, ['node_modules/jest/bin/jest.js', '--runInBand', '--json', `--outputFile=${baselinePath}`],
  { cwd: project, log: 'baseline.log', allowFailure: true });
const before = await readJSON(baselinePath);
assert.equal(before.numTotalTests, 120);
assert.equal(before.numPassedTests, 63);
assert.equal(before.numFailedTests, 57);
assert.equal(baseline.code, 1, 'the pinned upstream has known failing assertions');

await run('go', ['build', '-mod=readonly', '-o', binary, './cmd/wiretap']);
const packed = JSON.parse((await run('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', directory], { cwd: path.join(root, 'sdk/js') })).stdout)[0];
await run('npm', ['install', '--save-dev', '--ignore-scripts', '--no-audit', '--no-fund', path.join(directory, packed.filename), 'postman-collection@5.3.1'],
  { cwd: project, log: 'sdk-install.log' });
await fs.cp(path.join(here, 'overlay'), project, { recursive: true });
const clientPath = path.join(project, 'utils/apiUtility.js');
const client = await fs.readFile(clientPath, 'utf8');
assert.ok(client.includes('const api = supertest(app);'));
await fs.writeFile(clientPath, client.replace('const api = supertest(app);', 'const api = supertest(process.env.WIRETAP_SUT_URL || app);'));
const capturedPath = path.join(directory, 'captured.json');
const captured = await run(process.execPath, ['node_modules/jest/bin/jest.js', '--config', 'jest.wiretap.config.cjs', '--runInBand', '--json', `--outputFile=${capturedPath}`],
  { cwd: project, env: { ...process.env, WIRETAP_BINARY: binary }, log: 'captured.log', allowFailure: true });
const after = await readJSON(capturedPath);
// Keep duplicate names as separate rows; the upstream has two identical names.
const outcomes = result => result.testResults.flatMap(file => file.assertionResults.map(test =>
  JSON.stringify([path.relative(project, file.name), test.fullName, test.status]))).sort();
assert.deepEqual(outcomes(after), outcomes(before), 'capture changed existing test outcomes');
assert.equal(captured.code, baseline.code, 'capture changed the Jest exit code');
await run('git', ['diff', '--exit-code', '--', 'tests/'], { cwd: project });

const receipt = await readJSON(path.join(project, 'artifacts/wiretap/last-run.json'));
assert.equal(receipt.status, 'complete');
assert.deepEqual(receipt.outputs.errors, []);
const bundle = await readJSON(path.join(receipt.bundle_path, 'bundle.json'));
assert.equal(bundle.tests.length, 120);
const attempts = await Promise.all(bundle.tests.map(test => readJSON(path.join(receipt.bundle_path, test.path))));
assert.equal(attempts.filter(test => test.capture.testcase.status === 'pass').length, 63);
assert.equal(attempts.filter(test => test.capture.testcase.status === 'fail').length, 57);
assert.equal(new Set(attempts.map(test => test.capture.testcase.identity.id)).size, 120, 'distinct tests need distinct identities');
const summary = await fs.readFile(receipt.outputs.coverage.text, 'utf8');
assert.match(summary, /property-api: 10\/10 operations exercised/);
const require = createRequire(path.join(project, 'package.json'));
const { Collection } = require('postman-collection');
let requestCount = 0;
for (const file of receipt.outputs.postman) {
  const original = await readJSON(file);
  const collection = new Collection(original);
  for (const folder of original.item) {
    const importedFolder = collection.items.one(folder.id);
    assert.equal(importedFolder.items.count(), folder.item.length);
    for (const [index, item] of folder.item.entries()) {
      const imported = importedFolder.items.idx(index);
      assert.equal(imported.request.method, item.request.method);
      assert.equal(imported.request.url.toString(), item.request.url);
      assert.deepEqual(imported.request.body?.toJSON(), item.request.body);
      assert.deepEqual(imported.responses.all().map(response => [response.code, response.body]), item.response.map(response => [response.code, response.body]));
      requestCount++;
    }
  }
}
assert.ok(requestCount >= 120);
console.log(`Unchanged: 120 tests, 63 passed, 57 upstream failures. All 10 API operations captured.`);
console.log(`Official Postman SDK parsed ${requestCount} requests with request/response data preserved.`);
console.log(`Receipt: ${path.join(project, 'artifacts/wiretap/last-run.json')}`);
console.log(summary);
