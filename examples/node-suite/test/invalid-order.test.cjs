const assert = require('node:assert/strict');
const { createNodeTestSuite } = require('@wiretap/sdk');
const suite = createNodeTestSuite({ manageRuntime: false });

suite.test('rejects an invalid order ID', async (_t, session) => {
  const response = await fetch(`${session.sutUrl}/orders/not-a-number`);
  assert.equal(response.status, 400);
  assert.deepEqual(await response.json(), { error: 'invalid_order_id' });
  // The invalid request remains validation evidence; the framework result passes.
});
