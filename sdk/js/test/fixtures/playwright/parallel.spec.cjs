const { test, expect } = require('../../../playwright');

test.use({ wiretapOptions: { sutBaseUrl: 'http://original.example:3000/api/' } });
for (const name of ['first', 'second']) test(name, async ({ request, wiretap }) => {
  for (let index = 0; index < 3; index++) {
    const response = await request.get('nested', { headers: { 'x-case': name } });
    const body = await response.json();
    expect(body.headers.traceparent).toBe(wiretap.traceContext.traceparent);
    expect(body.headers['x-existing']).toBe('kept');
    await new Promise(resolve => setTimeout(resolve, 15));
  }
  await request.get('/root', { headers: { 'x-case': name } });
});
