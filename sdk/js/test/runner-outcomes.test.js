const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const execute = promisify(execFile);

for (const runner of ['node', 'jest']) test(`${runner} final outcomes include timeouts, hooks and validation`, async t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'phrond-outcomes-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const resultPath = path.join(dir, 'result.json');
  const fixture = path.join(__dirname, 'fixtures', `${runner}-outcomes.cjs`);
  const args = runner === 'node' ? ['--test', fixture] : [require.resolve('jest/bin/jest'), '--runInBand', '--config', JSON.stringify({
    rootDir: path.resolve(__dirname, '..'), testEnvironment: './jest-environment.js',
    testMatch: ['**/test/fixtures/jest-outcomes.cjs'], transform: {}
  })];
  // The child suites intentionally fail. The parent verifies both the runner
  // result and capture lifecycle, without introducing failures in npm test.
  const env = { ...process.env, PHROND_REVIEW_RESULT: resultPath };
  delete env.NODE_TEST_CONTEXT;
  await assert.rejects(execute(process.execPath, args, {
    env, timeout: 20000
  }), error => error.code === 1);
  const result = JSON.parse(fs.readFileSync(resultPath, 'utf8'));
  const setups = result.calls.filter(call => call.path === '/v1/setupTest');
  const teardowns = result.calls.filter(call => call.path === '/v1/teardownTest');
  assert.equal(teardowns.length, setups.length);
  assert.equal(result.open, 0);
  const outcomes = Object.fromEntries(teardowns.map(call => [call.body.session_id, call.body.status]));
  assert.equal(outcomes.pass, 'pass');
  assert.equal(outcomes.timeout, 'fail');
  assert.ok(setups.every(call => call.body.explicit_session === false), 'serial fallback must stay available');
  if (runner === 'node') {
    assert.equal(result.beforeCounts.pass, 1, 'existing hooks must not run twice');
    assert.equal(outcomes['hook failure'], 'fail');
    assert.equal(outcomes['subtest failure'], 'fail');
    assert.equal(outcomes.skip, 'skip');
    const spans = Object.fromEntries(result.spans.map(span => [span.name, span.status]));
    for (const name of ['hook failure', 'subtest failure', 'timeout', 'validation']) assert.equal(spans[name], 2, name);
  } else {
    assert.equal(outcomes['never settles'], 'fail');
    assert.equal(outcomes['assertion count'], 'fail');
    assert.equal(outcomes['hooks hook failure'], 'fail');
  }
});
