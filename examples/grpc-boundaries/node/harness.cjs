const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const net = require('node:net');
const crypto = require('node:crypto');
const { spawn, execFileSync } = require('node:child_process');
const { once } = require('node:events');
const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const { startCapture } = require('../../../sdk/js/src');

const protoRoot = path.resolve(__dirname, '../proto');
const health = grpc.loadPackageDefinition(protoLoader.loadSync('health.proto', {
  includeDirs: [protoRoot], defaults: true, enums: String, longs: String,
})).grpc.health.v1;
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

function client(url, ca) {
  return new health.Health(new URL(url).host, ca ? grpc.credentials.createSsl(ca) : grpc.credentials.createInsecure(),
    ca ? { 'grpc.ssl_target_name_override': 'localhost', 'grpc.default_authority': 'localhost' } : {});
}
function call(connection, service, { timeout = 3000, metadata = new grpc.Metadata(), cancel = false } = {}) {
  return new Promise(resolve => {
    let headers;
    const request = connection.check({ service }, metadata, { deadline: Date.now() + timeout }, (error, response) => {
      resolve({ code: error?.code ?? grpc.status.OK, details: error?.details || '', response,
        headers: headers || {}, trailers: metadataValues(error?.metadata) });
    });
    request.on('metadata', value => { headers = metadataValues(value); });
    if (cancel) setTimeout(() => request.cancel(), 30);
  });
}
function metadataValues(metadata) {
  const result = {};
  for (const key of ['x-example', 'example-bin']) {
    const values = metadata?.get(key) || [];
    if (values.length) result[key] = values.map(value => Buffer.isBuffer(value) ? value.toString('base64') : value);
  }
  return result;
}
function reply(call, callback) {
  const metadata = new grpc.Metadata();
  metadata.add('x-example', 'first'); metadata.add('x-example', 'second');
  metadata.add('example-bin', Buffer.from([0, 255, 42]));
  if (call.request.service === 'invalid') {
    callback({ code: grpc.status.INVALID_ARGUMENT, details: 'expected invalid request', metadata });
    return;
  }
  if (call.request.service === 'slow') {
    const timer = setTimeout(() => callback(null, { status: 'SERVING' }), 5000);
    call.on('cancelled', () => clearTimeout(timer));
    return;
  }
  const finish = () => {
    call.sendMetadata(metadata);
    callback(null, call.request.service === 'empty' ? {} : { status: 'SERVING' });
  };
  // Enough overlap to exercise multiplexed channels and traced ownership.
  if (call.request.service.startsWith('parallel-')) setTimeout(finish, 60);
  else finish();
}
async function grpcServer(address = '127.0.0.1:0', dependency, tls) {
  const server = new grpc.Server();
  server.addService(health.Health.service, {
    check: dependency ? (incoming, callback) => {
      const outgoing = dependency.check(incoming.request, (error, response) => callback(error, response));
      incoming.on('cancelled', () => outgoing.cancel());
    } : reply,
    list: (_call, callback) => callback(null, {}),
  });
  const credentials = tls ? grpc.ServerCredentials.createSsl(null, [{ private_key: tls.key, cert_chain: tls.cert }]) : grpc.ServerCredentials.createInsecure();
  const port = await new Promise((resolve, reject) => server.bindAsync(address, credentials, (error, value) => error ? reject(error) : resolve(value)));
  return { url: `${tls ? 'https' : 'http'}://127.0.0.1:${port}`, close: () => server.forceShutdown() };
}
async function freePort() {
  const server = net.createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}
async function waitFor(check, message) {
  for (let i = 0; i < 100; i++) { try { if (await check()) return; } catch {} await delay(50); }
  throw new Error(message);
}
async function stopProcess(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, 'exit'); child.kill('SIGTERM');
  const timer = setTimeout(() => child.kill('SIGKILL'), 3000);
  try { await exited; } finally { clearTimeout(timer); }
}
const httpSpec = {
  openapi: '3.0.3', info: { title: 'Node HTTP facade', version: '1' }, paths: {
    '/check': { get: { parameters: [{ name: 'service', in: 'query', schema: { type: 'string' } }], responses: {
      200: { description: 'Health', content: { 'application/json': { schema: {
        type: 'object', required: ['status'], properties: { status: { type: 'string' } },
      } } } },
    } } },
  },
};

async function fixture(t, { name, mode = 'provider', tls = false, explicitSession = false } = {}) {
  assert.ok(process.env.WIRETAP_EXAMPLE_BINARY, 'Run ../run-node.sh; it builds the local fixture binaries first.');
  const work = await fs.mkdtemp(path.join(os.tmpdir(), 'wiretap-node-'));
  const output = path.resolve(process.env.WIRETAP_GRPC_ARTIFACT_DIR || path.join(work, 'bundles'), name);
  await fs.mkdir(output, { recursive: true });
  await fs.cp(protoRoot, path.join(work, 'proto'), { recursive: true });
  const controlPort = await freePort(); const providerPort = await freePort();
  const certificate = tls ? {
    cert: await fs.readFile(path.join(process.env.WIRETAP_EXAMPLE_TLS_DIR, 'tls.crt')),
    key: await fs.readFile(path.join(process.env.WIRETAP_EXAMPLE_TLS_DIR, 'tls.key')),
  } : undefined;
  const boundary = url => ({ protocol: 'grpc', url, services: ['grpc.health.v1.Health'],
    schema: { source: 'proto', files: ['health.proto'], import_paths: ['./proto'] },
    ...(tls ? { capture_tls: { cert_file: path.join(process.env.WIRETAP_EXAMPLE_TLS_DIR, 'tls.crt'), key_file: path.join(process.env.WIRETAP_EXAMPLE_TLS_DIR, 'tls.key') },
      upstream_tls: { ca_file: path.join(process.env.WIRETAP_EXAMPLE_TLS_DIR, 'tls.crt'), server_name: 'localhost' } } : {}),
  });
  const hasDependency = mode !== 'provider';
  let mock, provider, dependency, wiretap, goProvider;
  const clients = [];
  const closeUpstreams = async () => {
    for (const connection of clients) connection.close();
    dependency?.close(); provider?.close(); mock?.close();
    await stopProcess(goProvider); goProvider = null;
  };
  t.after(async () => { await closeUpstreams(); await stopProcess(wiretap); await fs.rm(work, { recursive: true, force: true }); });
  if (hasDependency) mock = await grpcServer();
  const sutUrl = `${tls ? 'https' : 'http'}://127.0.0.1:${providerPort}`;
  const config = { version: 1, http_listen: `127.0.0.1:${controlPort}`, proxy_listen: '127.0.0.1:0', output_dir: output,
    sut: { id: 'node-provider', kind: 'service', ...boundary(sutUrl) },
    ...(hasDependency ? { dependencies: { inventory: { kind: 'mock', ...boundary(mock.url) } } } : {}),
  };
  if (mode === 'mixed') {
    await fs.writeFile(path.join(work, 'http.json'), JSON.stringify(httpSpec));
    config.sut = { id: 'http-facade', kind: 'service', url: sutUrl, schema: './http.json' };
  }
  await fs.writeFile(path.join(work, '.wiretap.yaml'), JSON.stringify(config));
  const logFile = await fs.open(path.join(work, 'wiretap.log'), 'w');
  wiretap = spawn(process.env.WIRETAP_EXAMPLE_BINARY, ['--config', path.join(work, '.wiretap.yaml')], { stdio: ['ignore', logFile.fd, logFile.fd] });
  await logFile.close();
  const baseUrl = `http://127.0.0.1:${controlPort}`;
  try { await waitFor(async () => (await fetch(`${baseUrl}/healthz`)).ok, 'Wiretap did not start'); }
  catch (error) { throw new Error(`${error.message}\n${await fs.readFile(path.join(work, 'wiretap.log'), 'utf8')}`); }
  const runtime = await startCapture({ baseUrl, explicitSession, instrumentation: false });
  if (hasDependency) dependency = client(runtime.dependencyUrl('inventory'));
  if (mode === 'go') {
    goProvider = spawn(process.env.WIRETAP_EXAMPLE_GO_SERVER, ['--listen', `127.0.0.1:${providerPort}`, '--dependency', runtime.dependencyUrl('inventory'), '--reflection=false'], { stdio: 'ignore' });
    const probe = client(sutUrl);
    try { await new Promise((resolve, reject) => probe.waitForReady(Date.now() + 5000, error => error ? reject(error) : resolve())); }
    finally { probe.close(); }
  } else if (mode === 'mixed') {
    const server = http.createServer((request, response) => {
      const service = new URL(request.url, sutUrl).searchParams.get('service') || '';
      dependency.check({ service }, (error, value) => {
        response.writeHead(error ? 502 : 200, { 'content-type': 'application/json' }); response.end(JSON.stringify(value || {}));
      });
    });
    server.listen(providerPort, '127.0.0.1'); await once(server, 'listening');
    provider = { close() { server.closeAllConnections(); server.close(); } };
  } else provider = await grpcServer(`127.0.0.1:${providerPort}`, dependency, certificate);
  const getClient = url => { const connection = client(url, certificate?.cert); clients.push(connection); return connection; };
  const paths = new Set();
  return { runtime, sutUrl, getClient, call, mode, grpc,
    remember(session) { paths.add(session.bundlePath); },
    async finishAndInspect(inspect) {
      await runtime.finishRun();
      await closeUpstreams();
      await fs.rm(path.join(work, 'proto'), { recursive: true });
      await fs.rm(path.join(work, 'http.json'), { force: true });
      assert.equal(paths.size, 1);
      const root = [...paths][0];
      const manifest = JSON.parse(await fs.readFile(path.join(root, 'bundle.json'), 'utf8'));
      assert.equal(manifest.run_state, 'complete');
      const attempts = [];
      for (const ref of manifest.tests) {
        const bytes = await fs.readFile(path.join(root, ref.path));
        assert.equal(crypto.createHash('sha256').update(bytes).digest('hex'), ref.sha256);
        attempts.push(JSON.parse(bytes));
      }
      for (const schema of manifest.contracts.filter(schema => schema.type === 'protobuf')) {
        assert.equal(schema.acquired_from, 'proto');
        assert.ok(schema.source.path.startsWith('contracts/'));
        assert.equal(crypto.createHash('sha256').update(await fs.readFile(path.join(root, schema.source.path))).digest('hex'), schema.source.sha256);
      }
      for (const attempt of attempts) {
        for (const body of attempt.bodies || []) {
          const bytes = await fs.readFile(path.join(root, body.path));
          assert.equal(crypto.createHash('sha256').update(bytes).digest('hex'), body.sha256);
        }
        for (const flow of attempt.capture.traffic) {
          if (flow.protocol !== 'grpc') continue;
          assert.equal(flow.request.procedure, '/grpc.health.v1.Health/Check');
          assert.ok(!flow.request.procedure.includes('Reflection'));
          if (flow.response.status_source) assert.equal(typeof flow.response.grpc_status, 'number');
        }
      }
      const projection = async (attempt, flow, phase) => {
        const ref = attempt.bodies.find(body => body.flow_id === flow.id && body.phase === phase && body.message_index === 0 && !body.kind);
        assert.ok(ref, `missing ${phase} projection for ${flow.id}`);
        return JSON.parse(await fs.readFile(path.join(root, ref.path), 'utf8'));
      };
      await inspect({ root, manifest, attempts, projection });
      // Coverage must rebuild solely from the capture after upstreams and sources are gone.
      const exports = path.join(root, 'exports');
      await fs.mkdir(exports, { recursive: true });
      const coveragePath = path.join(exports, 'coverage.json');
      execFileSync(process.env.WIRETAP_EXAMPLE_BINARY, ['report', '--bundle', root, '--format', 'json', '--output', coveragePath], { stdio: 'pipe' });
      const coverage = JSON.parse(await fs.readFile(coveragePath, 'utf8'));
      assert.equal(coverage.artifact_count, attempts.length);
      console.log(`Raw capture: ${root}`);
      console.log(`JSON coverage: ${coveragePath}`);
      return { root, manifest, attempts };
    },
  };
}
module.exports = { fixture, call, grpc, delay };
