const fs = require('node:fs/promises');
const path = require('node:path');
const { test } = require('../../../playwright');

test('interrupted test', async ({ request }) => {
  await request.get('/api/ready');
  await fs.writeFile(path.join(process.env.WIRETAP_PLAYWRIGHT_RESULT, 'ready'), 'ready');
  await new Promise(() => {});
});
