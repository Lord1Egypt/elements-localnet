#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${ROOT_DIR}/generated/network.env"
COMPOSE_FILE="${ROOT_DIR}/compose.yaml"
[[ -f "${ENV_FILE}" && -f "${COMPOSE_FILE}" ]] || { echo "Network is not configured. Run ./setup-network.sh first." >&2; exit 1; }
# shellcheck disable=SC1090
source "${ENV_FILE}"
NETWORK_NAME="${NETWORK_NAME:-elements-localnet}"
VOLUME_NAMESPACE="${VOLUME_NAMESPACE:-${NETWORK_NAME}}"
DASHBOARD_HOST_PORT="${DASHBOARD_HOST_PORT:-8080}"
EXPLORER_DB_VOLUME="${VOLUME_NAMESPACE}-explorer-db"
BACKUP_DIR="${ROOT_DIR}/generated/backups"
COMPOSE=(docker compose -f "${COMPOSE_FILE}")
CLI=(elements-cli -chain=elements -datadir=/data -conf=/config/elements.conf)

usage() {
  cat <<'EOF'
Usage: ./manage.sh COMMAND [ARGS]
  status | peers | height | start | stop | restart | verify [--acceptance]
  mine N
  logs node-NN
  producer start|stop|status|interval SECONDS
  explorer status|logs|restart|backup|reindex --yes-i-understand
  wallet load|unload|status
  destroy --yes-i-understand
EOF
}

valid_node() {
  [[ "$1" =~ ^node-([0-9]{2,3})$ ]] || return 1
  local n=$((10#${BASH_REMATCH[1]}))
  ((n >= 1 && n <= NODE_COUNT))
}

rpc() {
  local node="$1"; shift
  "${COMPOSE[@]}" exec -T "${node}" "${CLI[@]}" "$@"
}

producer_running() {
  [[ "$(docker inspect --format '{{.State.Running}}' "${NETWORK_NAME}-producer" 2>/dev/null || true)" == "true" ]]
}

command="${1:-}"
case "${command}" in
  status)
    "${COMPOSE[@]}" --profile producer ps
    ;;
  peers)
    for ((i = 1; i <= NODE_COUNT; i++)); do
      node="$(printf 'node-%02d' "${i}")"
      count="$(rpc "${node}" getconnectioncount)"
      printf '%s peers=%s\n' "${node}" "${count}"
    done
    ;;
  height)
    for ((i = 1; i <= NODE_COUNT; i++)); do
      node="$(printf 'node-%02d' "${i}")"
      height="$(rpc "${node}" getblockcount)"
      printf '%s height=%s\n' "${node}" "${height}"
    done
    ;;
  mine)
    count="${2:-}"
    [[ "${count}" =~ ^[1-9][0-9]*$ ]] || { echo "mine requires a positive block count." >&2; exit 2; }
    ((10#${count} <= 100000)) || { echo "Refusing to mine more than 100000 blocks in one command." >&2; exit 2; }
    producer_running && { echo "Stop the automatic producer before manual mining." >&2; exit 1; }
    address="$(tr -d '\r\n' <"${ROOT_DIR}/generated/public/payout-address")"
    rpc node-01 generatetoaddress "${count}" "${address}"
    ;;
  logs)
    node="${2:-}"
    valid_node "${node}" || { echo "logs requires a configured node name such as node-02." >&2; exit 2; }
    "${COMPOSE[@]}" logs --tail=200 -f "${node}"
    ;;
  producer)
    action="${2:-status}"
    case "${action}" in
      start) "${COMPOSE[@]}" --profile producer up -d producer ;;
      stop) "${COMPOSE[@]}" --profile producer stop producer ;;
      status)
        if producer_running; then echo "producer running"; else echo "producer stopped"; fi
        ;;
      interval)
        new_interval="${3:-}"
        [[ "${new_interval}" =~ ^[1-9][0-9]*$ ]] || { echo "producer interval requires a positive integer number of seconds." >&2; exit 2; }
        old_interval="${BLOCK_INTERVAL}"
        was_running="no"; producer_running && was_running="yes"
        env_tmp="${ENV_FILE}.tmp.$$"
        awk -v value="${new_interval}" '
          /^BLOCK_INTERVAL=/ { print "BLOCK_INTERVAL=" value; found=1; next }
          { print }
          END { if (!found) print "BLOCK_INTERVAL=" value }
        ' "${ENV_FILE}" >"${env_tmp}"
        chmod 600 "${env_tmp}"
        mv "${env_tmp}" "${ENV_FILE}"
        BLOCK_INTERVAL="${new_interval}"
        "${ROOT_DIR}/scripts/generate-inventory.sh"
        "${ROOT_DIR}/scripts/generate-compose.sh"
        "${COMPOSE[@]}" config --quiet
        "${COMPOSE[@]}" up -d --no-deps --force-recreate explorer
        if [[ "${was_running}" == "yes" ]]; then
          "${COMPOSE[@]}" --profile producer up -d --no-deps --force-recreate producer
        else
          "${COMPOSE[@]}" --profile producer rm -sf producer >/dev/null 2>&1 || true
          "${COMPOSE[@]}" --profile producer create producer >/dev/null
        fi
        effective="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${NETWORK_NAME}-producer" | awk -F= '$1 == "BLOCK_INTERVAL" {print $2}')"
        [[ "${effective}" == "${new_interval}" ]] || { echo "Interval update failed: effective value is ${effective:-unknown}." >&2; exit 1; }
        echo "Producer interval changed from ${old_interval}s to ${new_interval}s; Elements nodes and blockchain volumes were not restarted."
        echo "Effective producer interval: ${effective}s"
        ;;
      *) echo "producer expects start, stop, status, or interval SECONDS." >&2; exit 2 ;;
    esac
    ;;
  explorer)
    action="${2:-status}"
    case "${action}" in
      status)
        "${COMPOSE[@]}" ps explorer
        if command -v curl >/dev/null; then
          curl -fsS "http://127.0.0.1:${DASHBOARD_HOST_PORT}/api/v1/explorer/status" || {
            echo "The explorer API did not answer on 127.0.0.1:${DASHBOARD_HOST_PORT}." >&2
            exit 1
          }
          echo
        else
          echo "Install curl to read http://127.0.0.1:${DASHBOARD_HOST_PORT}/api/v1/explorer/status"
        fi
        ;;
      logs) "${COMPOSE[@]}" logs --tail=200 -f explorer ;;
      restart) "${COMPOSE[@]}" restart explorer ;;
      backup)
        mkdir -p "${BACKUP_DIR}"
        stamp="$(date -u +%Y%m%dT%H%M%SZ)"
        container_path="/var/lib/elements-explorer/backup-${stamp}.db"
        host_path="${BACKUP_DIR}/explorer-${stamp}.db"
        # VACUUM INTO writes a consistent copy while the indexer keeps running.
        "${COMPOSE[@]}" exec -T explorer /usr/local/bin/explorer -backup "${container_path}"
        docker cp "${NETWORK_NAME}-explorer:${container_path}" "${host_path}"
        "${COMPOSE[@]}" exec -T explorer /usr/local/bin/explorer -remove-backup "${container_path}"
        chmod 600 "${host_path}"
        echo "Wrote ${host_path}"
        ;;
      reindex)
        [[ "${3:-}" == "--yes-i-understand" ]] || {
          echo "Destructive command refused. Exact required form:" >&2
          echo "  ./manage.sh explorer reindex --yes-i-understand" >&2
          exit 2
        }
        echo "This deletes only the rebuildable explorer index:"
        echo "  Docker volume: ${EXPLORER_DB_VOLUME}"
        echo "Elements node volumes, chain data, wallets, credentials and generated/public/assets.json are untouched."
        "${COMPOSE[@]}" stop explorer
        "${COMPOSE[@]}" rm -f explorer >/dev/null
        docker volume rm "${EXPLORER_DB_VOLUME}" >/dev/null
        docker volume create --label "com.docker.compose.project=${NETWORK_NAME}" --label com.docker.compose.volume=explorer-db "${EXPLORER_DB_VOLUME}" >/dev/null
        "${COMPOSE[@]}" up -d explorer
        echo "The explorer index was deleted and is rebuilding from genesis."
        ;;
      *) echo "explorer expects status, logs, restart, backup, or reindex --yes-i-understand." >&2; exit 2 ;;
    esac
    ;;
  wallet)
    action="${2:-status}"
    case "${action}" in
      load)
        rpc node-01 loadwallet payout
        echo "Warning: unload the payout wallet after the deliberate operation."
        ;;
      unload) rpc node-01 unloadwallet payout ;;
      status) rpc node-01 listwallets ;;
      *) echo "wallet expects load, unload, or status." >&2; exit 2 ;;
    esac
    ;;
  start)
    nodes=(); for ((i = 1; i <= NODE_COUNT; i++)); do nodes+=("$(printf 'node-%02d' "${i}")"); done
    "${COMPOSE[@]}" up -d "${nodes[@]}" explorer
    if [[ "${AUTO_MINE}" == "yes" ]]; then "${COMPOSE[@]}" --profile producer up -d producer; fi
    ;;
  stop)
    "${COMPOSE[@]}" --profile producer stop
    echo "Stopped containers; named volumes and generated files were preserved."
    ;;
  restart)
    "$0" stop
    "$0" start
    ;;
  verify)
    shift
    exec "${ROOT_DIR}/scripts/verify-network.sh" "$@"
    ;;
  destroy)
    [[ "${2:-}" == "--yes-i-understand" ]] || {
      echo "Destructive command refused. Exact required form: ./manage.sh destroy --yes-i-understand" >&2
      exit 2
    }
    echo "The following generated directory contents will be permanently removed:"
    printf '  %s\n' "${ROOT_DIR}/generated"
    echo "The following named volumes will be permanently removed:"
    for ((i = 1; i <= NODE_COUNT; i++)); do printf '  %s-node-%02d-data\n' "${VOLUME_NAMESPACE}" "${i}"; done
    echo "  ${VOLUME_NAMESPACE}-producer-lock"
    echo "  ${VOLUME_NAMESPACE}-producer-status"
    echo "  ${EXPLORER_DB_VOLUME}"
    "${COMPOSE[@]}" --profile producer down --volumes --remove-orphans
    volumes=("${VOLUME_NAMESPACE}-producer-lock" "${VOLUME_NAMESPACE}-producer-status" "${EXPLORER_DB_VOLUME}")
    for ((i = 1; i <= NODE_COUNT; i++)); do volumes+=("$(printf '%s-node-%02d-data' "${VOLUME_NAMESPACE}" "${i}")"); done
    docker volume rm "${volumes[@]}"
    find "${ROOT_DIR}/generated" -mindepth 1 ! -name .gitkeep -depth -delete
    echo "Destroyed only the listed local test-network data. This cannot be recovered unless separately backed up."
    ;;
  -h|--help|'') usage ;;
  *) echo "Unknown command: ${command}" >&2; usage >&2; exit 2 ;;
esac
