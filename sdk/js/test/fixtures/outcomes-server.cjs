const http = require('node:http');
const { once } = require('node:events');
const fs = require('node:fs');

module.exports = async function outcomesServer() {
  const calls = [];
  const server = http.createServer(async (req, res) => {
    let raw = ''; for await (const chunk of req) raw += chunk;
    const body = JSON.parse(raw || '{}');
    calls.push({ path: req.url, body });
    res.setHeader('content-type', 'application/json');
    if (req.url === '/v1/setupTest') res.end(JSON.stringify({ session_id: body.name, name: body.name, sut_url: url, proxy_listen: url }));
    else if (req.url === '/v1/teardownTest') res.end(JSON.stringify({
      capture: { schema_validation: { status: body.session_id.includes('validation') ? 'fail' : 'skipped' } }
    }));
    else res.end(JSON.stringify({ sut_url: url }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const url = `http://127.0.0.1:${server.address().port}`;
  return { url, async finish(extra) {
    fs.writeFileSync(process.env.PHROND_REVIEW_RESULT, JSON.stringify({ calls, ...extra }));
    await new Promise(resolve => server.close(resolve));
  } };
};
