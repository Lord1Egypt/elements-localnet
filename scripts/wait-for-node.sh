#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
node="${1:-node-01}"
timeout_seconds="${2:-180}"
[[ "${node}" =~ ^node-[0-9]{2,3}$ ]] || { echo "Invalid node name: ${node}" >&2; exit 2; }
[[ "${timeout_seconds}" =~ ^[1-9][0-9]*$ ]] || { echo "Timeout must be positive." >&2; exit 2; }

deadline=$((SECONDS + timeout_seconds))
while ((SECONDS < deadline)); do
  status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "elements-localnet-${node}" 2>/dev/null || true)"
  if [[ "${status}" == "healthy" ]]; then
    echo "${node} is healthy."
    exit 0
  fi
  if [[ "${status}" == "unhealthy" || "${status}" == "exited" || "${status}" == "dead" ]]; then
    echo "${node} failed with status ${status}." >&2
    docker compose -f "${ROOT_DIR}/compose.yaml" logs --tail=80 "${node}" >&2 || true
    exit 1
  fi
  sleep 2
done
echo "Timed out waiting for ${node}." >&2
exit 1

