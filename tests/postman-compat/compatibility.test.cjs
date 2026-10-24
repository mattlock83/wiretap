const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { before, after, test } = require('node:test');
const { Collection } = require('postman-collection');
const Ajv = require('ajv');

const root = path.resolve(__dirname, '../..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'wiretap-postman-compat-'));
let http, grpc;
before(() => {
  execFileSync('go', ['test', './internal/postman', '-run=^TestWritePostmanParserFixtures$', '-count=1'], {
    cwd: root, env: { ...process.env, WIRETAP_POSTMAN_FIXTURE_DIR: temporary }, stdio: 'pipe', timeout: 120000,
  });
  http = JSON.parse(fs.readFileSync(path.join(temporary, 'sut-compat.postman_collection.json'), 'utf8'));
  grpc = JSON.parse(fs.readFileSync(path.join(temporary, 'sut-compat.grpc.postman_collection.json'), 'utf8'));
});
after(() => fs.rmSync(temporary, { recursive: true, force: true }));

const headers = entries => entries.map(({ key, value }) => ({ key, value }));
function httpProjection(collection) {
  const sdk = new Collection(collection);
  return {
    name: sdk.name,
    behavior: sdk.toJSON().protocolProfileBehavior,
    folders: sdk.items.all().map(folder => ({
      id: folder.id, name: folder.name,
      requests: folder.items.all().map(item => ({
        method: item.request.method,
        url: item.request.url.toString(),
        body: item.request.body?.toJSON(),
        headers: headers(item.request.headers.toJSON()),
        examples: item.responses.all().map(response => ({
          code: response.code, body: response.body, headers: headers(response.headers.toJSON()),
          requestURL: response.originalRequest.url.toString(),
          requestBody: response.originalRequest.body?.toJSON(),
        })),
        scripts: item.events.toJSON().map(event => ({ listen: event.listen, exec: event.script.exec })),
      })),
    })),
  };
}

test('official Postman SDK parses and round-trips actual Go HTTP exports without changing replay data', () => {
  const sdk = new Collection(http);
  const projection = httpProjection(http);
  assert.deepEqual(httpProjection(sdk.toJSON()), projection);
  const source = http.item.find(folder => folder.id === 'attempt-http').item[0];
  const imported = projection.folders.find(folder => folder.id === 'attempt-http').requests[0];
  assert.equal(imported.method, source.request.method);
  assert.equal(imported.url, source.request.url);
  assert.deepEqual(imported.body, source.request.body);
  assert.deepEqual(imported.headers, source.request.header);
  assert.equal(imported.examples[0].code, 201);
  assert.equal(imported.examples[0].body, source.response[0].body);
  assert.deepEqual(imported.examples[0].headers, source.response[0].header);
  assert.deepEqual(imported.examples[0].requestBody, source.request.body);
  assert.equal(imported.examples[0].requestURL, source.request.url);
  assert.deepEqual(Buffer.from(imported.body.file.content, 'base64'), Buffer.from([0, 255, 1, 128]));
  assert.deepEqual(Buffer.from(imported.examples[0].body, 'base64'), Buffer.from([255, 0, 1]));
  assert.match(imported.scripts[0].exec.join('\n'), /pm.response.to.have.status\(201\)/);
  assert.deepEqual(projection.behavior, http.protocolProfileBehavior);
});

test('native gRPC v3 passes the pinned official schema and lossless JSON round-trip (SDK supports v2.1 only)', () => {
  const schema = JSON.parse(fs.readFileSync(path.join(root, 'internal/postman/testdata/postman-v3.0.0.schema.json'), 'utf8'));
  const validate = new Ajv({ strict: false, validateFormats: false, allErrors: true }).compile(schema);
  assert.equal(validate(grpc), true, JSON.stringify(validate.errors));
  assert.deepEqual(JSON.parse(JSON.stringify(grpc)), grpc);
  const folders = grpc.items;
  assert.equal(new Set(folders.map(folder => folder.id)).size, 6);
  assert.equal(new Set(folders.map(folder => folder.name)).size, 6);
  const expected = {
    Echo: ['outgoing', 'incoming'], Watch: ['outgoing', 'incoming', 'incoming'],
    Collect: ['outgoing', 'outgoing', 'incoming'], Chat: ['outgoing', 'incoming', 'outgoing', 'incoming'],
  };
  for (const method of Object.keys(expected)) {
    const request = folders.find(folder => folder.id === `attempt-${method}`).items[0];
    assert.equal(request.methodPath.split('/').at(-1), method);
    assert.equal(request.url, 'localhost:50051');
    assert.equal(request.settings.secureConnection, false);
    assert.equal(request.schema.source, 'file');
    assert.match(fs.readFileSync(path.join(temporary, request.schema.location), 'utf8'), /service Streams/);
    const example = request.examples[0];
    assert.deepEqual(example.response.messages.map(message => message.type), expected[method]);
    assert.equal(example.request.url, request.url);
    assert.equal(example.request.message.content, request.message.content);
    assert.equal(example.response.code, 0);
    assert.deepEqual(example.request.metadata.filter(h => h.key === 'x-tag').map(h => h.value), ['one', 'two']);
    assert.equal(example.request.metadata.find(h => h.key === 'example-bin').value, 'AAEC');
    assert.equal(example.response.trailers.find(h => h.key === 'reply-bin').value, '/w==');
  }
  const error = folders.find(folder => folder.id === 'attempt-error').items[0].examples[0].response;
  assert.equal(error.code, 3);
  assert.deepEqual(error.messages.map(message => message.type), ['outgoing']);
  const rich = error.trailers.find(h => h.key === 'grpc-status-details-bin').value;
  assert.deepEqual(Buffer.from(rich, 'base64'), Buffer.from([8, 3, 18, 3, 98, 97, 100]));
});
