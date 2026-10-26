const childProcess = require('node:child_process');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const { CoreClient } = require('./control-client');
const { retainCleanupError } = require('./cleanup-error');

function localURL(value, allowTLS = false) {
  if (typeof value !== 'string') return false;
  let url;
  try { url = new URL(value); } catch { return false; }
  const host = url.hostname.replace(/^\[|\]$/g, '');
  const port = value.match(/^[a-z]+:\/\/[^/?#]*:(\d+)(?:[/?#]|$)/i)?.[1];
  return (url.protocol === 'http:' || allowTLS && url.protocol === 'https:') &&
    !url.username && !url.password && !url.search && !url.hash && url.pathname === '/' &&
    (host === 'localhost' || host === '::1' || net.isIP(host) === 4 && host.startsWith('127.')) &&
    port !== undefined && Number(port) > 0 && Number(port) <= 65535;
}

async function validateStart(info) {
  if (!info || typeof info.run_id !== 'string' || !info.run_id.trim() ||
      typeof info.bundle_path !== 'string' || !path.isAbsolute(info.bundle_path) || !localURL(info.sut_url, true)) {
    throw new Error('Wiretap startRun returned an incomplete or invalid run receipt');
  }
  try { if ((await fs.stat(info.bundle_path)).isDirectory()) return; } catch { /* Diagnostic below. */ }
  throw new Error('Wiretap startRun returned a missing or invalid bundle directory');
}

function sameRun(receipt, info) {
  return receipt && receipt.run_id === info.run_id && receipt.bundle_path === info.bundle_path;
}

async function writeArtifact(directory, name, data) {
  const staging = await fs.mkdtemp(path.join(directory, '.wiretap-sdk-'));
  try {
    const file = path.join(staging, name);
    await fs.writeFile(file, data, { flag: 'wx', mode: 0o644 });
    await fs.rename(file, path.join(directory, name));
  } finally { await fs.rm(staging, { recursive: true, force: true }); }
}

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
  if (Object.hasOwn(options, 'outputs')) throw new Error('outputs configuration was removed; automatic exports always include JSON, HTML and Arazzo');
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
    WIRETAP_EXIT_ON_STDIN_EOF: '1',
    WIRETAP_HTTP_LISTEN: '127.0.0.1:0', WIRETAP_PROXY_LISTEN: '127.0.0.1:0',
    ...(outputDir ? { WIRETAP_OUTPUT_DIR: outputDir } : {}) };
  let child, exited = false, exitDescription, exitFailure, logs = Buffer.alloc(0), baseUrl, runtimeInfo, closing, client;
  let resolveExit;
  const exit = new Promise(resolve => { resolveExit = resolve; });
  // Keep useful startup diagnostics bounded even if a failing process is noisy.
  const appendLog = data => {
    const bytes = Buffer.isBuffer(data) ? data : Buffer.from(data);
    logs = Buffer.concat([logs, bytes.subarray(-65536)]).subarray(-65536);
  };
  // The OS also closes this pipe if the owner is killed without running exit hooks.
  const onExit = () => { child?.stdin.destroy(); };
  const onAbort = () => { void close().catch(() => {}); };
  const wait = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
  async function waitForExit() {
    let timer;
    try { await Promise.race([exit, new Promise(resolve => { timer = setTimeout(resolve, shutdownTimeoutMs); })]); }
    finally { clearTimeout(timer); }
  }
  async function stop() {
    onExit();
    if (!child) return;
    if (!exited) await waitForExit();
    if (!exited) {
      child.kill('SIGKILL');
      await waitForExit();
      if (!exited) throw new Error('Wiretap process did not exit after SIGKILL');
      throw new Error('Wiretap process exceeded its shutdown timeout and was killed');
    }
    if (exitFailure) throw exitFailure;
  }
  function close() {
    if (!closing) closing = (async () => {
      let receipt, failure;
      const fail = error => { if (failure) retainCleanupError(failure, error); else failure = error; };
      try {
        if (runtimeInfo) {
          receipt = await client.finishRun();
          if (receipt?.status !== 'complete' || !sameRun(receipt, runtimeInfo)) {
            throw new Error('Wiretap finishRun returned an incomplete or mismatched run receipt');
          }
        }
      } catch (error) { fail(error); if (error.body !== undefined) receipt = error.body; }
      try { await stop(); } catch (error) { fail(error); }
      process.removeListener('exit', onExit);
      options.signal?.removeEventListener('abort', onAbort);
      try {
        if (runtimeInfo) {
          const artifactDir = runtimeInfo.bundle_path;
          await writeArtifact(artifactDir, 'wiretap.log', logs);
          if (receipt !== undefined) {
            const data = `${JSON.stringify(receipt, null, 2)}\n`;
            await writeArtifact(artifactDir, 'last-run.json', data);
          }
        }
      } catch (error) { fail(error); }
      try { await fs.rm(directory, { recursive: true, force: true }); }
      catch (error) { fail(error); }
      if (failure) throw failure;
      return receipt;
    })();
    return closing;
  }
  try {
    // Windows otherwise kills all non-detached children immediately when Node
    // exits. Let the stdin lifeline trigger archive finalization instead.
    child = childProcess.spawn(binary, args, { cwd, env, stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true, detached: process.platform === 'win32' });
    // A child that exits early can close its end first; its exit reports the failure.
    child.stdin.on('error', () => {});
    child.stdout.on('data', appendLog);
    child.stderr.on('data', appendLog);
    child.once('error', error => { exited = true; exitDescription = error.message; exitFailure = error; resolveExit(); });
    child.once('exit', (code, signal) => {
      exited = true; exitDescription = signal ? `signal ${signal}` : `exit code ${code}`; resolveExit();
      if (code !== 0 || signal) exitFailure = new Error(`Wiretap process exited unsuccessfully: ${exitDescription}`);
    });
    process.once('exit', onExit);
    const deadline = Date.now() + startupTimeoutMs;
    while (!baseUrl) {
      if (options.signal?.aborted) throw abortError(options.signal);
      if (exited) throw new Error(`Wiretap exited before becoming ready: ${exitDescription}${logs.length ? `\n${logs}` : ''}`);
      try {
        const ready = JSON.parse(await fs.readFile(readyFile, 'utf8'));
        const url = new URL(ready.control_url);
        if (ready.pid !== child.pid || !localURL(ready.control_url)) {
          throw new Error('Wiretap readiness file did not identify the owned local process');
        }
        baseUrl = url.href;
      } catch (error) {
        if (error.code !== 'ENOENT' && !(error instanceof SyntaxError)) throw error;
      }
      if (!baseUrl) {
        if (Date.now() >= deadline) throw new Error(`Wiretap did not become ready within ${startupTimeoutMs}ms${logs.length ? `\n${logs}` : ''}`);
        await Promise.race([exit, wait(Math.min(25, Math.max(1, deadline - Date.now())))]);
      }
    }
    client = new CoreClient({ baseUrl, timeoutMs, requireJSON: true });
    const started = await client.startRun({}, { signal: options.signal });
    await validateStart(started);
    runtimeInfo = started;
    if (options.signal?.aborted) throw abortError(options.signal);
    options.signal?.addEventListener('abort', onAbort, { once: true });
    return { baseUrl, sutUrl: runtimeInfo.sut_url, runtimeInfo, close };
  } catch (error) {
    try { await close(); } catch (cleanupError) { retainCleanupError(error, cleanupError); }
    throw error;
  }
}

module.exports = { startManagedRuntime };
