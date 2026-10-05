const assert = require('node:assert/strict');
const http = require('node:http');
const test = require('node:test');
const api = require('@opentelemetry/api');
const { startCapture, CoreClient, ExplorationError } = require('../src');

async function server(handler) {
  const srv = http.createServer(async (req, res) => {
    const parts = [];
    for await (const part of req) parts.push(part);
    const body = JSON.parse(Buffer.concat(parts).toString() || '{}');
    const reply = await handler(req.url, body, req);
    res.writeHead(reply?.httpStatus || 200, {'content-type':'application/json'});
    res.end(JSON.stringify(reply?.httpStatus ? reply.body : (reply || {})));
  });
  await new Promise(resolve => srv.listen(0, '127.0.0.1', resolve));
  return { baseUrl:`http://127.0.0.1:${srv.address().port}`, close:()=>new Promise(resolve=>srv.close(resolve)) };
}
function normal(path, body) {
  if (path === '/v1/setupTest') return {session_id:body.name, name:body.name};
  if (path === '/v1/startRun') return {sut_url:'http://sut.invalid'};
  return {capture:{testcase:{status:body.status || 'pass'}}};
}
function action(id, hook, event, input={}) {
  return {state:'running',action:{id,hook_id:hook,event,input:{event,bindings:{},state:{},headers:{},...input}}};
}
const completed = {state:'complete',result:{generated:2,executed:2,passed:2,failed:0,bundle_path:'/run/bundle'}};

test('explore dispatches scoped callbacks through HTTP and isolates their trace from the enclosing test', async () => {
  let start;
  const outputs = [];
  const traceID = 'a'.repeat(32);
  const steps = [
    action('a','hook-1','before_case',{operation:'POST /orders',trace_context:{traceparent:`00-${traceID}-${'b'.repeat(16)}-01`}}),
    action('b','hook-2','after_case'), completed
  ];
  const srv = await server((path,body) => {
    if (path === '/v1/explore') {start=body;return {exploration_id:'e',state:'running'};}
    if (path === '/v1/explore/next') return steps.shift();
    if (path === '/v1/explore/respond') {outputs.push(body);return {};}
    return normal(path,body);
  });
  try {
    const runtime = await startCapture({baseUrl:srv.baseUrl,schemaRequired:false,instrumentation:false});
    await runtime.runTest('orders', async () => {
      const parent = api.trace.getSpanContext(api.context.active()).traceId;
      const result = await runtime.explore({operations:['POST /orders'],hooks:{
        beforeCase:{operations:['POST /orders'],handler:async input=>{
          assert.equal(input.operation,'POST /orders');
          assert.equal(api.trace.getSpanContext(api.context.active()).traceId,traceID);
          await Promise.resolve();
          assert.equal(api.trace.getSpanContext(api.context.active()).traceId,traceID);
          return {headers:{Authorization:'fresh'},state:{fixture:1}};
        }},
        afterCase:() => {
          assert.equal(api.trace.getSpanContext(api.context.active()),undefined);
          return {state:{cleaned:true}};
        }
      }});
      const { bundle_path, ...counts } = completed.result;
      assert.deepEqual(result,{ ...counts, bundlePath: bundle_path });
      assert.equal(api.trace.getSpanContext(api.context.active()).traceId,parent);
    });
    assert.equal(start.session_id,'orders');
    assert.equal('max_cases' in start,false);
    assert.deepEqual(start.hooks,[{id:'hook-1',event:'before_case',operations:['POST /orders']},{id:'hook-2',event:'after_case'}]);
    assert.equal(outputs[0].output.headers.Authorization,'fresh');
    assert.equal(outputs[1].output.state.cleaned,true);
  } finally {await srv.close();}
});

test('callback assertions reach the server and cleanup completes before explore rejects',async()=>{
  const events=[], replies=[];
  const steps=[action('a','hook-1','check_case'),action('b','hook-2','after_case'),action('c','hook-3','after_explore'),{state:'failed',result:{...completed.result,failed:1},error:'hook failed'}];
  const srv=await server((path,body)=>{
    if(path==='/v1/explore') return {exploration_id:'e',state:'running'};
    if(path==='/v1/explore/next') return steps.shift();
    if(path==='/v1/explore/respond') {replies.push(body);return {};}
    return normal(path,body);
  });
  try {
    const runtime=await startCapture({baseUrl:srv.baseUrl,schemaRequired:false});
    await runtime.setupTest({name:'assertions'});
    await assert.rejects(runtime.explore({hooks:{
      checkCase:()=>{throw new Error('row was unexpectedly inserted');},
      afterCase:()=>{events.push('case cleanup');},
      afterExplore:()=>{events.push('explore cleanup');}
    }}),error=>error instanceof ExplorationError && error.result.failed===1 && /row was unexpectedly inserted/.test(error.cause.message));
    assert.match(replies[0].error,/row was unexpectedly inserted/);
    assert.deepEqual(events,['case cleanup','explore cleanup']);
    await runtime.teardownTest({status:'fail'});
  } finally {await srv.close();}
});

test('cancellation interrupts a callback and pumps cleanup with a fresh signal',async()=>{
  const controller=new AbortController();
  const events=[];
  let cancelled=false, cleanupSent=false;
  const srv=await server((path,body)=>{
    if(path==='/v1/explore') return {exploration_id:'e',state:'running'};
    if(path==='/v1/explore/cancel') {cancelled=true;events.push('cancel');return {};}
    if(path==='/v1/explore/next') {
      if(!cancelled) return action('a','hook-1','before_case');
      if(!cleanupSent) {cleanupSent=true;return action('b','hook-2','after_case');}
      return {state:'cancelled',result:{generated:1,executed:0,passed:0,failed:0}};
    }
    return normal(path,body);
  });
  try {
    const runtime=await startCapture({baseUrl:srv.baseUrl,schemaRequired:false});
    await runtime.setupTest({name:'cancel'});
    await assert.rejects(runtime.explore({signal:controller.signal,hooks:{
      beforeCase:async input=>{
        setImmediate(()=>controller.abort(new Error('test deadline')));
        await new Promise(resolve=>input.signal.addEventListener('abort',resolve,{once:true}));
      },
      afterCase:input=>{assert.equal(input.signal.aborted,false);events.push('cleanup');}
    }}),/test deadline/);
    assert.deepEqual(events,['cancel','cleanup']);
    await runtime.teardownTest({status:'fail'});
  } finally {await srv.close();}
});

test('CoreClient exposes the exploration HTTP protocol without lifecycle helpers',async()=>{
  const paths=[];
  const srv=await server((path,body)=>{paths.push(path);return {exploration_id:'id',state:'running'};});
  try {
    const client=new CoreClient({baseUrl:srv.baseUrl});
    await client.startExploration({session_id:'session'});
    const ref={session_id:'session',exploration_id:'id'};
    await client.nextExploration(ref);
    await client.respondExploration({...ref,action_id:'a',output:{}});
    await client.cancelExploration(ref);
    assert.deepEqual(paths,['/v1/explore','/v1/explore/next','/v1/explore/respond','/v1/explore/cancel']);
  } finally {await srv.close();}
});

test('framework teardown cancels outstanding exploration before closing capture and fails the outer test', async () => {
  let entered;
  const ready = new Promise(resolve => { entered = resolve; });
  const events = [];
  let cancelled = false, cleaned = false;
  const srv = await server((path, body) => {
    if (path === '/v1/explore') return { exploration_id: 'e', state: 'running' };
    if (path === '/v1/explore/cancel') { cancelled = true; return {}; }
    if (path === '/v1/explore/next') {
      if (!cancelled) return action('a', 'hook-1', 'before_case');
      if (!cleaned) return action('b', 'hook-2', 'after_case');
      return { state: 'cancelled', result: { generated: 1, executed: 0, passed: 0, failed: 0 } };
    }
    if (path === '/v1/teardownTest') { assert.equal(body.status, 'fail'); events.push('teardown'); }
    return normal(path, body);
  });
  try {
    const runtime = await startCapture({ baseUrl: srv.baseUrl, schemaRequired: false });
    await runtime.setupTest({ name: 'deadline' });
    const exploring = runtime.explore({ hooks: {
      beforeCase: async input => {
        entered();
        await new Promise(resolve => input.signal.addEventListener('abort', resolve, { once: true }));
      },
      afterCase: () => { cleaned = true; events.push('cleanup'); }
    }});
    // Framework timeout handlers observe the running promise while finishing.
    exploring.catch(() => {});
    await ready;
    await assert.rejects(runtime.teardownTest(), /ended before exploration completed/);
    await assert.rejects(exploring, /ended before exploration completed/);
    assert.deepEqual(events, ['cleanup', 'teardown']);
  } finally { await srv.close(); }
});

test('cancelled dispatch skips stale setup and continues cleanup after an expired response', async () => {
  const controller = new AbortController();
  let cancelled = false;
  const events = [];
  const cleanup = [
    action('stale', 'hook-2', 'before_request'),
    action('cleanup', 'hook-3', 'after_case'),
    action('finish', 'hook-4', 'after_explore'),
    { state: 'cancelled', result: { generated: 1, executed: 0, passed: 0, failed: 0 } }
  ];
  const srv = await server((path, body) => {
    if (path === '/v1/explore') return { exploration_id: 'e', state: 'running' };
    if (path === '/v1/explore/cancel') { cancelled = true; return {}; }
    if (path === '/v1/explore/next') return cancelled ? cleanup.shift() : action('setup', 'hook-1', 'before_case');
    if (path === '/v1/explore/respond' && body.action_id === 'cleanup') {
      return { httpStatus: 409, body: { error: 'hook action is no longer pending' } };
    }
    return normal(path, body);
  });
  try {
    const runtime = await startCapture({ baseUrl: srv.baseUrl, schemaRequired: false });
    await runtime.setupTest({ name: 'cancel race' });
    await assert.rejects(runtime.explore({ signal: controller.signal, hooks: {
      beforeCase: () => { controller.abort(new Error('test deadline')); },
      beforeRequest: () => { events.push('stale request'); },
      afterCase: () => { events.push('case cleanup'); },
      afterExplore: () => { events.push('explore cleanup'); }
    }}), /test deadline/);
    assert.deepEqual(events, ['case cleanup', 'explore cleanup']);
    await runtime.teardownTest({ status: 'fail' });
  } finally { await srv.close(); }
});

test('native gRPC exploration options and hook messages pass through the same HTTP API', async () => {
  let start, reply;
  const procedure = '/orders.v1.Orders/CreateOrder';
  const steps = [action('grpc', 'hook-1', 'before_request', {
    operation: procedure,
    grpc_request: { procedure, metadata: { authorization: ['old'] }, messages: [{ quantity: 0 }], messages_base64: ['EAA='], response_gates: [0] }
  }), completed];
  const srv = await server((path, body) => {
    if (path === '/v1/explore') { start = body; return { exploration_id: 'e', state: 'running' }; }
    if (path === '/v1/explore/next') return steps.shift();
    if (path === '/v1/explore/respond') { reply = body; return {}; }
    return normal(path, body);
  });
  try {
    const runtime = await startCapture({ baseUrl: srv.baseUrl, sutProtocol: 'grpc' });
    await runtime.setupTest({ name: 'grpc' });
    await runtime.explore({
      operations: [procedure], expectedGrpcStatuses: [3],
      bindings: [{ name: 'user', in: 'body', path: '/user_id', messageIndex: 0 }],
      extract: [{ name: 'token', in: 'trailers', path: 'token', flowId: 'seed', messageIndex: 0 }],
      hooks: { beforeRequest: { operations: [procedure], handler: input => {
        assert.equal(input.grpc_request.procedure, procedure);
        assert.deepEqual(input.grpc_request.messages, [{ quantity: 0 }]);
        return { headers: { authorization: 'fresh' } };
      } } }
    });
    assert.deepEqual(start.expected_grpc_statuses, [3]);
    assert.equal(start.bindings[0].message_index, 0);
    assert.equal(start.extract[0].message_index, 0);
    assert.deepEqual(reply.output.headers, { authorization: 'fresh' });
    await runtime.teardownTest();
  } finally { await srv.close(); }
});
