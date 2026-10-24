const { test } = require('../../../playwright');
test.use({ wiretapOptions: { sutBaseUrl: 'http://different.example:3001' } });
test('unsafe target', async ({ request }) => { await request.get('/must-not-run'); });
