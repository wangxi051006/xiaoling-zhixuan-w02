import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError, apiRequest, assertNoProductionMock } from '../src/shared/api.ts';

test('failed writes retry only with the same explicit idempotency key', async () => {
  let attempts = 0;
  const options = { method: 'POST', body: { quote_id: 'q1' }, csrf: 'csrf-x', idempotencyKey: 'same-key', retries: 1,
    fetcher: async (url, request) => {
      attempts++;
      assert.equal(url, '/api/v1/orders');
      assert.equal(request.headers.get('Idempotency-Key'), 'same-key');
      assert.equal(request.headers.get('X-CSRF-Token'), 'csrf-x');
      assert.equal(request.credentials, 'same-origin');
      if (attempts === 1) throw new TypeError('network');
      return Response.json({ id: 'o1' }, { status: 201 });
    },
  };
  assert.deepEqual(await apiRequest('/orders', options), { id: 'o1' });
  assert.equal(attempts, 2);
  attempts = 0;
  await assert.rejects(apiRequest('/orders', { ...options, idempotencyKey: undefined, fetcher: async () => { attempts++; throw new TypeError(); } }));
  assert.equal(attempts, 1);
});

test('business errors and unexpected success content are never treated as success', async () => {
  await assert.rejects(apiRequest('/orders', { fetcher: async () => Response.json({ code: 'SLOT_FULL', message: 'full', request_id: 'r1' }, { status: 409 }) }),
    err => err instanceof ApiError && err.code === 'SLOT_FULL' && err.requestId === 'r1' && err.status === 409);
  await assert.rejects(apiRequest('/orders', { fetcher: async () => new Response('<html>fallback</html>') }), ApiError);
  await assert.rejects(apiRequest('/../healthz'), /invalid API path/);
  await assert.rejects(apiRequest('//outside.invalid/'), /invalid API path/);
  assert.throws(() => assertNoProductionMock(true, 'true'), /production/);
});
