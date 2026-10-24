const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { before, after, test } = require('node:test');
const Ajv = require('ajv');
const { createHTTPValidator, headerValues } = require('./http-validation.cjs');

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

test('actual Go HTTP export satisfies the official v2.1 schema and preserves known fixture data', () => {
  createHTTPValidator(Ajv)(http);
  assert.equal(http.info.name, 'Compatibility - compat');
  assert.deepEqual(http.item.map(folder => folder.id).sort(), [
    'attempt-Chat', 'attempt-Collect', 'attempt-Echo', 'attempt-Watch', 'attempt-error', 'attempt-http', 'attempt-retry',
  ]);
  assert.equal(new Set(http.item.map(folder => folder.name)).size, 7);
  for (const folder of http.item) assert.equal(folder.item.length, folder.id === 'attempt-http' ? 1 : 0);
  const item = http.item.find(folder => folder.id === 'attempt-http').item[0];
  assert.equal(item.request.method, 'POST');
  assert.equal(item.request.url, 'http://sut:8080/bytes?x=a&x=b&encoded=%2F');
  assert.deepEqual(item.request.body, { mode: 'file', file: { src: null, content: 'AP8BgA==' } });
  assert.deepEqual(Buffer.from(item.request.body.file.content, 'base64'), Buffer.from([0, 255, 1, 128]));
  assert.deepEqual(headerValues(item.request.header), {
    'content-type': ['application/octet-stream'], 'x-repeated': ['one', 'two'],
  });
  assert.equal(item.response.length, 1);
  const response = item.response[0];
  assert.equal(response.code, 201);
  assert.equal(response.body, '/wAB');
  assert.deepEqual(Buffer.from(response.body, 'base64'), Buffer.from([255, 0, 1]));
  assert.deepEqual(headerValues(response.header), { 'set-cookie': ['a=1', 'b=2'] });
  assert.deepEqual(response.originalRequest, item.request);
  assert.deepEqual(item.event, [{ listen: 'test', script: { type: 'text/javascript', exec: [
    'pm.test("Status code is 201", function () {', '    pm.response.to.have.status(201);', '});',
  ] } }]);
  assert.deepEqual(http.protocolProfileBehavior.disabledSystemHeaders, {
    'accept': true, 'accept-encoding': true, 'cache-control': true, 'content-type': true,
    'postman-token': true, 'user-agent': true,
  });
});

test('native gRPC v3 passes the pinned official schema and preserves known fixture data', () => {
  const schema = JSON.parse(fs.readFileSync(path.join(root, 'internal/postman/testdata/postman-v3.0.0.schema.json'), 'utf8'));
  const validate = new Ajv({ strict: false, validateFormats: false, allErrors: true }).compile(schema);
  assert.equal(validate(grpc), true, JSON.stringify(validate.errors));
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
