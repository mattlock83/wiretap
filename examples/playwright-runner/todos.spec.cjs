const { test, expect } = require('../../sdk/js/playwright');

test('create and list a todo', async ({ request, wiretap }) => {
  const created = await request.post('/todos', { data: { title: 'Try Wiretap' } });
  expect(created.status()).toBe(201);
  expect(created.headers()['x-request-traceparent']).toBe(wiretap.traceContext.traceparent);
  expect(created.headers()['x-test-client']).toBe('playwright');
  const listed = await request.get('/todos');
  expect(await listed.json()).toContainEqual(await created.json());
});

test('reject an empty title', async ({ request }) => {
  const response = await request.post('/todos', { data: { title: '' } });
  expect(response.status()).toBe(400);
  expect(await response.json()).toEqual({ error: 'title is required' });
});
