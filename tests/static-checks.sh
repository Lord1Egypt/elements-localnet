#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
failures=0
fail() { echo "FAIL $*" >&2; failures=$((failures + 1)); }
pass() { echo "PASS $*"; }

while IFS= read -r script; do
  bash -n "${script}" || fail "bash syntax: ${script#${ROOT_DIR}/}"
  grep -q '^set -Eeuo pipefail$' "${script}" || fail "strict mode: ${script#${ROOT_DIR}/}"
done < <(find "${ROOT_DIR}" -type f -name '*.sh' -print)
((failures == 0)) && pass "bash syntax and strict mode"

if rg -n 'verification\.threads|con_dyna_deploy_start|docker-compose|/var/run/docker\.sock|0\.0\.0\.0:[0-9]+:7040' "${ROOT_DIR}" \
  -g '!*.md' -g '!tests/static-checks.sh' -g '!tests/dashboard-check.sh'; then
  fail "forbidden obsolete/exposure patterns"
else
  pass "no obsolete options, legacy Compose, Docker socket, or public RPC mapping"
fi

if [[ -f "${ROOT_DIR}/compose.yaml" ]]; then
  docker compose -f "${ROOT_DIR}/compose.yaml" config --quiet || fail "docker compose config"
  ((failures == 0)) && pass "docker compose config"
fi

if command -v shellcheck >/dev/null; then
  mapfile -t scripts < <(find "${ROOT_DIR}" -type f -name '*.sh' -print)
  shellcheck "${scripts[@]}" || fail "shellcheck"
  ((failures == 0)) && pass "shellcheck"
else
  echo "SKIP shellcheck (not installed)"
fi

((failures == 0)) || exit 1
