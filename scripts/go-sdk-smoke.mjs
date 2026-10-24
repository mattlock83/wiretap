// Consume the Go SDK from an external module and exercise the actual Go runner.
// Intentional failures and hard aborts must not leak an owned Wiretap process.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs/promises';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
assert.ok(process.argv.length === 2 || (process.argv.length === 4 && process.argv[2] === '--binary'),
  'Usage: node scripts/go-sdk-smoke.mjs [--binary path]');
const scratch = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-go-smoke-'));
const consumer = path.join(scratch, 'external Go project');
const moduleName = 'example.com/wiretap-go-smoke';
const active = new Set();
let interrupted, interruptDeadline, lastResult;

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
function interrupt(signal) {
  interrupted = signal;
  for (const child of active) { child.smokeCancelled = true; stop(child); }
  interruptDeadline ??= setTimeout(() => { for (const child of active) stop(child, 'SIGKILL'); }, 5000);
}
const onInterrupt = () => interrupt('SIGINT');
const onTerminate = () => interrupt('SIGTERM');
process.once('SIGINT', onInterrupt);
process.once('SIGTERM', onTerminate);

async function execute(command, args, options = {}) {
  assert.equal(interrupted, undefined, `Go smoke interrupted by ${interrupted}`);
  const { timeoutMs = 300000, ...spawnOptions } = options;
  const child = spawn(command, args, { cwd: root, env: process.env,
    detached: process.platform !== 'win32', ...spawnOptions });
  active.add(child);
  let stdout = '', stderr = '', forced;
  child.stdout.on('data', chunk => { stdout += chunk; });
  child.stderr.on('data', chunk => { stderr += chunk; });
  const timer = setTimeout(() => {
    child.smokeCancelled = true;
    stop(child);
    forced = setTimeout(() => stop(child, 'SIGKILL'), 5000);
  }, timeoutMs);
  try {
    const [code, signal] = await once(child, 'close');
    lastResult = { code, signal, stdout, stderr, child };
    assert.ok(!child.smokeCancelled, `${command} timed out or was interrupted:\n${stderr}\n${stdout}`);
    return lastResult;
  } finally {
    clearTimeout(timer);
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
const normalizePath = filename => process.platform === 'win32'
  ? path.resolve(filename).toLowerCase() : path.resolve(filename);
async function filesNamed(directory, name) {
  let entries;
  try { entries = await fs.readdir(directory, { withFileTypes: true }); }
  catch (error) { if (error.code === 'ENOENT') return []; throw error; }
  const nested = await Promise.all(entries.map(entry => entry.isDirectory()
    ? filesNamed(path.join(directory, entry.name), name)
    : entry.name === name ? [path.join(directory, entry.name)] : []));
  return nested.flat();
}
function requestsIn(items) {
  return items.flatMap(item => item.request ? [item] : requestsIn(item.item || []));
}
async function runsIn(directory) {
  const receipts = await filesNamed(directory, 'last-run.json');
  return Promise.all(receipts.map(async filename => {
    const receipt = await readJSON(filename);
    assert.equal(receipt.status, 'complete');
    assert.equal(normalizePath(await fs.realpath(path.dirname(filename))), normalizePath(await fs.realpath(receipt.bundle_path)),
      'each package must keep its receipt inside its own archive');
    await fs.access(path.join(receipt.bundle_path, 'wiretap.log'));
    assert.deepEqual(receipt.outputs.errors, []);
    const bundle = await readJSON(path.join(receipt.bundle_path, 'bundle.json'));
    assert.equal(bundle.run_state, 'complete');
    const recordings = await Promise.all((bundle.tests || []).map(entry => readJSON(path.join(receipt.bundle_path, entry.path))));
    const coverage = await readJSON(receipt.outputs.coverage.json);
    assert.equal(coverage.artifact_count, recordings.length);
    const collections = await Promise.all((receipt.outputs.postman || []).map(readJSON));
    for (const collection of collections) {
      assert.equal(collection.info.schema, 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json');
      for (const item of requestsIn(collection.item)) {
        assert.equal(item.request.method, 'GET');
        assert.match(item.request.url, /\/orders\//);
        assert.ok(item.response.length > 0, 'Postman request lost its observed response');
      }
    }
    return { receipt, bundle, recordings, coverage, requests: collections.flatMap(collection => requestsIn(collection.item)) };
  }));
}
function checkIdentity(run, count = 1) {
  const grouped = new Map();
  for (const { capture } of run.recordings) {
    const testcase = capture.testcase;
    assert.ok(testcase.identity.id.includes(`${moduleName}/`), `identity lacks package: ${testcase.identity.id}`);
    assert.ok(testcase.identity.id.includes(testcase.name), `identity lacks test name: ${testcase.identity.id}`);
    const attempts = grouped.get(testcase.identity.id) || [];
    attempts.push(testcase.identity.attempt_ordinal);
    grouped.set(testcase.identity.id, attempts);
  }
  for (const ordinals of grouped.values()) {
    assert.deepEqual(ordinals.sort((a, b) => a - b), Array.from({ length: count }, (_unused, index) => index + 1));
  }
  return [...grouped.keys()];
}
function portIsOpen(address) {
  const url = new URL(address);
  return new Promise(resolve => {
    const socket = net.connect({ host: url.hostname, port: Number(url.port) });
    socket.setTimeout(300);
    let done = false;
    const finish = open => { if (!done) { done = true; socket.destroy(); resolve(open); } };
    socket.once('connect', () => finish(true));
    socket.once('error', () => finish(false));
    socket.once('timeout', () => finish(false));
  });
}
async function waitForShutdown(marker) {
  const deadline = Date.now() + 15000;
  let state, proxyOpen;
  while (Date.now() < deadline) {
    const manifest = await readJSON(path.join(marker.bundle_path, 'bundle.json'));
    state = manifest.run_state;
    proxyOpen = await portIsOpen(marker.sut_url);
    if (state === 'interrupted' && !proxyOpen) return;
    await delay(100);
  }
  assert.fail(`owned Wiretap shutdown failed: archive state=${state}, proxy open=${proxyOpen}, archive=${marker.bundle_path}`);
}

const smokeMain = `package orders
import (
  "os"
  "testing"
  wiretap "github.com/mattlock83/wiretap/sdk/go"
)
func TestMain(m *testing.M) {
  options := wiretap.RunOptions{Handler: NewHandler()}
  if os.Getenv("WIRETAP_GO_SMOKE_OUTPUTS") == "json" {
    options.Outputs = &wiretap.OutputOptions{Coverage: []string{"json"}}
  }
  os.Exit(wiretap.Run(m, options))
}
`;
const extraTests = `package orders
import (
  "encoding/json"
  "os"
  "testing"
  "time"
  wiretap "github.com/mattlock83/wiretap/sdk/go"
)
func smokeSession(t *testing.T, mode string) *wiretap.Session {
  t.Helper()
  if os.Getenv("WIRETAP_GO_SMOKE_MODE") != mode { t.Skip("separate acceptance scenario") }
  session := wiretap.StartTest(t)
  response, err := session.HTTPClient(existingClient).Get(session.SUTURL + "/orders/42")
  if err != nil { t.Fatal(err) }
  response.Body.Close()
  return session
}
func TestSmokeCleanup(t *testing.T) {
  session := smokeSession(t, "failures")
  t.Cleanup(func() {
    response, err := session.HTTPClient(existingClient).Get(session.SUTURL + "/orders/99")
    if err != nil { t.Fatal(err) }
    response.Body.Close()
    t.Error("intentional user cleanup failure")
  })
}
func TestSmokeFatal(t *testing.T) {
  smokeSession(t, "failures")
  t.Fatal("intentional fatal assertion")
}
func TestSmokeSkip(t *testing.T) {
  smokeSession(t, "failures")
  t.Skip("intentional dynamic skip")
}
func saveSmokeMarker(t *testing.T, session *wiretap.Session) {
  t.Helper()
  data, err := json.Marshal(map[string]string{"sut_url": session.SUTURL, "bundle_path": session.BundlePath})
  if err != nil { t.Fatal(err) }
  if err := os.WriteFile(os.Getenv("WIRETAP_GO_SMOKE_MARKER"), data, 0600); err != nil { t.Fatal(err) }
}
func TestSmokePanic(t *testing.T) {
  session := smokeSession(t, "panic")
  saveSmokeMarker(t, session)
  panic("intentional parent panic")
}
func TestSmokeTimeout(t *testing.T) {
  session := smokeSession(t, "timeout")
  saveSmokeMarker(t, session)
  time.Sleep(time.Minute)
}
`;

try {
  const binary = process.argv[3] ? path.resolve(process.argv[3]) : path.join(scratch, process.platform === 'win32' ? 'wiretap.exe' : 'wiretap');
  if (!process.argv[3]) await mustRun('go', ['build', '-mod=readonly', '-trimpath', '-o', binary, './cmd/wiretap']);
  await fs.mkdir(consumer);
  for (const name of ['alpha', 'beta']) {
    const directory = path.join(consumer, name);
    await fs.cp(path.join(root, 'examples/go-suite'), directory, { recursive: true,
      filter: source => !['artifacts', 'go.mod', 'go.sum'].includes(path.basename(source)) });
    const config = await fs.readFile(path.join(directory, '.wiretap.yaml'), 'utf8');
    await fs.writeFile(path.join(directory, '.wiretap.yaml'), config.replace('output_dir: artifacts', 'output_dir: ../shared-artifacts'));
    await fs.writeFile(path.join(directory, 'main_test.go'), smokeMain);
    await fs.writeFile(path.join(directory, 'outcomes_test.go'), extraTests);
  }
  await mustRun('go', ['mod', 'init', moduleName], { cwd: consumer });
  await mustRun('go', ['mod', 'edit', '-require=github.com/mattlock83/wiretap@v0.0.0',
    `-replace=github.com/mattlock83/wiretap=${root}`], { cwd: consumer });
  await mustRun('go', ['mod', 'tidy'], { cwd: consumer });
  const env = { ...process.env, WIRETAP_BINARY: binary };
  for (const name of ['WIRETAP_OUTPUT_DIR', 'WIRETAP_BASE_URL', 'WIRETAP_CONFIG',
    'WIRETAP_GO_SMOKE_MODE', 'WIRETAP_GO_SMOKE_OUTPUTS', 'WIRETAP_GO_SMOKE_MARKER']) delete env[name];

  await mustRun('go', ['test', '-count=1', '-p=2', './...'], { cwd: consumer, env });
  const initial = await runsIn(path.join(consumer, 'shared-artifacts'));
  assert.equal(initial.length, 2, 'concurrent packages must produce separate complete archives');
  const identities = [];
  for (const run of initial) {
    assert.equal(run.recordings.length, 2);
    identities.push(...checkIdentity(run));
    const positive = run.recordings.find(record => record.capture.testcase.name === 'TestOrders');
    const negative = run.recordings.find(record => record.capture.testcase.name === 'TestRejectsInvalidID');
    assert.equal(positive.capture.testcase.status, 'pass');
    assert.equal(positive.capture.traffic.length, 2, 'user cleanup traffic was lost');
    assert.equal(negative.capture.testcase.status, 'pass');
    assert.equal(negative.capture.schema_validation.status, 'fail', 'negative validation evidence was lost');
    assert.equal(run.requests.length, 3);
    assert.match(await fs.readFile(run.receipt.outputs.coverage.text, 'utf8'), /orders: 1\/1 operations exercised/);
  }
  assert.equal(new Set(identities).size, 4, 'same test names from different packages collided');
  console.log('Go: clean external module, concurrent packages, existing clients, user cleanup, negative validation, coverage and Postman exports passed.');

  const failuresDir = path.join(consumer, 'failure artifacts');
  const failures = await execute('go', ['test', '-count=1', '-run=^TestSmoke(Cleanup|Fatal|Skip)$', './alpha'],
    { cwd: consumer, env: { ...env, WIRETAP_OUTPUT_DIR: failuresDir, WIRETAP_GO_SMOKE_MODE: 'failures' } });
  assert.equal(failures.code, 1, 'capture changed Go\'s failing test exit status');
  assert.match(failures.stdout, /intentional user cleanup failure/);
  assert.match(failures.stdout, /intentional fatal assertion/);
  const failedRuns = await runsIn(failuresDir);
  assert.equal(failedRuns.length, 1);
  assert.deepEqual(Object.fromEntries(failedRuns[0].recordings.map(record => [record.capture.testcase.name, record.capture.testcase.status])),
    { TestSmokeCleanup: 'fail', TestSmokeFatal: 'fail', TestSmokeSkip: 'skip' });
  assert.equal(failedRuns[0].requests.length, 4);
  checkIdentity(failedRuns[0]);

  const repeatedDir = path.join(consumer, 'repeated artifacts');
  await mustRun('go', ['test', '-count=2', '-run=^TestOrders$', '-p=2', './...'],
    { cwd: consumer, env: { ...env, WIRETAP_OUTPUT_DIR: repeatedDir } });
  const repeated = await runsIn(repeatedDir);
  assert.equal(repeated.length, 2);
  for (const run of repeated) {
    assert.equal(run.recordings.length, 2);
    const [identity] = checkIdentity(run, 2);
    assert.ok(identities.includes(identity), 'test identity changed between fresh runs');
  }

  const emptyDir = path.join(consumer, 'empty artifacts');
  await mustRun('go', ['test', '-count=1', '-run=^NoSuchTest$', '-p=2', './...'],
    { cwd: consumer, env: { ...env, WIRETAP_OUTPUT_DIR: emptyDir } });
  const empty = await runsIn(emptyDir);
  assert.equal(empty.length, 2);
  assert.ok(empty.every(run => run.recordings.length === 0));

  const overridesDir = path.join(consumer, 'override artifacts');
  await mustRun('go', ['test', '-count=1', '-run=^TestOrders$', './alpha'],
    { cwd: consumer, env: { ...env, WIRETAP_OUTPUT_DIR: overridesDir, WIRETAP_GO_SMOKE_OUTPUTS: 'json' } });
  const overrides = await runsIn(overridesDir);
  assert.equal(overrides.length, 1);
  assert.deepEqual(Object.keys(overrides[0].receipt.outputs.coverage), ['json']);
  assert.equal(overrides[0].requests.length, 0);
  console.log('Go: fatal assertions, failed cleanup, dynamic skip, repeated attempt identity, empty suites and explicit output options passed.');

  const testBinary = path.join(consumer, process.platform === 'win32' ? 'alpha.test.exe' : 'alpha.test');
  await mustRun('go', ['test', '-c', '-o', testBinary, './alpha'], { cwd: consumer, env });
  for (const mode of ['panic', 'timeout']) {
    const markerPath = path.join(consumer, `${mode}-marker.json`);
    const result = await execute(testBinary, [`-test.run=^TestSmoke${mode === 'panic' ? 'Panic' : 'Timeout'}$`, '-test.timeout=10s'],
      { cwd: path.join(consumer, 'alpha'), timeoutMs: 30000,
        env: { ...env, WIRETAP_OUTPUT_DIR: path.join(consumer, `${mode} artifacts`),
          WIRETAP_GO_SMOKE_MODE: mode, WIRETAP_GO_SMOKE_MARKER: markerPath } });
    assert.equal(result.code, 2, `${mode} should retain Go's hard-abort exit status`);
    const marker = await readJSON(markerPath);
    try { await waitForShutdown(marker); }
    catch (error) { stop(result.child, 'SIGKILL'); throw error; }
  }
  console.log('Go: parent panic and Go test timeout closed owned proxies and retained interrupted archives. No packages published.');
} catch (error) {
  if (lastResult) console.error(`Last Go command output:\n${lastResult.stderr}\n${lastResult.stdout}`);
  throw error;
} finally {
  for (const child of active) stop(child, 'SIGKILL');
  process.removeListener('SIGINT', onInterrupt);
  process.removeListener('SIGTERM', onTerminate);
  clearTimeout(interruptDeadline);
  await fs.rm(scratch, { recursive: true, force: true });
}
