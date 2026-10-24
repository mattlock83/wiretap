const http = require('node:http');
const crypto = require('node:crypto');
const path = require('node:path');
const { startCapture } = require('./index');
const { startManagedRuntime } = require('./managed-runtime');
const { metadataKey } = require('./playwright-config');
const { getRun, setRun, attemptKey } = require('./playwright-state');

module.exports = async function setup(config) {
  const { explicitSession = false, capture = {}, ...managedOptions } = config.metadata[metadataKey];
  if (config.workers > 1 && !explicitSession) throw new Error('Parallel Playwright capture requires explicitSession: true');
  if (getRun()) throw new Error('A managed Wiretap Playwright run is already active');
  const cwd = managedOptions.cwd || path.dirname(config.configFile || path.join(process.cwd(), 'playwright.config.js'));
  // Playwright handles SIGINT through its task runner, including worker stop
  // and global cleanup. Give process supervisors the same path for SIGTERM.
  const onSigterm = () => process.emit('SIGINT');
  process.on('SIGTERM', onSigterm);
  let managed;
  try { managed = await startManagedRuntime({ ...managedOptions, outputs: managedOptions.outputs ?? capture.outputs, cwd }); }
  catch (error) { process.removeListener('SIGTERM', onSigterm); throw error; }
  let server;
  let parallelOrigin;
  const attempts = new Map();
  const failures = [];
  const pending = new Set();
  const token = crypto.randomBytes(24).toString('hex');
  const previous = process.env.WIRETAP_PLAYWRIGHT_COORDINATOR;
  async function closeServer() {
    if (!server?.listening) return;
    // No worker should be sending more setup requests once global cleanup starts.
    // Destroy abandoned keep-alive/partial requests instead of waiting forever.
    const closed = new Promise(resolve => server.close(resolve));
    server.closeAllConnections();
    await closed;
  }
  const finish = (key, outcome) => {
    const record = attempts.get(key);
    if (!record) return false;
    if (record.finishing) return true;
    record.finishing = true;
    const job = record.attempt.finish(outcome).catch(error => { failures.push(error); }).finally(() => pending.delete(job));
    pending.add(job);
    return true;
  };
  try {
    const runtime = await startCapture({ ...capture, baseUrl: managed.baseUrl, manageRuntime: false, instrumentation: false, explicitSession });
    if (config.workers > 1) {
      const origins = new Set(config.projects.map(project => project.use?.wiretapOptions?.sutBaseUrl || capture.testDefaults?.sutBaseUrl || runtime.defaultSutBaseUrl).filter(Boolean).map(value => new URL(value).origin));
      if (origins.size > 1) throw new Error('Parallel Playwright projects must use the same SUT origin; run different servers in separate Wiretap runs or use workers: 1');
      parallelOrigin = origins.values().next().value;
    }
    server = http.createServer(async (req, res) => {
      res.setHeader('content-type', 'application/json');
      if (req.method !== 'POST' || req.url !== '/session' || req.headers.authorization !== `Bearer ${token}`) {
        res.writeHead(403); res.end(JSON.stringify({ error: 'Invalid Wiretap coordinator request' })); return;
      }
      try {
        let raw = '';
        for await (const chunk of req) {
          raw += chunk;
          if (Buffer.byteLength(raw) > 65536) throw new Error('Wiretap session request is too large');
        }
        const body = JSON.parse(raw);
        if (!explicitSession) await Promise.all(pending);
        const key = attemptKey(body.id, body.retry);
        if (attempts.has(key)) throw new Error('Wiretap test attempt already exists');
        const targetValue = body.options?.sutBaseUrl || capture.testDefaults?.sutBaseUrl || runtime.defaultSutBaseUrl;
        const target = targetValue ? new URL(targetValue) : undefined;
        if (config.workers > 1 && target) {
          parallelOrigin ??= target.origin;
          if (target.origin !== parallelOrigin) throw new Error('Parallel Playwright tests must use the same SUT origin; a per-test target override requires a separate run or workers: 1');
        }
        const attempt = runtime.createTestAttempt(body.name, {
          ...capture,
          ...capture.testDefaults,
          ...body.options,
          // Playwright resolves paths against baseURL in the client. Keep the
          // target origin separate so the reverse proxy does not prepend that
          // path a second time (or change leading-slash URL semantics).
          ...(target ? { sutBaseUrl: target.origin } : {}),
          identity: { id: body.id, variant: body.project || undefined, attemptOrdinal: body.retry + 1 },
          // Playwright/browser transports carry their own trace headers. Serial
          // fallback remains available for uninstrumented application dependencies.
          explicitSession,
        });
        attempts.set(key, { attempt });
        const session = await attempt.run(value => value);
        const clientURL = new URL(session.sutUrl);
        if (target) {
          clientURL.pathname = target.pathname; clientURL.search = target.search; clientURL.hash = target.hash;
        }
        res.end(JSON.stringify({ sessionId: session.sessionId, sutUrl: clientURL.href,
          dependencyUrls: session.dependencyUrls, traceContext: session.traceContext }));
      } catch (error) {
        res.writeHead(500); res.end(JSON.stringify({ error: error.message }));
      }
    });
    server.requestTimeout = 10000;
    server.headersTimeout = 10000;
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
    process.env.WIRETAP_PLAYWRIGHT_COORDINATOR = JSON.stringify({ url: `http://127.0.0.1:${server.address().port}/session`, token, timeoutMs: managedOptions.timeoutMs ?? 30000 });
    setRun({ finish, failures, reporterConnected: false });
  } catch (error) {
    await closeServer();
    await managed.close().catch(cleanupError => { error.cleanupError = cleanupError; });
    process.removeListener('SIGTERM', onSigterm);
    throw error;
  }
  return async () => {
    // onTestEnd has the final status after every fixture has torn down. Its
    // promises are observed here because Playwright does not await onTestEnd.
    if (!getRun()?.reporterConnected) failures.push(new Error('Wiretap reporter was replaced; keep the reporter added by withWiretap (do not override it with --reporter)'));
    for (const [key, record] of attempts) {
      if (!record.finishing) {
        const error = new Error('Playwright ended before reporting the final test result');
        failures.push(error);
        finish(key, { status: 'fail', error });
      }
    }
    await Promise.all(pending);
    await closeServer();
    try { await managed.close(); } catch (error) { failures.push(error); }
    process.removeListener('SIGTERM', onSigterm);
    if (previous === undefined) delete process.env.WIRETAP_PLAYWRIGHT_COORDINATOR;
    else process.env.WIRETAP_PLAYWRIGHT_COORDINATOR = previous;
    setRun(undefined);
    if (failures.length) throw new AggregateError(failures, `Wiretap could not complete Playwright capture: ${failures.map(error => error.message).join('; ')}`);
  };
};
