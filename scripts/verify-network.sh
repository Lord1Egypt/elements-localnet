#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOT_DIR}/generated/network.env"
[[ -f "${ENV_FILE}" ]] || { echo "FAIL setup: generated/network.env is missing" >&2; exit 1; }
# shellcheck disable=SC1090
source "${ENV_FILE}"
NETWORK_NAME="${NETWORK_NAME:-elements-localnet}"
COMPOSE=(docker compose -f "${ROOT_DIR}/compose.yaml")
CLI=(elements-cli -chain=elements -datadir=/data -conf=/config/elements.conf)
acceptance="no"
[[ "${1:-}" == "--acceptance" ]] && acceptance="yes"
failures=0

pass() { printf 'PASS %-28s %s\n' "$1" "${2:-}"; }
fail() { printf 'FAIL %-28s %s\n' "$1" "${2:-}" >&2; failures=$((failures + 1)); }
rpc() { local node="$1"; shift; "${COMPOSE[@]}" exec -T "${node}" "${CLI[@]}" "$@"; }
wallet_rpc() { rpc node-01 -rpcwallet=payout "$@"; }
wallet_loaded="no"
cleanup() {
  if [[ "${wallet_loaded}" == "yes" ]]; then rpc node-01 unloadwallet payout >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT

if "${COMPOSE[@]}" config --quiet; then pass "compose config"; else fail "compose config"; fi
version="$(rpc node-01 getnetworkinfo 2>/dev/null | jq -r '.subversion' || true)"
[[ "${version}" == *"23.3.4"* ]] && pass "Elements version" "${version}" || fail "Elements version" "${version:-unavailable}"

genesis=""
heights=()
for ((i = 1; i <= NODE_COUNT; i++)); do
  node="$(printf 'node-%02d' "${i}")"
  health="$(docker inspect --format '{{.State.Health.Status}}' "${NETWORK_NAME}-${node}" 2>/dev/null || true)"
  for attempt in {1..30}; do
    [[ "${health}" == "healthy" ]] && break
    sleep 1
    health="$(docker inspect --format '{{.State.Health.Status}}' "${NETWORK_NAME}-${node}" 2>/dev/null || true)"
  done
  [[ "${health}" == "healthy" ]] && pass "${node} healthy" || fail "${node} healthy" "status=${health:-missing}"
  node_genesis="$(rpc "${node}" getblockhash 0 2>/dev/null || true)"
  if [[ -z "${genesis}" ]]; then genesis="${node_genesis}"; fi
  [[ -n "${node_genesis}" && "${node_genesis}" == "${genesis}" ]] && pass "${node} genesis" "${node_genesis}" || fail "${node} genesis"
  peers="$(rpc "${node}" getconnectioncount 2>/dev/null || echo 0)"
  if ((NODE_COUNT > 1)); then
    for attempt in {1..40}; do
      ((peers > 0)) && break
      sleep 2
      peers="$(rpc "${node}" getconnectioncount 2>/dev/null || echo 0)"
    done
  fi
  if ((NODE_COUNT == 1)); then pass "${node} peers" "single-node mode";
  elif ((peers > 0)); then pass "${node} peers" "${peers}"; else fail "${node} peers" "0"; fi
  heights+=("$(rpc "${node}" getblockcount 2>/dev/null || echo -1)")
done

for attempt in {1..30}; do
  heights=()
  for ((i = 1; i <= NODE_COUNT; i++)); do heights+=("$(rpc "$(printf 'node-%02d' "${i}")" getblockcount)"); done
  unique="$(printf '%s\n' "${heights[@]}" | sort -u | wc -l | tr -d ' ')"
  ((unique == 1)) && break
  sleep 2
done
((unique == 1)) && pass "matching heights" "${heights[0]}" || fail "matching heights" "${heights[*]}"

producer_count="$(docker ps --format '{{.Names}}' | awk -v name="${NETWORK_NAME}-producer" '$0 == name {n++} END {print n+0}')"
if [[ "${AUTO_MINE}" == "yes" ]]; then
  ((producer_count == 1)) && pass "single producer" || fail "single producer" "running=${producer_count}"
else
  ((producer_count <= 1)) && pass "at most one producer" "running=${producer_count}" || fail "at most one producer" "running=${producer_count}"
fi

loaded="$(rpc node-01 listwallets 2>/dev/null | jq -c . || true)"
[[ "${loaded}" == "[]" ]] && pass "wallets unloaded" || fail "wallets unloaded" "${loaded}"
if git -C "${ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  tracked_secrets="$(git -C "${ROOT_DIR}" ls-files 'generated/**' | grep -v '^generated/.gitkeep$' || true)"
  [[ -z "${tracked_secrets}" ]] && pass "no tracked secrets" || fail "no tracked secrets" "tracked generated files exist"
else
  pass "no tracked secrets" "not a Git worktree; generated/ is ignored"
fi

if [[ "${acceptance}" == "yes" ]]; then
  command -v jq >/dev/null || { fail "acceptance dependencies" "jq is required"; acceptance="no"; }
fi

if [[ "${acceptance}" == "yes" ]]; then
  producer_was_running="no"
  if ((producer_count == 1)); then
    producer_was_running="yes"
    "${COMPOSE[@]}" --profile producer stop producer >/dev/null
  fi
  address="$(tr -d '\r\n' <"${ROOT_DIR}/generated/public/payout-address")"
  height="$(rpc node-01 getblockcount)"
  if ((height == 0)); then rpc node-01 generatetoaddress 1 "${address}" >/dev/null; height=1; fi

  block1_hash="$(rpc node-01 getblockhash 1)"
  block1="$(rpc node-01 getblock "${block1_hash}" 2)"
  reward="$(jq -r --arg address "${address}" '[.tx[0].vout[] | select(.scriptPubKey.address == $address) | .value] | add // 0' <<<"${block1}")"
  [[ "${reward}" == "50.00000000" || "${reward}" == "50" ]] && pass "50-unit first reward" "block=${block1_hash}" || fail "50-unit first reward" "value=${reward}"

  rpc node-01 loadwallet payout >/dev/null
  wallet_loaded="yes"
  balances="$(wallet_rpc getbalances)"
  immature="$(jq -r '.mine.immature | if type == "object" then ([.. | numbers] | add // 0) else (. // 0) end' <<<"${balances}")"
  if ((height < 101)) && awk -v n="${immature}" 'BEGIN {exit !(n > 0)}'; then
    pass "first reward immature" "immature=${immature}"
  elif ((height >= 101)); then
    fail "first reward immature" "chain already at height ${height}; use a clean network for this assertion"
  else
    fail "first reward immature" "immature=${immature}"
  fi
  rpc node-01 unloadwallet payout >/dev/null
  wallet_loaded="no"

  if ((height < 101)); then rpc node-01 generatetoaddress "$((101 - height))" "${address}" >/dev/null; fi
  for attempt in {1..30}; do
    all_101="yes"
    for ((i = 1; i <= NODE_COUNT; i++)); do
      h="$(rpc "$(printf 'node-%02d' "${i}")" getblockcount)"
      ((h >= 101)) || all_101="no"
    done
    [[ "${all_101}" == "yes" ]] && break
    sleep 2
  done
  [[ "${all_101}" == "yes" ]] && pass "101 blocks synchronized" || fail "101 blocks synchronized"

  rpc node-01 loadwallet payout >/dev/null
  wallet_loaded="yes"
  balances="$(wallet_rpc getbalances)"
  trusted="$(jq -r '.mine.trusted | if type == "object" then ([.. | numbers] | add // 0) else (. // 0) end' <<<"${balances}")"
  if awk -v n="${trusted}" 'BEGIN {exit !(n >= 50)}'; then pass "first reward spendable" "trusted=${trusted}"; else fail "first reward spendable" "trusted=${trusted}"; fi
  rpc node-01 unloadwallet payout >/dev/null
  wallet_loaded="no"
  [[ "$(rpc node-01 listwallets | jq -c .)" == "[]" ]] && pass "wallet unloaded after test" || fail "wallet unloaded after test"

  before_restart="$(rpc node-01 getblockcount)"
  nodes=(); for ((i = 1; i <= NODE_COUNT; i++)); do nodes+=("$(printf 'node-%02d' "${i}")"); done
  "${COMPOSE[@]}" restart "${nodes[@]}" >/dev/null
  for node in "${nodes[@]}"; do "${ROOT_DIR}/scripts/wait-for-node.sh" "${node}" 180 >/dev/null; done
  after_restart="$(rpc node-01 getblockcount)"
  [[ "${before_restart}" == "${after_restart}" ]] && pass "blockchain persistence" "height=${after_restart}" || fail "blockchain persistence" "before=${before_restart} after=${after_restart}"
  restart_match="yes"
  for node in "${nodes[@]}"; do [[ "$(rpc "${node}" getblockcount)" == "${after_restart}" ]] || restart_match="no"; done
  [[ "${restart_match}" == "yes" ]] && pass "heights after restart" "${after_restart}" || fail "heights after restart"
  [[ "$(rpc node-01 listwallets | jq -c .)" == "[]" ]] && pass "walletless normal operation" || fail "walletless normal operation"
  if [[ "${producer_was_running}" == "yes" ]]; then "${COMPOSE[@]}" --profile producer up -d producer >/dev/null; fi
fi

if ((failures > 0)); then
  echo "Verification failed: ${failures} check(s)." >&2
  exit 1
fi
echo "Verification passed."
