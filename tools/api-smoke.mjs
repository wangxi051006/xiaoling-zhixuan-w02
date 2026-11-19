import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { setTimeout as delay } from 'node:timers/promises';
import { once } from 'node:events';

const [binary] = process.argv.slice(2);
assert(binary && process.env.TEST_DATABASE_URL, 'usage: set TEST_DATABASE_URL, then node tools/api-smoke.mjs API_BINARY');

async function probe(dsn, expectedReadiness) {
  const portSocket = createServer();
  portSocket.listen(0, '127.0.0.1');
  await once(portSocket, 'listening');
  const host = `127.0.0.1:${portSocket.address().port}`;
  await new Promise(resolve => portSocket.close(resolve));

  const api = spawn(binary, [], { env: { ...process.env, APP_ENV:'test', HTTP_ADDR:host, DATABASE_URL:dsn }, windowsHide: true });
  const exit = new Promise(resolve => api.once('close', resolve));
  let stderr = '';
  api.stderr.setEncoding('utf8').on('data', chunk => { stderr += chunk; });
  api.stdout.resume();
  const url = path => `http://${host}${path}`;
  try {
    let health;
    for (let i = 0; i < 30 && api.exitCode === null; i++) {
      health = await fetch(url('/healthz')).catch(() => undefined);
      if (health) break;
      await delay(100);
    }
    assert.equal(health?.status, 200, `expected healthy loopback process: ${stderr}`);
    const ready = await fetch(url('/readyz'), { signal: AbortSignal.timeout(5000) });
    assert.equal(ready.status, expectedReadiness);
    const order = await fetch(url('/api/v1/orders'));
    assert.equal(order.status, 404);
    assert.equal((await order.json()).request_id, order.headers.get('x-request-id'));
    console.log(`PASS API live=200 ready=${expectedReadiness} unset route=404`);
  } finally {
    api.kill();
    await exit;
  }
}

await probe(process.env.TEST_DATABASE_URL, 200);
const connections = new Set();
const unresponsive = createServer(socket => {
  connections.add(socket);
  socket.on('error', () => {});
  socket.once('close', () => connections.delete(socket));
});
unresponsive.listen(0, '127.0.0.1');
await once(unresponsive, 'listening');
try {
  await probe(`postgres://fixture:fixture@127.0.0.1:${unresponsive.address().port}/fixture?sslmode=disable&connect_timeout=1`, 503);
} finally {
  for (const socket of connections) socket.destroy();
  await new Promise(resolve => unresponsive.close(resolve));
}
