// Isolated HTTP behavior test: no database, real API, production hostname or credential.
// Certificate must cover user.p07.test/rider.p07.test/admin.p07.test.
import { createServer } from 'node:http';
import { request } from 'node:https';
import { createServer as createNetServer } from 'node:net';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdirSync, writeFileSync, readFileSync, existsSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
const argv = process.argv.slice(2);
assert.equal(argv.length, 8, 'usage: verify-edge.mjs --caddy <exe> --cert <pem> --key <pem> --work <new-directory>');
const opts = Object.fromEntries(Array.from({ length: 4 }, (_, i) => [argv[i * 2].replace('--', ''), resolve(argv[i * 2 + 1])]));
for (const key of ['caddy', 'cert', 'key']) assert.ok(existsSync(opts[key]), `${key} missing`);
assert.ok(opts.work && !existsSync(opts.work), 'work directory must be new');
const template = fileURLToPath(new URL('../Caddyfile', import.meta.url));
const listen = server => new Promise((yes, no) => { server.once('error', no); server.listen(0, '127.0.0.1', () => yes(server.address().port)); });
const unusedPort = async () => { const s = createNetServer(); const p = await listen(s); await new Promise(r => s.close(r)); return p; };
const upstreamRequests = [];
const upstream = createServer((req, res) => {
  upstreamRequests.push({ host: req.headers.host, forwarded: req.headers.forwarded, xff: req.headers['x-forwarded-for'], proto: req.headers['x-forwarded-proto'], forwardedOrigin: req.headers['x-forwarded-origin'] });
  let size = 0; req.on('data', bytes => size += bytes.length);
  req.on('end', () => { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ ...upstreamRequests.at(-1), size })); });
});
const apiPort = await listen(upstream);
const httpsPort = await unusedPort(), httpPort = await unusedPort();
const staticRoot = resolve(opts.work, 'static');
for (const entry of ['user', 'rider', 'admin']) {
  mkdirSync(resolve(staticRoot, entry, 'assets'), { recursive: true });
  writeFileSync(resolve(staticRoot, entry, `${entry}.html`), `<main>${entry}</main>`);
  writeFileSync(resolve(staticRoot, entry, 'assets', `${entry}.js`), `/* ${entry} asset */`);
}
const env = { ...process.env, USER_HOST: `user.p07.test:${httpsPort}`, RIDER_HOST: `rider.p07.test:${httpsPort}`, ADMIN_HOST: `admin.p07.test:${httpsPort}`, TLS_CERT_FILE: opts.cert, TLS_KEY_FILE: opts.key, STATIC_ROOT: staticRoot, API_UPSTREAM: `127.0.0.1:${apiPort}`, HTTP_PORT: String(httpPort), HTTPS_PORT: String(httpsPort), EDGE_BIND: '127.0.0.1', XDG_DATA_HOME: resolve(opts.work, 'caddy-data'), XDG_CONFIG_HOME: resolve(opts.work, 'caddy-config'), APPDATA: resolve(opts.work, 'caddy-appdata'), LOCALAPPDATA: resolve(opts.work, 'caddy-localappdata') };
delete env.ADMIN_VPN_CIDRS;
const edge = spawn(opts.caddy, ['run', '--config', template, '--adapter', 'caddyfile'], { env, cwd: opts.work, windowsHide: true });
let output = '', exited = false;
edge.stdout.on('data', b => output += b); edge.stderr.on('data', b => output += b);
edge.on('exit', () => exited = true);
const ca = readFileSync(opts.cert);
const probe = (entry, path, extra = {}) => new Promise((yes, no) => {
  const req = request({ hostname: '127.0.0.1', port: httpsPort, servername: `${entry}.p07.test`, ca, rejectUnauthorized: true, path, method: extra.body ? 'POST' : 'GET', headers: { Host: `${entry}.p07.test:${httpsPort}`, ...extra.headers }, localAddress: extra.localAddress, timeout: 4000 }, res => { let body = ''; res.on('data', bytes => body += bytes); res.on('end', () => yes({ status: res.statusCode, headers: res.headers, body })); });
  req.on('error', no); req.on('timeout', () => req.destroy(new Error('probe timeout')));
  req.end(extra.body);
});
const results = [];
const logMarker = 'p07-fixture-' + randomBytes(16).toString('hex');
async function check(name, action) { await action(); results.push({ name, status: 'PASS' }); }
try {
  let ready = false;
  for (let i = 0; i < 100; i++) {
    if (exited) throw new Error('Caddy exited before probes; inspect the isolated configuration with caddy validate');
    try { if ((await probe('user', '/')).status === 200) { ready = true; break; } } catch {}
    await new Promise(r => setTimeout(r, 100));
  }
  assert.ok(ready, 'Caddy did not become ready');
  for (const entry of ['user', 'rider', 'admin']) {
    await check(`${entry}: own entry, asset and CSP`, async () => { const r = await probe(entry, '/'); assert.equal(r.status, 200); assert.equal(r.body, `<main>${entry}</main>`); assert.match(r.headers['content-security-policy'], /script-src 'self'/); assert.equal((await probe(entry, `/assets/${entry}.js`)).status, 200); });
    for (const other of ['user', 'rider', 'admin'].filter(x => x !== entry)) await check(`${entry}: rejects ${other} HTML/assets`, async () => { for (const p of [`/${other}.html`, `/assets/${other}.js`]) assert.equal((await probe(entry, p)).status, 404); });
    await check(`${entry}: hides diagnostics and unversioned API`, async () => { for (const p of ['/healthz', '/readyz', '/metrics', '/debug/pprof/', '/api', '/api/v1', '/.env', '/demo/', '/.vite/manifest.json']) assert.equal((await probe(entry, p)).status, 404); });
  }
  await check('unknown Host rejected', async () => assert.equal((await probe('user', '/', { headers: { Host: `unknown.p07.test:${httpsPort}` } })).status, 421));
  await check('SNI/Host mismatch rejected', async () => assert.equal((await probe('user', '/', { headers: { Host: `rider.p07.test:${httpsPort}` } })).status, 421));
  await check('same-origin API and original Host', async () => { const r = await probe('user', '/api/v1/probe'); assert.equal(r.status, 200); assert.equal(JSON.parse(r.body).host, env.USER_HOST); });
  await check('cross-origin rejected before upstream', async () => { const count = upstreamRequests.length; assert.equal((await probe('user', '/api/v1/probe', { headers: { Origin: `https://${env.RIDER_HOST}` } })).status, 403); assert.equal(upstreamRequests.length, count); });
  await check('untrusted forwarding headers overwritten', async () => { const r = await probe('user', '/api/v1/probe', { headers: { Forwarded: 'for=192.0.2.1;host=evil.test', 'X-Forwarded-For': '192.0.2.1', 'X-Forwarded-Proto': 'http', 'X-Forwarded-Origin': 'https://evil.test' } }); const body = JSON.parse(r.body); assert.equal(body.forwarded, undefined); assert.equal(body.forwardedOrigin, undefined); assert.equal(body.xff, '127.0.0.1'); assert.equal(body.proto, 'https'); });
  await check('admin fails closed for unapproved direct peer', async () => { assert.equal((await probe('admin', '/', { localAddress: '127.0.0.2', headers: { 'X-Forwarded-For': '127.0.0.1' } })).status, 403); });
  await check('request body limit', async () => assert.equal((await probe('user', '/api/v1/probe', { body: 'x'.repeat(65537), headers: { 'Content-Type': 'application/json' } })).status, 413));
  await check('access and upstream error logs suppress PII, URI and authentication', async () => { const extra = { headers: { Authorization: `Bearer ${logMarker}`, Cookie: `sid=${logMarker}`, 'X-Person': logMarker } }; const markedPath = '/api/v1/probe?token=' + logMarker; await probe('user', markedPath, extra); await new Promise(r => upstream.close(r)); assert.equal((await probe('user', markedPath, extra)).status, 502); await new Promise(r => setTimeout(r, 200)); assert.ok(!output.includes(logMarker), 'log leaked synthetic sensitive marker'); const lines = output.split(/\r?\n/).filter(Boolean).map(line => { try { return JSON.parse(line); } catch { return {}; } }); const access = lines.filter(line => String(line.logger).startsWith('http.log.access')); assert.ok(access.length > 0, 'no access log observed'); assert.ok(access.every(line => !('request' in line) && !('resp_headers' in line))); assert.ok(lines.some(line => String(line.logger).startsWith('http.log.error')), 'no upstream error log observed'); });
  console.log(JSON.stringify({ status: 'PASS', passed: results.length, failed: 0, results }, null, 2));
} catch (error) {
  console.error(JSON.stringify({ status: 'FAIL', passed: results.length, failed: 1, reason: error.message, results }, null, 2));
  process.exitCode = 1;
} finally {
  edge.kill('SIGTERM');
  await new Promise(r => { if (exited) r(); else { edge.once('exit', r); setTimeout(r, 5000).unref(); } });
  await new Promise(r => upstream.close(r));
  // Retain sanitized operational logs only. Never dump raw captured headers on failure.
  writeFileSync(resolve(opts.work, 'edge-log-sanitized.jsonl'), output.split(/\r?\n/).filter(Boolean).flatMap(line => { try { const v = JSON.parse(line); return [JSON.stringify({ level: v.level, logger: v.logger, msg: v.msg, status: v.status })]; } catch { return []; } }).join('\n') + '\n');
}
