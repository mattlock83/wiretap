// Shared by compatibility tests and clean external consumer smoke checks.
// Ajv comes from the caller's installation; the schema is pinned in this repo.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { TextDecoder } = require('node:util');

function createHTTPValidator(Ajv) {
  const schema = JSON.parse(fs.readFileSync(path.join(__dirname, '../../internal/postman/testdata/postman-v2.1.0.schema.json'), 'utf8'));
  const validate = new Ajv({ strict: false, validateFormats: false, allErrors: true }).compile(schema);
  return collection => {
    assert.equal(validate(collection), true, JSON.stringify(validate.errors));
    assert.equal(collection.info.schema, 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json');
    for (const [setting, expected] of Object.entries({
      followRedirects: false, disableBodyPruning: true, disableCookies: true, disableUrlEncoding: true,
    })) assert.equal(collection.protocolProfileBehavior[setting], expected, setting);
    return collection;
  };
}

function headerValues(entries) {
  const result = {};
  for (const entry of entries) {
    assert.notEqual(entry.disabled, true, `disabled captured header ${entry.key}`);
    (result[entry.key.toLowerCase()] ??= []).push(entry.value);
  }
  return result;
}

function capturedHeaders(headers = {}, omitted = new Set()) {
  const result = {};
  for (const [name, values] of Object.entries(headers)) {
    if (!omitted.has(name.toLowerCase())) (result[name.toLowerCase()] ??= []).push(...values);
  }
  return result;
}

function retainedBody(bundlePath, attempt, flow, phase) {
  const body = flow[phase]?.body;
  if (!body) return Buffer.alloc(0);
  assert.ok(!body.omitted && !body.redacted && !body.truncated, 'smoke fixture needs complete retained bodies');
  const reference = (attempt.bodies || []).find(entry => entry.flow_id === flow.id && entry.phase === phase && !entry.kind);
  if (reference) return fs.readFileSync(path.join(bundlePath, reference.path));
  return body.bytes ? Buffer.from(body.bytes, 'base64') : Buffer.from(body.text || '', 'utf8');
}

function bodyText(bytes) {
  try { return { text: new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes), binary: false }; }
  catch { return { text: bytes.toString('base64'), binary: true }; }
}

// Compare exports against the archive's independent capture records and payload
// files, rather than comparing a document with a JSON round trip of itself.
function assertCapturedHTTP(collection, attempts, bundlePath) {
  assert.equal(collection.item.length, attempts.length, 'one folder per captured attempt');
  const folders = new Map(collection.item.map(folder => [folder.id, folder]));
  assert.equal(folders.size, attempts.length, 'duplicate attempt folders');
  let count = 0;
  for (const attempt of attempts) {
    const capture = attempt.capture;
    assert.equal(attempt.attempt_id, capture.testcase.id);
    const folder = folders.get(attempt.attempt_id);
    assert.ok(folder, `missing attempt folder ${attempt.attempt_id}`);
    const flows = capture.traffic.filter(flow => flow.protocol === 'http' && flow.channel === 'test_to_sut' && !flow.interaction);
    assert.equal(folder.item.length, flows.length, `request count for ${attempt.attempt_id}`);
    const remaining = new Map(flows.map(flow => [`Captured flow: ${flow.id}.`, flow]));
    for (const item of folder.item) {
      const description = item.request.description;
      const key = [...remaining.keys()].find(prefix => description.startsWith(prefix));
      const flow = remaining.get(key);
      assert.ok(flow, `unknown or repeated exported flow: ${description}`);
      remaining.delete(key);
      const request = flow.request;
      assert.equal(item.request.method, request.method);
      assert.equal(item.request.url, request.url);
      const omitted = new Set(['host', 'content-length', 'transfer-encoding', 'connection', 'proxy-connection', 'trailer', 'te', 'upgrade']);
      for (const value of capturedHeaders(request.headers).connection || []) {
        for (const token of value.split(',')) omitted.add(token.trim().toLowerCase());
      }
      if (request.body?.content_encoding) omitted.add('content-encoding');
      assert.deepEqual(headerValues(item.request.header), capturedHeaders(request.headers, omitted));
      if (request.body) {
        const body = bodyText(retainedBody(bundlePath, attempt, flow, 'request'));
        assert.equal(item.request.body.mode, body.binary ? 'file' : 'raw');
        if (body.binary) {
          assert.equal(item.request.body.file.src, null);
          assert.equal(item.request.body.file.content, body.text);
        } else {
          assert.equal(item.request.body.raw, body.text);
          assert.equal(item.request.body.options.raw.language, 'text');
        }
      } else assert.equal(item.request.body, undefined);
      assert.equal(item.response.length, flow.response ? 1 : 0);
      if (flow.response) {
        const response = item.response[0];
        assert.equal(response.code, flow.response.status);
        assert.deepEqual(response.originalRequest, item.request);
        assert.deepEqual(headerValues(response.header), capturedHeaders(flow.response.headers));
        assert.equal(response.body, bodyText(retainedBody(bundlePath, attempt, flow, 'response')).text);
        assert.equal(response.responseTime, flow.duration_ms || 0);
        const event = item.event.find(event => event.listen === 'test');
        assert.equal(event.script.type, 'text/javascript');
        assert.ok(event.script.exec.includes(`    pm.response.to.have.status(${flow.response.status});`));
      }
      count++;
    }
    assert.equal(remaining.size, 0, 'capture flow missing from export');
  }
  return count;
}

module.exports = { createHTTPValidator, assertCapturedHTTP, headerValues };
