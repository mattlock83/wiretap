// Export the application's ordinary HTTP handler; the adapter owns its listener.
module.exports = (request, response) => {
  const match = /^\/orders\/([^/?]+)$/.exec(request.url);
  if (request.method !== 'GET' || !match) {
    response.writeHead(404).end();
    return;
  }
  const id = Number(match[1]);
  const valid = Number.isSafeInteger(id) && id > 0;
  response.writeHead(valid ? 200 : 400, { 'content-type': 'application/json' });
  response.end(JSON.stringify(valid ? { id, status: 'ready' } : { error: 'invalid order ID' }));
};
