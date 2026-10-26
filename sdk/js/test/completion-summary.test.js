const assert = require('node:assert/strict');
const test = require('node:test');
const { printCompletionSummary } = require('../src/completion-summary');

test('completion summaries use exact successful receipt paths on stderr', t => {
  const output = [];
  t.mock.method(process.stderr, 'write', chunk => { output.push(chunk); return true; });
  printCompletionSummary({ status: 'complete', bundle_path: '/run with spaces/archive', outputs: {
    coverage: { text: '/custom/report.txt', json: '/custom/report.json' },
    arazzo: '/workflows/arazzo.json', errors: []
  } });
  assert.deepEqual(output, ['Wiretap archive: /run with spaces/archive\n' +
    'Wiretap coverage (json): /custom/report.json\n' +
    'Wiretap coverage (text): /custom/report.txt\n' +
    'Wiretap Arazzo: /workflows/arazzo.json\n']);
});

test('incomplete receipts do not announce successful capture', t => {
  const write = t.mock.method(process.stderr, 'write', () => true);
  for (const receipt of [undefined, { status: 'idle' }, { status: 'complete' },
    { status: 'incomplete', bundle_path: '/archive' },
    { status: 'complete', bundle_path: '/archive', outputs: { errors: [{ message: 'export failed' }] } }]) {
    printCompletionSummary(receipt);
  }
  assert.equal(write.mock.callCount(), 0);
});
