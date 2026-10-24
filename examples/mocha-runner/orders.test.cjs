const assert = require('node:assert/strict');

// Change the existing shared client's base URL once. Keep ordinary Mocha hooks,
// requests and assertions; capture starts before beforeEach and ends after afterEach.
const api = route => fetch(process.env.WIRETAP_SUT_URL + route);

describe('orders', () => {
  beforeEach(async () => {
    const response = await api('/orders/1');
    assert.equal(response.status, 200);
    await response.json();
  });

  afterEach(async function () {
    const response = await api('/orders/2');
    assert.equal(response.status, 200);
    await response.json();
    if (process.env.WIRETAP_EXAMPLE_FAIL === '1' && this.currentTest.title === 'gets an order') {
      throw new Error('intentional user cleanup failure');
    }
  });

  it('gets an order', async () => {
    const response = await api('/orders/12');
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { id: 12, status: 'ready' });
  });

  it('rejects an invalid ID', async () => {
    const response = await api('/orders/invalid');
    assert.equal(response.status, 400);
    assert.deepEqual(await response.json(), { error: 'invalid order ID' });
  });
});
