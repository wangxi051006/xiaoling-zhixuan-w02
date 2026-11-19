#!/usr/bin/env bash
set -euo pipefail
mkdir -p evidence/security
failed=0
record() { "$@" || { code=$?; printf '%s exit=%s\n' "$1" "$code" >> evidence/security/failures.txt; failed=1; }; }
date --iso-8601=seconds > evidence/security/scanned-at.txt
go version > evidence/security/tools.txt
node --version >> evidence/security/tools.txt
npm --version >> evidence/security/tools.txt
gitleaks version >> evidence/security/tools.txt
syft version >> evidence/security/tools.txt
trivy --version >> evidence/security/tools.txt
govulncheck -version >> evidence/security/tools.txt
# Archive exactly the checked-out commit, excluding downloaded dependencies/evidence.
source_tree="$(mktemp -d "${RUNNER_TEMP:-/tmp}/p07-source.XXXXXX")"
trap 'rm -rf -- "$source_tree"' EXIT
git archive HEAD | tar -xf - -C "$source_tree"
# Candidate source and complete fetched Git history are both scanned, with redaction.
record gitleaks dir "$source_tree" --redact --report-format json --report-path evidence/security/secrets-tree.json
record gitleaks git . --redact --report-format json --report-path evidence/security/secrets-history.json
if ! (cd backend && govulncheck -json ./...) > evidence/security/govulncheck.json; then failed=1; fi
# Audit includes devDependencies; moderate findings remain visible, HIGH/CRITICAL block.
set +e
(cd web && npm audit --include=dev --include=optional --json) > evidence/security/npm-audit.json
audit_exit=$?
set -e
if test "$audit_exit" -gt 1; then failed=1; fi
if ! node deploy/ci/gates.mjs audit evidence/security/npm-audit.json > evidence/security/npm-audit-gate.json; then failed=1; fi
# Separate source SBOM includes the entire dependency graph, including lockfile dependencies.
record syft scan "dir:$source_tree" -o spdx-json=evidence/security/source.spdx.json
record trivy fs --include-dev-deps --scanners vuln,secret --severity HIGH,CRITICAL --exit-code 1 --format json --output evidence/security/source-scan.json "$source_tree"
trivy --version > evidence/security/vulnerability-db.txt
exit "$failed"
