#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "${ROOT_DIR}/generated/network.env"
cd "${ROOT_DIR}"
failures=0
pass() { echo "PASS $*"; }
fail() { echo "FAIL $*" >&2; failures=$((failures + 1)); }

[[ "$(curl -fsS http://127.0.0.1:8080/healthz | jq -r .status)" == "ok" ]] && pass healthz || fail healthz
snapshot="$(curl -fsS http://127.0.0.1:8080/api/v1/network)"
[[ "$(jq '.nodes | length' <<<"${snapshot}")" == "${NODE_COUNT}" ]] && pass "dynamic node count" || fail "dynamic node count"
[[ "$(jq '[.nodes[] | select(.state != "HEALTHY")] | length' <<<"${snapshot}")" == "0" ]] && pass "all nodes healthy" || fail "all nodes healthy"
[[ "$(jq -r '.nodes[] | select(.id == "node-01") | .role' <<<"${snapshot}")" == "producer" ]] && pass "producer role" || fail "producer role"
if ((NODE_COUNT >= 2)); then [[ "$(jq '[.nodes[] | select(.role == "validator")] | length' <<<"${snapshot}")" == "$((NODE_COUNT - 1))" ]] && pass "validator roles" || fail "validator roles"; fi
if ((NODE_COUNT >= 2)); then [[ "$(jq -r '.nodes[] | select(.id == "node-02") | .capabilities | index("archive") != null' <<<"${snapshot}")" == "true" ]] && pass "node-02 archive capability" || fail "node-02 archive capability"; fi
[[ "$(jq -r '.producer.blockInterval' <<<"${snapshot}")" == "${BLOCK_INTERVAL}" ]] && pass "producer interval" || fail "producer interval"

api_height="$(jq -r .network.canonicalHeight <<<"${snapshot}")"
api_hash="$(jq -r .network.canonicalBestBlockHash <<<"${snapshot}")"
direct_height="$(docker compose exec -T node-01 elements-cli -chain=elements -datadir=/data -conf=/config/elements.conf getblockcount)"
direct_hash="$(docker compose exec -T node-01 elements-cli -chain=elements -datadir=/data -conf=/config/elements.conf getblockhash "${api_height}")"
if [[ "${direct_hash}" == "${api_hash}" ]] && ((direct_height >= api_height && direct_height - api_height <= 3)); then pass "direct RPC height/hash match"; else fail "direct RPC height/hash match"; fi

for endpoint in nodes topology producer economics; do curl -fsS -o /dev/null "http://127.0.0.1:8080/api/v1/${endpoint}" && pass "endpoint ${endpoint}" || fail "endpoint ${endpoint}"; done
[[ "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v1/nodes/not-configured)" == "404" ]] && pass "unknown node rejected" || fail "unknown node rejected"
[[ "$(docker compose exec -T node-01 elements-cli -chain=elements -datadir=/data -conf=/config/elements.conf listwallets | jq -c .)" == "[]" ]] && pass "wallets unloaded" || fail "wallets unloaded"
[[ -z "$(docker inspect elements-localnet-network-status --format '{{range .Mounts}}{{if eq .Source "/var/run/docker.sock"}}mounted{{end}}{{end}}')" ]] && pass "no Docker socket" || fail "no Docker socket"

((failures == 0)) || exit 1
