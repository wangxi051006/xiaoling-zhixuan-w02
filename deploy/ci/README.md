# P07 CI / candidate preparation

Run from the integrated repository root. The source manifest remains P06's frozen candidate; P07 never patches the three HTML files. `node deploy/ci/gates.mjs wiring .` rejects `/src/shared/entry.ts` and any crossed entry. The controller must integrate each `/src/{user,rider,admin}/entry.ts` before complete candidate packaging or real browser business CI can pass.

## Status boundaries

- Local gate/SPDX unit tests and actual Node 26 report parsing have been exercised. Remote GitHub CI, required status checks, branch rules, and environment protection are **NOT RUN**. A YAML environment name does not configure approval protection.
- Windows business CI runs the existing `pwsh` certificate fixture, isolated PostgreSQL on its required `127.0.0.1:55497`, a checksum verified Chrome for Testing, and a synthetic issuer compiled in a separate test module copy. It is a required independent job. Linux does not silently skip this matrix.
- The fixed Windows PG 18.6 npm binary is the historical test binary family: its prior PG crash remains OPEN/UNCONFIRMED. Linux's separately pinned official PG 18.7 image is independent coverage; neither platform establishes production parity. Any intended PG major/minor change needs replay, invariant and recovery evidence on the deployment candidate.
- The frozen HTML wiring blocks formal full frontend preparation. Local rehearsal wiring is confined to a separate candidate copy and must use `CANDIDATE_ID=LOCAL-REHEARSAL-...`; such output carries that label in VERSION/SPDX. It cannot satisfy G3/G4.
- The coordinating agent observed a HIGH `go-jose/v4 v4.1.4` alert in its newer Trivy DB. CI includes no ignore or suppression. `govulncheck` symbol-level no finding does not override an unclosed HIGH package advisory; the backend/controller owns dependency remediation and re-test.

## Exact gates and evidence

The workflow fixes Go 1.27.4, Node 26.13.1/npm 11.20.0, GCC 13.3.0 (fail if runner drift), Chrome 159.0.8154.6, PostgreSQL image digest and scanner versions in `toolchain-lock.json`. GitHub actions use full immutable commit SHAs verified from their official tag APIs. Browser zip SHA-256 values were computed from the official downloads; scanner checksums came from official release asset digest metadata. Package downloads also retain npm integrity hashes.

Backend runs `go test ./...`, `go vet ./...`, `go test -race ./...`, and `go test -tags=integration ./...`. A separate required `go test -race -tags=integration -count=1 ./...` runs against real isolated PostgreSQL with `CGO_ENABLED=1` and the pinned C compiler. JSON output enables rejection of failed/skipped/zero matched tests. Explicit migration reruns require `TestG1CombinedMigrationPrefixUpgradeAndReplay` (historical W1 boundary) and `TestP05FullEmptyAndLegalPrefixUpgradeReplay` (full 000-060 chain with a clean schema, legal populated prefix upgrade, and unchanged existing rows on replay). The eight migration names and hashes in `deploy/ci/migration-lock.json` must be reviewed before freezing. The packager validates every source `*.sql` and every entry in the packaged `migrations/` directory against that lock before writing `VERSION.json`; invalid names, missing/extra entries, non-regular files, and changed bytes fail metadata generation.

Frontend retains `npm ci`, `npm run typecheck`, `npm test`, `npm run build`, `npm run test:e2e`. Its explicit P06 `node --test tests/p06-roles.e2e.mjs tests/p06-recovery.e2e.mjs` matrix requires at least 21 tests; Windows `node --test tests/p06-business.e2e.mjs` requires at least 16 including the wrapper. Explicit Node invocations add `--test-reporter=tap`; frozen npm scripts may use Node 26 spec output, which the gate also parses. Both formats require complete summaries and zero fail/cancel/skip/todo. Separate files keep MOCK, compiled entry, and real business layers distinct. `contracts/generate.mjs --check` checks OpenAPI plus generated Go/TS byte drift.

`scan.sh` collects redacted secret scans of the exact commit and full Git history, govulncheck JSON, npm audit including devDependencies and explicit picomatch result, source SPDX and Trivy source scan with `--include-dev-deps`. Scans continue after individual findings and the aggregate job fails. HIGH/CRITICAL are blocking. Scanner versions, scan time, Go DB stream and Trivy DB metadata remain in evidence; no `--ignore-unfixed` or exclusion of advisories is used.

Artifacts are `p07-backend-<sha>`, `p07-frontend-<sha>`, `p07-p06-real-windows-<sha>`, `p07-security-<sha>`. Evidence upload uses `always()` and missing expected files fails. Logs and screenshots contain synthetic test data only; the test TLS private key and fixture executable stay in runner temporary storage and are never packaged/uploaded.

## Protected manual preparation

Configure **both** `staging-preparation` and `production-preparation` as independent GitHub environments with required reviewers, no self-review, protected `main` deployment branches, and independently owned environment variables/secrets. Configure branch protection to require backend, frontend, p06-real-windows and security. Review these settings in the actual repository and save evidence before declaring protection active. This local task did not create a repository or change remote settings.

Only `workflow_dispatch`, `prepare_release=true`, `main`, all mandatory jobs green, and the selected environment's approval permit `release-preparation`. The selected context is recorded in VERSION. The job reads no production credentials, invokes no deployment action, creates no GitHub Release, and never publishes an image. A production context is preparation only; VERSION continues to say `productionRelease: NO-GO` pending external G3/G4/P08/G5 release authorization.

`bash deploy/ci/build-release.sh <new-dir>` builds Linux/amd64 with trimmed paths, readonly modules and an empty build ID; Vite emits a manifest; `deploy/scripts/split-static.mjs` copies only each endpoint's owned transitive asset closure. Output is `bin/campus-api`, `static/{user,rider,admin}/`, migrations, VERSION, dependency SPDX, inventory and SHA256SUMS. No source tree/demo/mock/tests/synthetic issuer/credential file is copied. The SPDX includes actual linked Go modules and non-dev locked npm dependencies, with a fixed source timestamp and deterministic IDs.

Preparation builds twice and diffs every byte before scanning the release directory **and** its SPDX. GNU tar uses sorted paths, fixed timestamps/owner/group; gzip uses `-n`. The resulting `p07-prepared-candidate-<sha>` includes `xiaoling-candidate.tar.gz`, archive SHA256, VERSION, migration list/hashes, SPDX, per-file hashes and scan reports. Reproducibility and scanner execution are not declared PASS until the actual integrated candidate run completes.

Local commands that do not need PostgreSQL or remote CI:

```text
node --test deploy/ci/gates.test.mjs deploy/ci/sbom.test.mjs deploy/scripts/split-static.test.mjs
node deploy/scripts/check-template-contract.mjs
node deploy/ci/gates.mjs wiring .
node contracts/generate.mjs --check
actionlint -shellcheck= -pyflakes= .github/workflows/ci.yml
```

The wiring command is an expected FAIL on the frozen unintegrated P06 source. Do not convert it to PASS by modifying source inside the release job. Deployment, real OTP/IdP/MFA, notification delivery, PITR, live upgrade/cutback, production-equivalent concurrency and signed release gates remain owned by the runbooks and controller.
