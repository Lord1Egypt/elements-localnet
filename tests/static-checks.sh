#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
failures=0
fail() { echo "FAIL $*" >&2; failures=$((failures + 1)); }
pass() { echo "PASS $*"; }

# Pattern scanning must never report PASS just because the scanner is missing.
# ripgrep is preferred; grep is a tested fallback. Both report "no match" as
# exit status 1 and a real error as 2 or above, which is checked explicitly.
if command -v rg >/dev/null; then
  SCANNER="rg"
elif command -v grep >/dev/null; then
  SCANNER="grep"
else
  echo "FAIL scanner: neither ripgrep nor grep is installed; pattern checks cannot run" >&2
  exit 1
fi

scan() {
  # scan PATTERN ROOT -> prints matches, exit 0 match / 1 no match / >=2 error
  local pattern="$1" root="$2"
  if [[ "${SCANNER}" == "rg" ]]; then
    rg -n --no-messages \
      --glob '!.git/**' --glob '!legacy/**' --glob '!generated/**' \
      --glob '!*.md' --glob '!tests/static-checks.sh' --glob '!tests/dashboard-check.sh' \
      -- "${pattern}" "${root}"
  else
    grep -R -n -E --binary-files=without-match \
      --exclude='*.md' --exclude='static-checks.sh' --exclude='dashboard-check.sh' \
      --exclude-dir=.git --exclude-dir=legacy --exclude-dir=generated \
      -- "${pattern}" "${root}"
  fi
}

# Self-test of the active scanner, including the grep fallback, so a broken or
# absent scanner cannot silently turn every pattern check into a PASS.
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT
mkdir -p "${fixture}/clean"
printf 'harmless=1\n' >"${fixture}/clean/ok.conf"
mkdir -p "${fixture}/dirty"
printf 'volumes:\n  - /var/run/docker.sock:/var/run/docker.sock\n' >"${fixture}/dirty/bad.yaml"

scanner_ok="yes"
scan 'docker\.sock' "${fixture}/dirty" >/dev/null 2>&1 || scanner_ok="no"
if [[ "${scanner_ok}" != "yes" ]]; then
  fail "scanner self-test (${SCANNER}): a known forbidden pattern was not detected"
fi
set +e
scan 'docker\.sock' "${fixture}/clean" >/dev/null 2>&1
clean_status=$?
set -e
if ((clean_status != 1)); then
  fail "scanner self-test (${SCANNER}): a clean tree returned status ${clean_status} instead of 1"
fi
((failures == 0)) && pass "scanner self-test (${SCANNER})"

while IFS= read -r script; do
  bash -n "${script}" || fail "bash syntax: ${script#"${ROOT_DIR}"/}"
  grep -q '^set -Eeuo pipefail$' "${script}" || fail "strict mode: ${script#"${ROOT_DIR}"/}"
done < <(find "${ROOT_DIR}" -type f -name '*.sh' -not -path '*/legacy/*' -print)
((failures == 0)) && pass "bash syntax and strict mode"

FORBIDDEN='verification\.threads|con_dyna_deploy_start|docker-compose|/var/run/docker\.sock|0\.0\.0\.0:[0-9]+:7040'
set +e
forbidden_output="$(scan "${FORBIDDEN}" "${ROOT_DIR}")"
forbidden_status=$?
set -e
case "${forbidden_status}" in
  0) fail "forbidden obsolete/exposure patterns"; printf '%s\n' "${forbidden_output}" >&2 ;;
  1) pass "no obsolete options, legacy Compose, Docker socket, or public RPC mapping" ;;
  *) fail "pattern scan failed with status ${forbidden_status} using ${SCANNER}" ;;
esac

if [[ -f "${ROOT_DIR}/compose.yaml" ]]; then
  docker compose -f "${ROOT_DIR}/compose.yaml" config --quiet || fail "docker compose config"
  rendered="$(docker compose -f "${ROOT_DIR}/compose.yaml" config 2>/dev/null || true)"

  grep -qE 'published:\s*"?8080"?' <<<"${rendered}" \
    && grep -qE 'host_ip:\s*127\.0\.0\.1' <<<"${rendered}" \
    && pass "explorer port is published on loopback" \
    || fail "explorer port is not published on 127.0.0.1"

  if grep -qE 'host_ip:\s*0\.0\.0\.0' <<<"${rendered}"; then
    if [[ "$(awk -F= '$1 == "P2P_EXPOSURE" {print $2}' "${ROOT_DIR}/generated/network.env" 2>/dev/null)" == "lan" ]]; then
      pass "P2P is published on all interfaces because P2P_EXPOSURE=lan was chosen deliberately"
    else
      fail "a port is published on 0.0.0.0 without P2P_EXPOSURE=lan"
    fi
  else
    pass "no service is published on 0.0.0.0"
  fi

  grep -q 'payout-wallet.backup' <<<"${rendered}" && fail "a wallet backup is mounted into a container" \
    || pass "no wallet backup is mounted"
  grep -qE '/run/explorer-secrets.*:ro|read_only:\s*true' <<<"${rendered}" \
    && pass "explorer credentials are mounted read-only" \
    || fail "explorer credential mount is not read-only"
fi

if command -v shellcheck >/dev/null; then
  mapfile -t scripts < <(find "${ROOT_DIR}" -type f -name '*.sh' -not -path '*/legacy/*' -print)
  shellcheck "${scripts[@]}" || fail "shellcheck"
  ((failures == 0)) && pass "shellcheck"
else
  echo "SKIP shellcheck (not installed)"
fi

((failures == 0)) || exit 1
