// Replace this synthetic service with your existing service/mock startup.
import http from 'node:http';

export function createService() {
  return http.createServer((request, response) => {
    if (request.url === '/healthz') {
      response.writeHead(200).end('ok');
      return;
    }
    const match = /^\/orders\/([^/?]+)$/.exec(request.url);
    if (request.method !== 'GET' || !match) {
      response.writeHead(404).end();
      return;
    }
    const id = Number(match[1]);
    const valid = Number.isSafeInteger(id) && id > 0;
    response.writeHead(valid ? 200 : 400, { 'content-type': 'application/json' });
    response.end(JSON.stringify(valid ? { id, status: 'pending' } : { error: 'invalid_order_id' }));
  });
}
