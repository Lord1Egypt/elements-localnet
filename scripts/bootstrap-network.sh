#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "${ROOT_DIR}/generated/network.env"
COMPOSE=(docker compose -f "${ROOT_DIR}/compose.yaml")

nodes=()
for ((i = 1; i <= NODE_COUNT; i++)); do nodes+=("$(printf 'node-%02d' "${i}")"); done
"${COMPOSE[@]}" up -d "${nodes[@]}" network-status
for node in "${nodes[@]}"; do "${ROOT_DIR}/scripts/wait-for-node.sh" "${node}" 240; done
"${ROOT_DIR}/scripts/create-payout-wallet.sh"
deadline=$((SECONDS + 120))
while ((SECONDS < deadline)); do
  [[ "$(docker inspect --format '{{.State.Health.Status}}' elements-localnet-network-status 2>/dev/null || true)" == "healthy" ]] && break
  sleep 2
done
[[ "$(docker inspect --format '{{.State.Health.Status}}' elements-localnet-network-status 2>/dev/null || true)" == "healthy" ]] || { echo "network-status did not become healthy." >&2; exit 1; }

if [[ "${AUTO_MINE}" == "yes" ]]; then
  "${COMPOSE[@]}" --profile producer up -d producer
echo "Automatic block producer started."
else
  echo "Automatic block producer is disabled; use './manage.sh mine N' or './manage.sh producer start'."
fi
