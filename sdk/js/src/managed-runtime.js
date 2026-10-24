const childProcess = require('node:child_process');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { withoutTracing } = require('./tracing');

function duration(value, fallback, name) {
  value ??= fallback;
  if (!Number.isSafeInteger(value) || value < 1 || value > 2147483647) {
    throw new Error(`${name} must be a positive integer no greater than 2147483647`);
  }
  return value;
}

function abortError(signal) {
  return signal.reason instanceof Error ? signal.reason : new Error('Wiretap startup was cancelled');
}

/** Own one local Wiretap process. The application and test runner remain caller-owned. */
async function startManagedRuntime(options = {}) {
  const cwd = path.resolve(options.cwd || process.cwd());
  const startupTimeoutMs = duration(options.startupTimeoutMs, 30000, 'startupTimeoutMs');
  const timeoutMs = duration(options.timeoutMs, 30000, 'timeoutMs');
  const shutdownTimeoutMs = duration(options.shutdownTimeoutMs, 5000, 'shutdownTimeoutMs');
  const binary = options.binary || process.env.WIRETAP_BINARY || 'wiretap';
  if (typeof binary !== 'string' || !binary.trim()) throw new Error('binary must be an executable path or command');
  if (options.signal?.aborted) throw abortError(options.signal);
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-'));
  const readyFile = path.join(directory, 'ready.json');
  const outputDir = options.outputDir && path.resolve(cwd, options.outputDir);
  const args = options.config ? ['--config', path.resolve(cwd, options.config)] : [];
  const env = { ...process.env, WIRETAP_READY_FILE: readyFile,
    WIRETAP_HTTP_LISTEN: '127.0.0.1:0', WIRETAP_PROXY_LISTEN: '127.0.0.1:0',
    ...(outputDir ? { WIRETAP_OUTPUT_DIR: outputDir } : {}) };
  let child, exited = false, exitDescription, logs = '', baseUrl, runtimeInfo, closing;
  let resolveExit;
  const exit = new Promise(resolve => { resolveExit = resolve; });
  // Keep useful startup diagnostics bounded even if a failing process is noisy.
  const appendLog = data => { logs = (logs + data.toString()).slice(-65536); };
  const onExit = () => { if (!exited) child?.kill('SIGTERM'); };
  const onAbort = () => { void close().catch(() => {}); };
  const wait = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
  async function waitForExit() {
    let timer;
    try { await Promise.race([exit, new Promise(resolve => { timer = setTimeout(resolve, shutdownTimeoutMs); })]); }
    finally { clearTimeout(timer); }
  }
  async function request(endpoint, body, signal) {
    const deadline = AbortSignal.timeout(timeoutMs);
    const response = await withoutTracing(() => fetch(new URL(endpoint, baseUrl), {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body), signal: signal ? AbortSignal.any([signal, deadline]) : deadline
    }));
    const text = await response.text();
    let receipt;
    try { receipt = JSON.parse(text); } catch { throw new Error(`Wiretap ${endpoint} returned an invalid JSON response (${response.status})`); }
    if (!response.ok) {
      const error = new Error(`Wiretap ${endpoint} failed (${response.status}): ${receipt.error || response.statusText}`);
      error.status = response.status;
      error.body = receipt;
      throw error;
    }
    return receipt;
  }
  async function stop() {
    if (!child || exited) return;
    child.kill('SIGTERM');
    await waitForExit();
    if (!exited) {
      child.kill('SIGKILL');
      await waitForExit();
      if (!exited) throw new Error('Wiretap process did not exit after SIGKILL');
    }
  }
  function close() {
    if (!closing) closing = (async () => {
      let receipt, failure;
      try {
        if (runtimeInfo) receipt = await request('/v1/finishRun', {});
      } catch (error) { failure = error; receipt = error.body; }
      try { await stop(); } catch (error) { failure ??= error; }
      process.removeListener('exit', onExit);
      options.signal?.removeEventListener('abort', onAbort);
      try {
        const artifactDir = outputDir || (runtimeInfo?.bundle_path && path.dirname(runtimeInfo.bundle_path));
        if (artifactDir) {
          await fs.mkdir(artifactDir, { recursive: true });
          await fs.writeFile(path.join(artifactDir, 'wiretap.log'), logs);
          if (receipt) await fs.writeFile(path.join(artifactDir, 'last-run.json'), `${JSON.stringify(receipt, null, 2)}\n`);
        }
      } catch (error) { failure ??= error; }
      try { await fs.rm(directory, { recursive: true, force: true }); }
      catch (error) { failure ??= error; }
      if (failure) throw failure;
      return receipt;
    })();
    return closing;
  }
  try {
    child = childProcess.spawn(binary, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
    child.stdout.on('data', appendLog);
    child.stderr.on('data', appendLog);
    child.once('error', error => { exited = true; exitDescription = error.message; resolveExit(); });
    child.once('exit', (code, signal) => {
      exited = true; exitDescription = signal ? `signal ${signal}` : `exit code ${code}`; resolveExit();
    });
    process.once('exit', onExit);
    const deadline = Date.now() + startupTimeoutMs;
    while (!baseUrl) {
      if (options.signal?.aborted) throw abortError(options.signal);
      if (exited) throw new Error(`Wiretap exited before becoming ready: ${exitDescription}${logs ? `\n${logs}` : ''}`);
      try {
        const ready = JSON.parse(await fs.readFile(readyFile, 'utf8'));
        const url = new URL(ready.control_url);
        if (ready.pid !== child.pid || url.protocol !== 'http:' || !['127.0.0.1', '[::1]', 'localhost'].includes(url.hostname)) {
          throw new Error('Wiretap readiness file did not identify the owned local process');
        }
        baseUrl = url.href;
      } catch (error) {
        if (error.code !== 'ENOENT' && !(error instanceof SyntaxError)) throw error;
      }
      if (!baseUrl) {
        if (Date.now() >= deadline) throw new Error(`Wiretap did not become ready within ${startupTimeoutMs}ms${logs ? `\n${logs}` : ''}`);
        await Promise.race([exit, wait(Math.min(25, Math.max(1, deadline - Date.now())))]);
      }
    }
    runtimeInfo = await request('/v1/startRun', {}, options.signal);
    if (options.signal?.aborted) throw abortError(options.signal);
    options.signal?.addEventListener('abort', onAbort, { once: true });
    return { baseUrl, sutUrl: runtimeInfo.sut_url, runtimeInfo, close };
  } catch (error) {
    try { await close(); } catch (cleanupError) { error.cleanupError = cleanupError; }
    throw error;
  }
}

module.exports = { startManagedRuntime };
