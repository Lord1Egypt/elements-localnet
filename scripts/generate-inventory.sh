#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "${ROOT_DIR}/generated/network.env"
tmp="${ROOT_DIR}/generated/inventory.json.tmp"
trap 'rm -f "${tmp}"' EXIT

cat >"${tmp}" <<EOF
{
  "networkName": "elements-localnet",
  "pollIntervalSeconds": 1,
  "rpcTimeoutMillis": 2000,
  "staleThresholdSeconds": $((BLOCK_INTERVAL * 5 + 15)),
  "blockRewardSats": ${BLOCK_REWARD_SATS},
  "halvingInterval": ${HALVING_INTERVAL},
  "blockInterval": ${BLOCK_INTERVAL},
  "autoMine": $([[ "${AUTO_MINE}" == "yes" ]] && echo true || echo false),
  "producerStatusFile": "/run/producer-status/status.json",
  "nodes": [
EOF
for ((i = 1; i <= NODE_COUNT; i++)); do
  node="$(printf 'node-%02d' "${i}")"
  role="validator"; capabilities='[]'
  ((i == 1)) && role="producer"
  ((i == 2)) && capabilities='["archive","txindex"]'
  comma=","; ((i == NODE_COUNT)) && comma=""
  printf '    {"id":"%s","role":"%s","capabilities":%s,"rpcHost":"%s","rpcPort":7040,"networkIp":"172.28.239.%d","credentialsFile":"/run/monitor-secrets/%s.rpc"}%s\n' \
    "${node}" "${role}" "${capabilities}" "${node}" "$((10 + i))" "${node}" "${comma}" >>"${tmp}"
done
cat >>"${tmp}" <<'EOF'
  ]
}
EOF
chmod 644 "${tmp}"
mv "${tmp}" "${ROOT_DIR}/generated/inventory.json"
trap - EXIT
