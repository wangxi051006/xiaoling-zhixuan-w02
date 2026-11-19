#!/usr/bin/env bash
set -euo pipefail
# Run from repository root. This prepares local artifacts; it never deploys or publishes.
: "${CANDIDATE_ID:?candidate ID required}" "${SOURCE_DATE_EPOCH:?fixed source epoch required}"
out="${1:?new output directory required}"
test ! -e "$out"
node deploy/ci/gates.mjs wiring .
node contracts/generate.mjs --check
mkdir -p "$out/bin" "$out/migrations"
export TZ=UTC LC_ALL=C CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=local
binary="$(realpath "$out/bin/campus-api")"
(cd backend && go build -mod=readonly -trimpath -buildvcs=false -ldflags='-buildid=' -o "$binary" ./cmd/api)
(cd web && npm run build -- --manifest)
node deploy/scripts/split-static.mjs --dist web/dist --out "$out/static"
cp backend/migrations/*.sql "$out/migrations/"
node deploy/ci/release-manifest.mjs metadata "$out"
node deploy/ci/sbom.mjs "$out"
node deploy/ci/release-manifest.mjs inventory "$out"
