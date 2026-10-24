const assert = require('node:assert/strict');
const { createNodeTestSuite } = require('@wiretap/sdk');
// The parent harness owns finalization. Each test file owns only its attempts.
const suite = createNodeTestSuite({ manageRuntime: false });

suite.test('gets an order', async (t, session) => {
  const response = await fetch(`${session.sutUrl}/orders/123`);
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { id: 123, status: 'pending' });
  // Exercise a framework cleanup failure without changing the API traffic.
  if (process.env.WIRETAP_EXAMPLE_FAIL === '1') {
    t.after(() => { throw new Error('original test cleanup failure'); });
  }
});
