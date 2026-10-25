const childProcess = require('node:child_process');
const { EventEmitter } = require('node:events');
const { PassThrough } = require('node:stream');
const fs = require('node:fs/promises');
const http = require('node:http');
const path = require('node:path');

module.exports = async function setup() {
  const calls = [];
  const bundlePath = path.join(process.env.WIRETAP_PLAYWRIGHT_RESULT, 'bundle');
  await fs.mkdir(bundlePath, { recursive: true });
  const server = http.createServer(async (req, res) => {
    let raw = ''; for await (const part of req) raw += part;
    const body = JSON.parse(raw || '{}');
    calls.push({ path: req.url, body, headers: req.headers });
    res.setHeader('content-type', 'application/json');
    if (req.url === '/v1/startRun') res.end(JSON.stringify({ sut_url: url, defaults: { schema_required: false }, run_id: 'playwright-fixture', bundle_path: bundlePath }));
    else if (req.url === '/v1/setupTest') res.end(JSON.stringify({ session_id: `${body.test_identity.id}:${body.test_identity.attempt_ordinal}`, name: body.name, sut_url: url }));
    else if (req.url === '/v1/teardownTest') {
      // Finalization intentionally outlives onTestEnd. The global teardown
      // must wait for it before asking the runtime to export the run.
      await new Promise(resolve => setTimeout(resolve, 30));
      res.end(JSON.stringify({ capture: {} }));
    } else if (req.url === '/v1/finishRun') res.end(JSON.stringify({ status: 'complete', run_id: 'playwright-fixture', bundle_path: bundlePath, outputs: { errors: [] } }));
    else res.end(JSON.stringify({ ok: true, headers: req.headers }));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${server.address().port}`;
  const original = childProcess.spawn;
  childProcess.spawn = function (binary, args, options) {
    if (binary !== 'wiretap-test-runtime') return original.apply(this, arguments);
    const child = new EventEmitter();
    child.pid = process.pid;
    child.stdin = new PassThrough();
    child.stdin.once('close', () => child.emit('exit', 0, null));
    child.stdout = new PassThrough(); child.stderr = new PassThrough();
    child.kill = signal => { queueMicrotask(() => child.emit('exit', null, signal)); return true; };
    void fs.writeFile(options.env.WIRETAP_READY_FILE, JSON.stringify({ control_url: url, pid: child.pid }));
    return child;
  };
  return async () => {
    childProcess.spawn = original;
    await fs.writeFile(path.join(process.env.WIRETAP_PLAYWRIGHT_RESULT, 'calls.json'), JSON.stringify(calls));
    await new Promise(resolve => server.close(resolve));
  };
};
