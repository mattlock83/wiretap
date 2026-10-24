// A real HTTP control server isolates adapter semantics from the Go executable.
// Mocha still runs in its own process with the normal CLI, hooks and reporters.
const http = require('node:http');
const fs = require('node:fs/promises');

// An npm alias lets the same integration suite exercise both supported Mocha
// versions. Resolve the SDK's peer to the CLI version under test.
if (process.env.WIRETAP_MOCHA_PACKAGE) {
  const resolved = require.resolve('mocha');
  require.cache[resolved] = { id: resolved, filename: resolved, loaded: true, exports: require(process.env.WIRETAP_MOCHA_PACKAGE) };
}

require('../../src/managed-runtime').startManagedRuntime = async function () {
  const calls = [];
  let session = 0;
  const server = http.createServer(async (req, res) => {
    let raw = ''; for await (const chunk of req) raw += chunk;
    const body = raw ? JSON.parse(raw) : {};
    calls.push({ path: req.url, body, traceparent: req.headers.traceparent });
    res.setHeader('content-type', 'application/json');
    if (req.url === '/v1/startRun') res.end(JSON.stringify({ sut_url: url, defaults: { schema_required: false } }));
    else if (req.url === '/v1/setupTest') {
      globalThis.__wiretapMochaSutTarget = body.sut_base_url;
      res.end(JSON.stringify({ session_id: `session-${++session}`, name: body.name, sut_url: url }));
    }
    else if (req.url === '/v1/teardownTest' && process.env.WIRETAP_MOCHA_EXPORT_FAIL) {
      res.statusCode = 500; res.end(JSON.stringify({ error: 'deliberate export failure' }));
    }
    else res.end(JSON.stringify({ ok: true }));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${server.address().port}`;
  return { baseUrl: url, sutUrl: url, async close() {
    const result = { calls, closed: true, cleanup: globalThis.__wiretapMochaCleanup || [] };
    await fs.writeFile(process.env.WIRETAP_MOCHA_RESULT, JSON.stringify(result));
    await new Promise(resolve => { server.close(resolve); server.closeAllConnections(); });
  } };
};
