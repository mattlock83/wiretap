const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

function createArazzoValidator(Ajv2020) {
  const schema = JSON.parse(fs.readFileSync(path.join(__dirname, '../../internal/arazzo/testdata/arazzo-1.0.schema.json'), 'utf8'));
  const validate = new Ajv2020({ strict: false, validateFormats: false, allErrors: true }).compile(schema);
  return document => {
    assert.equal(validate(document), true, JSON.stringify(validate.errors));
    assert.equal(document.arazzo, '1.0.1');
    return document;
  };
}

function retainedBody(bundlePath, attempt, flow, phase) {
  const body = flow[phase]?.body;
  if (!body) return Buffer.alloc(0);
  assert.ok(!body.omitted && !body.redacted && !body.truncated, 'fixture needs complete retained bodies');
  const ref = (attempt.bodies || []).find(entry => entry.flow_id === flow.id && entry.phase === phase);
  return ref ? fs.readFileSync(path.join(bundlePath, ref.path)) : body.bytes ? Buffer.from(body.bytes, 'base64') : Buffer.from(body.text || '', 'utf8');
}

// Verify the projection against independent archive records and retained bytes.
// Response bodies stay in the archive; Arazzo asserts captured status criteria.
function assertCapturedArazzo(document, attempts, bundlePath, documentPath) {
  assert.equal(document.arazzo, '1.0.1');
  assert.ok(document.sourceDescriptions.length > 0);
  for (const source of document.sourceDescriptions) {
    assert.equal(source.type, 'openapi');
    assert.ok(!path.isAbsolute(source.url) && !source.url.split('/').includes('..'));
    assert.ok(fs.statSync(path.join(path.dirname(documentPath), source.url)).isFile(), 'missing OpenAPI companion');
  }
  const workflows = new Map();
  for (const workflow of document.workflows) {
    const key = workflow['x-wiretap-attempt-id'] + ':' + workflow['x-wiretap-boundary'];
    assert.ok(!workflows.has(key), 'duplicate workflow boundary');
    workflows.set(key, workflow);
  }
  let count = 0;
  for (const attempt of attempts) {
    const capture = attempt.capture;
    assert.equal(attempt.attempt_id, capture.testcase.id);
    const frozenContract = (capture.schemas || []).some(schema => schema.type === 'openapi' && schema.source.type !== 'omitted' && (schema.role === 'sut' || !schema.role));
    const jsonMedia = media => media === 'application/json' || /^application\/[^;]+\+json$/.test(media);
    const eligible = flow => {
      if (flow.protocol !== 'http' || flow.channel !== 'test_to_sut' || flow.interaction) return false;
      let hasJSON = false;
      let hasBody = false;
      for (const phase of ['request', 'response']) {
        const message = flow[phase];
        if (!message) return false;
        const headers = Object.entries(message.headers || {}).filter(([name]) => name.toLowerCase() === 'content-type').flatMap(([, values]) => values);
        assert.ok(headers.length <= 1, 'fixture contains ambiguous media type');
        const media = (headers[0] || '').split(';')[0].trim().toLowerCase();
        if (media && !jsonMedia(media)) return false;
        const bytes = retainedBody(bundlePath, attempt, flow, phase);
        if (bytes.length) {
          hasBody = true;
          if (!media) return false;
          JSON.parse(bytes.toString('utf8'));
        }
        hasJSON ||= jsonMedia(media);
      }
      return hasJSON || (frozenContract && !hasBody);
    };
    const inbound = capture.traffic.filter(flow => flow.protocol === 'http' && flow.channel === 'test_to_sut' && !flow.interaction);
    const flows = inbound.filter(eligible);
    if (!flows.length) {
      assert.equal(workflows.has(attempt.attempt_id + ':sut'), false, 'non-JSON traffic acquired a workflow');
      continue;
    }
    const workflow = workflows.get(attempt.attempt_id + ':sut');
    assert.ok(workflow, 'missing captured attempt workflow');
    assert.equal(workflow.summary, capture.testcase.name);
    assert.equal(workflow.steps.length, flows.length, 'captured request count differs');
    const remaining = new Map(flows.map(flow => [flow.id, flow]));
    const value = expression => {
      assert.match(expression, /^\$inputs\.value_\d+$/);
      return workflow.inputs.properties[expression.slice('$inputs.'.length)].default;
    };
    for (const step of workflow.steps) {
      const flow = remaining.get(step['x-wiretap-flow-id']);
      assert.ok(flow, 'unknown or repeated captured flow');
      remaining.delete(flow.id);
      assert.equal(step['x-wiretap-url'], flow.request.url);
      assert.deepEqual(step['x-wiretap-request-headers'] || {}, flow.request.headers || {});
      const operation = /^\{\$sourceDescriptions\.([^.]+)\.url\}#(.+)$/.exec(step.operationPath);
      assert.ok(operation && document.sourceDescriptions.some(source => source.name === operation[1]), 'unknown operation source');
      assert.equal(decodeURIComponent(operation[2]).split('/').at(-1), flow.request.method.toLowerCase());
      assert.deepEqual(step.successCriteria, [{ condition: '$statusCode == ' + flow.response.status }]);
      if (retainedBody(bundlePath, attempt, flow, 'request').length) {
        assert.ok(step.requestBody, 'missing request payload');
        assert.deepEqual(value(step.requestBody.payload), JSON.parse(retainedBody(bundlePath, attempt, flow, 'request').toString('utf8')));
      } else assert.equal(step.requestBody, undefined);
      for (const parameter of step.parameters || []) {
        const actual = value(parameter.value);
        if (parameter.in === 'query') assert.equal(actual, new URL(flow.request.url).searchParams.get(parameter.name));
        if (parameter.in === 'header') {
          const key = Object.keys(flow.request.headers || {}).find(name => name.toLowerCase() === parameter.name.toLowerCase());
          assert.equal(actual, flow.request.headers[key][0]);
        }
      }
      count++;
    }
    assert.equal(remaining.size, 0);
  }
  return count;
}
module.exports = { createArazzoValidator, assertCapturedArazzo };
