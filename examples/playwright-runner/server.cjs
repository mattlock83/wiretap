const http = require('node:http');

module.exports = async function setup() {
  const todos = [];
  const server = http.createServer(async (req, res) => {
    res.setHeader('content-type', 'application/json');
    res.setHeader('x-request-traceparent', req.headers.traceparent || '');
    res.setHeader('x-test-client', req.headers['x-test-client'] || '');
    if (req.method === 'GET' && req.url === '/todos') {
      res.end(JSON.stringify(todos));
    } else if (req.method === 'POST' && req.url === '/todos') {
      let raw = ''; for await (const part of req) raw += part;
      let todo;
      try { todo = JSON.parse(raw); } catch { /* invalid JSON is a bad request */ }
      if (typeof todo?.title !== 'string' || !todo.title.trim()) {
        res.writeHead(400); res.end(JSON.stringify({ error: 'title is required' })); return;
      }
      const created = { id: todos.length + 1, title: todo.title };
      todos.push(created);
      res.writeHead(201); res.end(JSON.stringify(created));
    } else { res.writeHead(404); res.end(JSON.stringify({ error: 'not found' })); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const previous = process.env.PLAYWRIGHT_SUT_URL;
  process.env.PLAYWRIGHT_SUT_URL = `http://127.0.0.1:${server.address().port}`;
  return async () => {
    if (previous === undefined) delete process.env.PLAYWRIGHT_SUT_URL;
    else process.env.PLAYWRIGHT_SUT_URL = previous;
    await new Promise(resolve => server.close(resolve));
  };
};
