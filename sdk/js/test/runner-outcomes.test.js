const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const execute = promisify(execFile);

test('Jest final outcomes include timeouts, hooks and validation', async t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'wiretap-outcomes-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const resultPath = path.join(dir, 'result.json');
  const args = [require.resolve('jest/bin/jest'), '--runInBand', '--config', JSON.stringify({
    rootDir: path.resolve(__dirname, '..'), testEnvironment: './jest-environment.js',
    testMatch: ['**/test/fixtures/jest-outcomes.cjs'], transform: {}
  })];
  // The child suites intentionally fail. The parent verifies both the runner
  // result and capture lifecycle, without introducing failures in npm test.
  const env = { ...process.env, WIRETAP_REVIEW_RESULT: resultPath };
  delete env.NODE_TEST_CONTEXT;
  let runnerFailure;
  await assert.rejects(execute(process.execPath, args, {
    env, timeout: 20000
  }), error => { runnerFailure = error; return error.code === 1; });
  const result = JSON.parse(fs.readFileSync(resultPath, 'utf8'));
  const setups = result.calls.filter(call => call.path === '/v1/setupTest');
  const teardowns = result.calls.filter(call => call.path === '/v1/teardownTest');
  assert.equal(teardowns.length, setups.length);
  assert.equal(result.open, 0);
  const outcomes = Object.fromEntries(teardowns.map(call => [call.body.session_id, call.body.status]));
  assert.equal(outcomes.pass, 'pass');
  assert.equal(outcomes.validation, 'pass');
  assert.equal(outcomes.timeout, 'fail');
  assert.equal(outcomes['falsy failure'], 'fail');
  assert.ok(setups.every(call => call.body.explicit_session === false), 'serial fallback must stay available');
  assert.equal(outcomes['test and capture cleanup fail'], 'fail');
  assert.match(runnerFailure.stderr, /original test failure retained/);
  assert.match(runnerFailure.stderr, /secondary capture cleanup failed/);
  assert.equal(outcomes['never settles'], 'fail');
  assert.equal(outcomes['assertion count'], 'fail');
  assert.equal(outcomes['hooks hook failure'], 'fail');
  assert.equal(outcomes['failed setup body never runs'], 'fail');
  assert.equal(result.calls.some(call => call.path === '/must-not-run'), false);
  for (const [name, prefix] of [['hooks hook failure', '/hook/'], ['failed setup body never runs', '/failed-setup/']]) {
    const setup = setups.find(call => call.body.name === name);
    const traffic = result.calls.filter(call => call.path.startsWith(prefix));
    assert.equal(traffic.length, 2, `${name}: setup and cleanup must each run once`);
    for (const call of traffic) assert.equal(call.traceparent.split('-')[1], setup.body.trace_context.traceparent.split('-')[1]);
  }
});
