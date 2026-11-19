// Fast structural guard. Runtime behavior is checked by verify-edge.mjs.
import { readFileSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
const root = fileURLToPath(new URL('../', import.meta.url));
const read = name => readFileSync(root + name, 'utf8');
for (const name of ['Caddyfile', 'compose.dev.yaml', 'env.example', 'systemd/campus-api.service']) {
  assert.ok(existsSync(root + name), `missing ${name}`);
}
const caddy = read('Caddyfile');
for (const marker of ['strict_sni_host on', 'max_size 64KB', 'request delete', 'header_up Host {http.request.hostport}', 'remote_ip {$ADMIN_VPN_CIDRS:127.0.0.1/32 ::1/128}', 'respond "Unknown host" 421', 'handle /api/v1/*', 'respond "Not found" 404']) assert.ok(caddy.includes(marker), marker);
assert.ok(!caddy.includes('trusted_proxies'), 'Do not trust a forwarding hop without a separate reviewed topology');
const compose = read('compose.dev.yaml');
assert.ok(compose.includes('127.0.0.1:'), 'PG published only on loopback');
assert.ok(!/redis|container_name:|external:\s*true/i.test(compose));
const unit = read('systemd/campus-api.service');
for (const marker of ['User=campus', 'Group=campus', 'NoNewPrivileges=true', 'ProtectSystem=strict', 'EnvironmentFile=/etc/campus/api.env']) assert.ok(unit.includes(marker), marker);
assert.ok(!unit.includes('migration.env'), 'API may not load migration credentials');
const env = read('env.example');
for (const marker of ['HTTP_ADDR=127.0.0.1:8080', 'sslmode=verify-full', 'BALANCE_ENABLED=false', 'WECHAT_ENABLED=false', 'ALLOW_TEST_AUTH=false']) assert.ok(env.includes(marker), marker);
console.log('PASS: template contract (structural checks; HTTP behavior requires verify-edge.mjs)');
