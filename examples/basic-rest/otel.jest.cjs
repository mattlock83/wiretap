const { createJestSuite } = require('../../sdk/js/jest');
const phrond = createJestSuite({
  baseUrl: process.env.PHROND_BASE_URL,
  sutBaseUrl: process.env.SUT_BASE_URL,
  schemas: [{ id: 'sut', type: 'openapi', source: { type: 'file', path: 'examples/basic-rest/schemas/sut-openapi.yaml' } }],
  dependencies: { 'inventory-service': {
    upstreamUrl: process.env.DEPENDENCY_UPSTREAM_URL,
    schema: { id: 'inventory', type: 'openapi', source: { type: 'file', path: 'examples/basic-rest/schemas/inventory-openapi.yaml' } }
  } }
});
let entered = 0, release;
const gate = new Promise(resolve => { release = resolve; });
phrond.test.concurrent.each(['123', '456'])('processes %s', async id => {
  if (++entered === 2) release();
  await gate; // Both sessions must exist before any request is sent.
  expect((await fetch(`${phrond.sutUrl}/status`)).status).toBe(200);
  const response = await fetch(`${phrond.sutUrl}/process`, {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ id, action: 'process' })
  });
  expect(await response.json()).toEqual({ message: 'processed', id, action: 'process' });
  await phrond.assertDependency('inventory-service', {
    method: 'POST', path: '/inventory/check', response: { '$.available': true }
  });
});
afterAll(async () => {
  const { report } = await phrond.runtime.report({ artifactsDir: process.env.PHROND_ARTIFACT_DIR });
  expect(report.testcases).toHaveLength(2);
  expect(new Set(report.testcases.map(tc => tc.attempts[0].trace_id)).size).toBe(2);
  for (const tc of report.testcases) {
    expect(tc.attempts[0].status).toBe('pass');
    expect(tc.traffic).toHaveLength(3);
    for (const flow of tc.traffic) {
      expect(flow.trace_id).toBe(tc.attempts[0].trace_id);
      expect(flow.test_correlation).toBe('trace');
      expect(flow.trace_context_source).toBe('received');
    }
    const downstream = tc.traffic.find(flow => flow.channel === 'sut_to_dependency');
    expect(downstream.request.body).toContain(tc.name.endsWith('123') ? '123' : '456');
    expect(downstream.parent_flow_id).toBeTruthy();
  }
});
