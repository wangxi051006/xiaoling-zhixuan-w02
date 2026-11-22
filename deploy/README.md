# P07 deployment templates

These files are a reviewable candidate, not authorization to deploy. The runtime is a single non-root Go process on `127.0.0.1:8080`, with its durable order worker in the same process. Direct Caddy TLS terminates three distinct HTTPS hosts. PostgreSQL is a separately provisioned same-region private service, with `sslmode=verify-full`, a trusted CA file, private DNS and outbound firewall controls. Private routing, actual certificate trust/renewal and staged operational drills require environment evidence; the example hostname does not prove those controls.

The API serves uploaded product images from the persistent systemd state directory at `/var/lib/campus-api/product-images`. Include this directory with database backups. For development the default is `product-images` under the API working directory. `PRODUCT_IMAGE_DIR` can point to a different writable directory.

## Configuration and installation contract

`env.example` is divided into separate API, migration, Caddy and development-only sections. Never source the entire file. API names exactly match `bootstrap.LoadConfig`; `APP_ENV=production` is the actual production value. The runtime unit loads only `/etc/campus/api.env`, owned by root with mode 0600, and runs `/opt/campus/current/bin/campus-api` as the pre-created `campus` user/group. A root-owned release tree is readable/executable by service users and never writable by them. Environment files, deployment tools and backup credentials are outside the release artifact. systemd reads the environment file before switching user. PostgreSQL CA material must be readable by the applicable service users.

Use a separate short-lived `campus-migrate` OS user and `/etc/campus/migration.env` with all required runtime configuration plus `MIGRATION_DATABASE_URL`. Execute the same binary with `migrate` through an approved one-shot unit that loads this file. The application and migration database usernames must differ. The resident API unit has no migration environment or `ExecStartPre` migration. Bootstrap rejects a reused production database username. Backup/WAL credentials are independently scoped. After migration `090_001_station_delivery_fee.sql`, the schema owner grants the application role `UPDATE (delivery_fee_fen, delivery_fee_version)` on `stations`. After `100_001_order_batches.sql`, grant the application role `UPDATE (merchant_batch_limit, rider_active_limit, order_limits_version)` on `stations` and `SELECT (merchant_accepted_at)` and `UPDATE (merchant_accepted_at)` on `orders`. The P00 template already grants `SELECT` on `stations`. A complete minimal runtime grant matrix for migrations 010–050 remains with P00/P01–P05; do not replace it with blanket table write/delete privileges.

Caddy loads `/etc/campus/caddy.env`. `USER_HOST`, `RIDER_HOST` and `ADMIN_HOST` are distinct lowercase names, without the `https://` prefix or `:443`; API origins are exactly `https://` plus these names. Test-only nondefault ports must agree on both sides. The explicit PEM certificate/key covers all three names and is renewed outside this template; certificates and keys are never artifacts. `STATIC_ROOT` defaults to `/opt/campus/current/static`, `API_UPSTREAM` to `127.0.0.1:8080`, and `EDGE_BIND` to `0.0.0.0`. IPv6 bindings and gateways require reviewed environment configuration. Caddy's administrative endpoint is disabled. HTTP returns 421; unknown HTTPS Host and SNI/Host disagreement are rejected. Only `/api/v1/*` is proxied and the original Host is preserved. Health, metrics, pprof, `.env`, manifest and arbitrary files are never exposed through the edge.

When enabling the merchant app, include `MERCHANT_HOST` in the same certificate and place the merchant build under `static/merchant`. The merchant and rider hosts also serve their own `/.well-known/assetlinks.json` for Android app verification. See [client builds](../apps/README.md).

`ADMIN_VPN_CIDRS` defaults to `127.0.0.1/32 ::1/128`; it matches the direct peer, never a forwarded address. Supply approved VPN client source ranges only after verifying routing. A generic public reverse proxy that hides client sources is not compatible with this direct-edge policy. Do not trust it by merely adding forwarded headers. Caddy overwrites its standard forwarding headers and removes `Forwarded` and unrelated origin/URL override headers. No CORS is enabled; a supplied API Origin must match the current HTTPS host.

Requests are limited to 64 KiB, with a 5 MiB limit for the product image upload route. Headers are limited to 32 KiB, header/body reads to 5/10 seconds, writes to 15 seconds and idle connections to 60 seconds; upstream connect/header/read/write limits are explicit. The CSP permits same-origin scripts and API, denies framing, objects and base URLs, and permits inline CSS for the inherited prototype styles. Inline executable scripts/event attributes are rejected during static packaging. Access and runtime error logs remove the complete request/response-header objects, including peer IP, URI/query, cookie and authorization. Log retention/access and a separate restricted audit copy belong to the environment runbooks; these logs are not a substitute for the database audit trail.

Caddy core has no generic rate limiter in this configuration. SMS and email challenge throttling use Go `RemoteAddr`; behind Caddy this aggregates clients into the loopback peer. Keep public email access disabled until a reviewed client-source and ingress rate-limit design is verified. The SMS provider is not injected by `cmd/api`. In the A01 candidate, staff login and provisioning require the complete `STAFF_OIDC_*` configuration; all keys absent leaves both unavailable. Email is opt-in via the `EMAIL_*` configuration. Real IdP/MFA and SMTP delivery still require approved staged tests. Production balance and WeChat stay off; new orders/dispatch default off until an approved staged check. The API reads switches on startup, so a controlled restart is required.

## Static artifacts

Build the approved, wired candidate with `npm run build -- --manifest` in `web/`. Then, from the repository root:

```text
node deploy/scripts/split-static.mjs --dist web/dist --out /new/release/static
```

Output is `static/{user,rider,admin}/`, each with only its own HTML and recursively required manifest `imports`, `dynamicImports`, `css` and `assets`. Shared dependencies are copied into each needed directory; no common web root is exposed. `static/artifact-layout.json` records per-entry SHA-256 files, remains outside all served roots, and supports checks of byte-identical shared dependencies. The source `.vite` directory and other entry HTML are not copied. Output must not exist. Invalid paths, external/unlisted HTML resources, inline executable scripts and incomplete dependency graphs fail before output creation. Frontend wiring, API contracts and browser business tests are separate mandatory gates. The default P06 sealed HTML is still a placeholder and must not be published merely because its build succeeds.

## Development PostgreSQL only

`compose.dev.yaml` runs only an isolated development PostgreSQL, with a pinned official PostgreSQL 18 image digest and the PostgreSQL 18+ versioned-data parent volume. It publishes only on IPv4 loopback; its internal network, database, role and project volume are nonproduction. It has no Redis, API, edge, production TLS or backup topology. The bootstrap owner is a dev convenience and must not become the production runtime role. Supply a new nonproduction password file, an unused port and a unique project name. Do not reuse another chat's database/project. `config` does not start or test an image.

```text
docker compose -p p07-dev-UNIQUE -f deploy/compose.dev.yaml config
```

`DEV_PG_PORT` and `DEV_PG_PASSWORD_FILE` are mandatory. On hosts without a Docker daemon, the standalone Compose plugin executable can parse the same command arguments without the `docker compose` prefix. Starting/tearing down this project is a separate explicit action; remove only its own volumes after saving required evidence.

## Isolated verification

```text
node deploy/scripts/check-template-contract.mjs
node --test deploy/scripts/split-static.test.mjs
caddy validate --config deploy/Caddyfile --adapter caddyfile
pwsh -File deploy/scripts/new-edge-test-cert.ps1 -OutDirectory /new/isolated/test-cert
node deploy/scripts/verify-edge.mjs --caddy /path/to/caddy --cert /new/isolated/test-cert/cert.pem --key /new/isolated/test-cert/key.pem --work /new/isolated/edge-probe
```

For `caddy validate`, supply three `.p07.test` hosts and the test certificate paths through the Caddy environment section; use unused test ports. The certificate helper creates a four-hour self-signed fixture using .NET, without changing the certificate store or hosts file. `verify-edge.mjs` trusts only its supplied fixture CA for its requests, binds Caddy and a mock upstream to loopback on random unused ports, never connects a database, and stops both child services. It isolates Caddy's data/config paths under the new work directory. Test keys remain local fixtures and must never enter publication bundles. Sanitized operational log records remain in the work directory.

The HTTP test covers each HTML and required asset root, other-entry HTML/assets rejection, unknown Host, strict SNI, default management denial including forged forwarded IP, hidden diagnostic routes, original Host, same-origin API, forwarding-header replacement, body limits and absence of a randomly generated synthetic secret marker in both access and 502 error logs. This is an edge behavior test against a stub, not a real login/business or preproduction acceptance result. Linux systemd hardening needs a Linux host and is not exercised by Windows syntax/config checks.

The first real HTTP run exposed a Caddy directive-order issue: an outer management `respond` was evaluated after `route`. Its FAIL evidence is retained. The guard now runs at the start of each entry route, and reruns passed. Preserve both first failure and rerun when collecting evidence.

## Primary tool references

Consulted on 2026-11-19 Asia/Shanghai: Caddy global options (`https://caddyserver.com/docs/caddyfile/options`), reverse proxy (`https://caddyserver.com/docs/caddyfile/directives/reverse_proxy`), and filter logging (`https://caddyserver.com/docs/caddyfile/directives/log`); systemd execution hardening (`https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html`); official PostgreSQL image secrets and 18+ volume layout (`https://raw.githubusercontent.com/docker-library/docs/master/postgres/README.md`); official Compose specification ports, host IP and internal network definitions (`https://raw.githubusercontent.com/compose-spec/compose-spec/main/spec.md`). Docker Compose networking documentation fetch encountered TLS failure in this environment; the primary specification fallback succeeded. Actual Compose 5.8.0 config parsing is recorded separately as local syntax evidence. Tool/docs checks do not sign G3/G4/G5.
