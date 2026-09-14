#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "${ROOT_DIR}/generated/network.env"
NETWORK_NAME="${NETWORK_NAME:-elements-localnet}"
DASHBOARD_HOST_PORT="${DASHBOARD_HOST_PORT:-8080}"
BASE="http://127.0.0.1:${DASHBOARD_HOST_PORT}"
cd "${ROOT_DIR}"
failures=0
pass() { echo "PASS $*"; }
fail() { echo "FAIL $*" >&2; failures=$((failures + 1)); }
skip() { echo "SKIP $*"; }

command -v jq >/dev/null || { echo "jq is required." >&2; exit 1; }
cli() { docker compose exec -T "$1" elements-cli -chain=elements -datadir=/data -conf=/config/elements.conf "${@:2}"; }

# ---------------------------------------------------------------- telemetry

[[ "$(curl -fsS "${BASE}/healthz" | jq -r .status)" == "ok" ]] && pass healthz || fail healthz
curl -fsS -o /dev/null "${BASE}/readyz" && pass readyz || fail readyz

snapshot="$(curl -fsS "${BASE}/api/v1/network")"
[[ "$(jq '.nodes | length' <<<"${snapshot}")" == "${NODE_COUNT}" ]] && pass "dynamic node count" || fail "dynamic node count"
[[ "$(jq '[.nodes[] | select(.state != "HEALTHY")] | length' <<<"${snapshot}")" == "0" ]] && pass "all nodes healthy" || fail "all nodes healthy"
[[ "$(jq -r '.nodes[] | select(.id == "node-01") | .role' <<<"${snapshot}")" == "producer" ]] && pass "producer role" || fail "producer role"
if ((NODE_COUNT >= 2)); then
  [[ "$(jq '[.nodes[] | select(.role == "validator")] | length' <<<"${snapshot}")" == "$((NODE_COUNT - 1))" ]] && pass "validator roles" || fail "validator roles"
  [[ "$(jq -r '.nodes[] | select(.id == "node-02") | .capabilities | index("archive") != null' <<<"${snapshot}")" == "true" ]] && pass "node-02 archive capability" || fail "node-02 archive capability"
fi
[[ "$(jq -r '.producer.blockInterval' <<<"${snapshot}")" == "${BLOCK_INTERVAL}" ]] && pass "producer interval" || fail "producer interval"

api_height="$(jq -r .network.canonicalHeight <<<"${snapshot}")"
api_hash="$(jq -r .network.canonicalBestBlockHash <<<"${snapshot}")"
direct_height="$(cli node-01 getblockcount)"
direct_hash="$(cli node-01 getblockhash "${api_height}")"
if [[ "${direct_hash}" == "${api_hash}" ]] && ((direct_height >= api_height && direct_height - api_height <= 3)); then
  pass "direct RPC height/hash match"
else
  fail "direct RPC height/hash match"
fi

for endpoint in nodes topology producer economics; do
  curl -fsS -o /dev/null "${BASE}/api/v1/${endpoint}" && pass "endpoint ${endpoint}" || fail "endpoint ${endpoint}"
done
[[ "$(curl -sS -o /dev/null -w '%{http_code}' "${BASE}/api/v1/nodes/not-configured")" == "404" ]] && pass "unknown node rejected" || fail "unknown node rejected"

# ----------------------------------------------------------------- explorer

status="$(curl -fsS "${BASE}/api/v1/explorer/status")"
indexed="$(jq -r .indexer.indexedHeight <<<"${status}")"
chain_height="$(jq -r .indexer.chainHeight <<<"${status}")"
pass "indexer progress" "indexed=${indexed} chain=${chain_height} lag=$(jq -r .indexer.lagBlocks <<<"${status}")"
[[ "$(jq -r .indexer.schemaVersion <<<"${status}")" =~ ^[0-9]+$ ]] && pass "index schema version reported" || fail "index schema version reported"

if ((indexed < 0)); then
  skip "indexed-chain comparisons (the index is still empty)"
else
  genesis_indexed="$(curl -fsS "${BASE}/api/v1/blocks/0" | jq -r .hash)"
  [[ "${genesis_indexed}" == "$(cli node-02 getblockhash 0)" ]] && pass "indexed genesis hash matches RPC" || fail "indexed genesis hash matches RPC"

  tip_indexed="$(curl -fsS "${BASE}/api/v1/blocks/${indexed}" | jq -r .hash)"
  [[ "${tip_indexed}" == "$(cli node-02 getblockhash "${indexed}")" ]] && pass "indexed tip hash matches RPC" || fail "indexed tip hash matches RPC"

  mismatches=0
  for _ in 1 2 3 4 5; do
    sample=$((RANDOM % (indexed + 1)))
    indexed_block="$(curl -fsS "${BASE}/api/v1/blocks/${sample}")"
    rpc_hash="$(cli node-02 getblockhash "${sample}")"
    rpc_txs="$(cli node-02 getblock "${rpc_hash}" 1 | jq -r .nTx)"
    [[ "$(jq -r .hash <<<"${indexed_block}")" == "${rpc_hash}" ]] || mismatches=$((mismatches + 1))
    [[ "$(jq -r .txCount <<<"${indexed_block}")" == "${rpc_txs}" ]] || mismatches=$((mismatches + 1))
  done
  ((mismatches == 0)) && pass "sampled block hashes and transaction counts match RPC" || fail "sampled block comparison" "${mismatches} mismatches"

  [[ "$(curl -sS -o /dev/null -w '%{http_code}' "${BASE}/api/v1/blocks/${indexed}?limit=500")" == "200" ]] && pass "oversized page limit is clamped" || fail "oversized page limit is clamped"
  [[ "$(jq -r '.transactionPage.limit' <<<"$(curl -fsS "${BASE}/api/v1/blocks/${indexed}?limit=500")")" == "100" ]] && pass "page limit capped at 100" || fail "page limit capped at 100"
  [[ "$(curl -sS -o /dev/null -w '%{http_code}' "${BASE}/api/v1/blocks/${indexed}?limit=0")" == "400" ]] && pass "invalid page limit rejected" || fail "invalid page limit rejected"
fi

for endpoint in "explorer/status" "blocks?limit=5" "assets?limit=5" "mempool?limit=5" "transactions?limit=5"; do
  curl -fsS -o /dev/null "${BASE}/api/v1/${endpoint}" && pass "endpoint ${endpoint%%\?*}" || fail "endpoint ${endpoint%%\?*}"
done
[[ "$(curl -sS -o /dev/null -w '%{http_code}' "${BASE}/api/v1/transactions/not-a-txid")" == "400" ]] && pass "malformed txid rejected" || fail "malformed txid rejected"
[[ "$(curl -sS -o /dev/null -w '%{http_code}' "${BASE}/api/v1/blocks/999999999")" == "404" ]] && pass "unknown block rejected" || fail "unknown block rejected"

# --------------------------------------------------------------- isolation

[[ "$(cli node-01 listwallets | jq -c .)" == "[]" ]] && pass "wallets unloaded" || fail "wallets unloaded"
mounts="$(docker inspect "${NETWORK_NAME}-explorer" --format '{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}')"
grep -q '/var/run/docker.sock' <<<"${mounts}" && fail "no Docker socket" || pass "no Docker socket"
grep -qi 'wallet' <<<"${mounts}" && fail "no wallet mount" || pass "no wallet mount"
port_bindings="$(docker inspect "${NETWORK_NAME}-explorer" --format '{{json .HostConfig.PortBindings}}')"
[[ "$(jq -r '.["8080/tcp"][0].HostIp' <<<"${port_bindings}")" == "127.0.0.1" ]] && pass "explorer bound to 127.0.0.1" || fail "explorer bound to 127.0.0.1"

leaks=0
for path in "/" "/app.js" "/styles.css" "/api/v1/network" "/api/v1/explorer/status" "/api/v1/nodes"; do
  body="$(curl -fsS "${BASE}${path}")"
  grep -qiE 'rpcauth|rpcpassword|RPC_PASSWORD|/run/explorer-secrets|/run/monitor-secrets|payout-wallet' <<<"${body}" && leaks=$((leaks + 1))
done
((leaks == 0)) && pass "no credential or secret path appears in served content" || fail "served content leaked ${leaks} secret reference(s)"

((failures == 0)) || exit 1
echo "Explorer checks passed."
