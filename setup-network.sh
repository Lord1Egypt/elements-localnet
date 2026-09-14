#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GENERATED_DIR="${ROOT_DIR}/generated"
NETWORK_ENV="${GENERATED_DIR}/network.env"
TEMPLATE="${ROOT_DIR}/templates/elements.conf.template"

ELEMENTS_VERSION="23.3.4"
NODE_COUNT="5"
BLOCK_REWARD_SATS="5000000000"
HALVING_INTERVAL="210000"
BLOCK_INTERVAL="60"
AUTO_MINE="yes"
MAX_SAFE_NODES="20"
RPC_HOST_PORT_BASE="7041"
P2P_HOST_PORT_BASE="${P2P_HOST_PORT_BASE:-7142}"
LOCALNET_SUBNET="172.28.239.0/24"
VOLUME_NAMESPACE="elements-localnet-phase11"
PAR="4"
LOCAL_UID="$(id -u)"
LOCAL_GID="$(id -g)"
if [[ "${LOCAL_UID}" == "0" ]]; then LOCAL_UID="10001"; LOCAL_GID="10001"; fi
NON_INTERACTIVE="no"
ALLOW_LARGE="no"
OVERWRITE="no"

usage() {
  cat <<'EOF'
Usage: ./setup-network.sh [options]
  --nodes N
  --block-reward-sats N
  --halving-interval N
  --block-interval N
  --auto-mine yes|no
  --allow-large-network       Permit more than 20 nodes
  --overwrite                 Replace generated files; never deletes volumes
  -h, --help
EOF
}

is_uint() { [[ "$1" =~ ^[0-9]+$ ]]; }
is_positive() { is_uint "$1" && ((10#$1 > 0)); }

if (($# > 0)); then NON_INTERACTIVE="yes"; fi
while (($#)); do
  case "$1" in
    --nodes) [[ $# -ge 2 ]] || { echo "--nodes needs a value" >&2; exit 2; }; NODE_COUNT="$2"; shift 2 ;;
    --block-reward-sats) [[ $# -ge 2 ]] || { echo "--block-reward-sats needs a value" >&2; exit 2; }; BLOCK_REWARD_SATS="$2"; shift 2 ;;
    --halving-interval) [[ $# -ge 2 ]] || { echo "--halving-interval needs a value" >&2; exit 2; }; HALVING_INTERVAL="$2"; shift 2 ;;
    --block-interval) [[ $# -ge 2 ]] || { echo "--block-interval needs a value" >&2; exit 2; }; BLOCK_INTERVAL="$2"; shift 2 ;;
    --auto-mine) [[ $# -ge 2 ]] || { echo "--auto-mine needs a value" >&2; exit 2; }; AUTO_MINE="$2"; shift 2 ;;
    --allow-large-network) ALLOW_LARGE="yes"; shift ;;
    --overwrite) OVERWRITE="yes"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ "${NON_INTERACTIVE}" == "no" ]]; then
  read -r -p "Node count [${NODE_COUNT}]: " answer; NODE_COUNT="${answer:-${NODE_COUNT}}"
  read -r -p "Block reward in satoshis [${BLOCK_REWARD_SATS}]: " answer; BLOCK_REWARD_SATS="${answer:-${BLOCK_REWARD_SATS}}"
  read -r -p "Halving interval [${HALVING_INTERVAL}]: " answer; HALVING_INTERVAL="${answer:-${HALVING_INTERVAL}}"
  read -r -p "Automatic block interval in seconds [${BLOCK_INTERVAL}]: " answer; BLOCK_INTERVAL="${answer:-${BLOCK_INTERVAL}}"
  read -r -p "Start automatic mining (yes/no) [${AUTO_MINE}]: " answer; AUTO_MINE="${answer:-${AUTO_MINE}}"
fi

is_positive "${NODE_COUNT}" || { echo "Node count must be a positive integer." >&2; exit 2; }
is_uint "${BLOCK_REWARD_SATS}" || { echo "Reward must be a non-negative integer in satoshis." >&2; exit 2; }
is_positive "${HALVING_INTERVAL}" || { echo "Halving interval must be positive." >&2; exit 2; }
is_positive "${BLOCK_INTERVAL}" || { echo "Block interval must be positive." >&2; exit 2; }
[[ "${AUTO_MINE}" == "yes" || "${AUTO_MINE}" == "no" ]] || { echo "--auto-mine must be yes or no." >&2; exit 2; }
if ((10#${NODE_COUNT} > MAX_SAFE_NODES)) && [[ "${ALLOW_LARGE}" != "yes" ]]; then
  echo "Refusing ${NODE_COUNT} nodes (safe default maximum ${MAX_SAFE_NODES}). Use --allow-large-network explicitly." >&2
  exit 2
fi
if ((10#${NODE_COUNT} > 200)); then
  echo "Refusing more than 200 nodes; host ports and subnet capacity would be unsafe." >&2
  exit 2
fi

command -v docker >/dev/null || { echo "Docker is required." >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo "Docker daemon is unavailable. Start Docker Desktop or Docker Engine." >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "Docker Compose v2 ('docker compose') is required." >&2; exit 1; }
command -v openssl >/dev/null || { echo "openssl is required to generate credentials." >&2; exit 1; }

if [[ -f "${NETWORK_ENV}" && "${OVERWRITE}" != "yes" ]]; then
  if [[ "${NON_INTERACTIVE}" == "yes" ]]; then
    echo "A generated network already exists. Re-run with --overwrite after reviewing it; volumes will be preserved." >&2
    exit 1
  fi
  read -r -p "Generated network files already exist. Overwrite files without deleting volumes? Type 'overwrite': " answer
  [[ "${answer}" == "overwrite" ]] || { echo "Cancelled."; exit 1; }
fi

if [[ -f "${NETWORK_ENV}" ]]; then
  old_nodes="$(awk -F= '$1 == "NODE_COUNT" {print $2}' "${NETWORK_ENV}")"
  old_reward="$(awk -F= '$1 == "BLOCK_REWARD_SATS" {print $2}' "${NETWORK_ENV}")"
  old_halving="$(awk -F= '$1 == "HALVING_INTERVAL" {print $2}' "${NETWORK_ENV}")"
  if [[ "${old_nodes}" != "${NODE_COUNT}" || "${old_reward}" != "${BLOCK_REWARD_SATS}" || "${old_halving}" != "${HALVING_INTERVAL}" ]]; then
    for ((i = 1; i <= old_nodes; i++)); do
      volume="$(printf '%s-node-%02d-data' "${VOLUME_NAMESPACE}" "${i}")"
      if docker volume inspect "${volume}" >/dev/null 2>&1; then
        echo "Refusing to apply topology/consensus changes while ${volume} exists." >&2
        echo "Use './manage.sh destroy --yes-i-understand' only if you intend to erase this test chain." >&2
        exit 1
      fi
    done
  fi
fi

mkdir -p "${GENERATED_DIR}/nodes" "${GENERATED_DIR}/secrets/wallet" "${GENERATED_DIR}/secrets/monitor" "${GENERATED_DIR}/public" "${GENERATED_DIR}/status"
chmod 700 "${GENERATED_DIR}/secrets" "${GENERATED_DIR}/secrets/wallet" "${GENERATED_DIR}/secrets/monitor"

for ((i = 1; i <= NODE_COUNT; i++)); do
  node="$(printf 'node-%02d' "${i}")"
  node_dir="${GENERATED_DIR}/nodes/${node}"
  mkdir -p "${node_dir}"
  rpc_user="rpc_${node//-/_}"
  rpc_password="$(openssl rand -hex 32)"
  rpc_salt="$(openssl rand -hex 16)"
  rpc_hash="$(printf '%s' "${rpc_password}" | openssl dgst -sha256 -hmac "${rpc_salt}" | awk '{print $NF}')"
  rpc_auth="${rpc_user}:${rpc_salt}\$${rpc_hash}"
  monitor_user="monitor_${node//-/_}"
  monitor_password="$(openssl rand -hex 32)"
  monitor_salt="$(openssl rand -hex 16)"
  monitor_hash="$(printf '%s' "${monitor_password}" | openssl dgst -sha256 -hmac "${monitor_salt}" | awk '{print $NF}')"
  monitor_auth="${monitor_user}:${monitor_salt}\$${monitor_hash}"
  txindex="0"
  wallet_setting="disablewallet=1"
  if ((i == 1)); then
    wallet_setting="blindedaddresses=0"
  elif ((i == 2)); then
    txindex="1"
  fi
  # Full mesh, one link per pair: node i dials only higher-numbered nodes, so
  # every node ends up with NODE_COUNT-1 peers instead of double that. Restarts
  # still heal because the lower-numbered peers keep retrying their addnodes.
  # sed expands the \n escapes into real lines.
  peer_setting="# full mesh is dialled by lower-numbered nodes"
  for ((j = i + 1; j <= NODE_COUNT; j++)); do
    if [[ "${peer_setting}" == \#* ]]; then peer_setting=""; else peer_setting+='\n'; fi
    peer_setting+="$(printf 'addnode=node-%02d:7042' "${j}")"
  done
  sed \
    -e "s|{{PAR}}|${PAR}|g" \
    -e "s|{{BLOCK_REWARD_SATS}}|${BLOCK_REWARD_SATS}|g" \
    -e "s|{{HALVING_INTERVAL}}|${HALVING_INTERVAL}|g" \
    -e "s|{{RPC_ALLOW_CIDR}}|${LOCALNET_SUBNET}|g" \
    -e "s|{{RPC_AUTH}}|${rpc_auth}|g" \
    -e "s|{{MONITOR_RPC_AUTH}}|${monitor_auth}|g" \
    -e "s|{{MONITOR_RPC_USER}}|${monitor_user}|g" \
    -e "s|{{TXINDEX}}|${txindex}|g" \
    -e "s|{{WALLET_SETTING}}|${wallet_setting}|g" \
    -e "s|{{PEER_SETTING}}|${peer_setting}|g" \
    "${TEMPLATE}" >"${node_dir}/elements.conf"
  chmod 600 "${node_dir}/elements.conf"
  printf 'RPC_USER=%s\nRPC_PASSWORD=%s\n' "${rpc_user}" "${rpc_password}" >"${GENERATED_DIR}/secrets/${node}.rpc"
  chmod 600 "${GENERATED_DIR}/secrets/${node}.rpc"
  printf 'RPC_USER=%s\nRPC_PASSWORD=%s\n' "${monitor_user}" "${monitor_password}" >"${GENERATED_DIR}/secrets/monitor/${node}.rpc"
  chmod 600 "${GENERATED_DIR}/secrets/monitor/${node}.rpc"
done

cat >"${NETWORK_ENV}" <<EOF
ELEMENTS_VERSION=${ELEMENTS_VERSION}
NODE_COUNT=${NODE_COUNT}
BLOCK_REWARD_SATS=${BLOCK_REWARD_SATS}
HALVING_INTERVAL=${HALVING_INTERVAL}
BLOCK_INTERVAL=${BLOCK_INTERVAL}
AUTO_MINE=${AUTO_MINE}
MAX_SAFE_NODES=${MAX_SAFE_NODES}
RPC_HOST_PORT_BASE=${RPC_HOST_PORT_BASE}
P2P_HOST_PORT_BASE=${P2P_HOST_PORT_BASE}
LOCALNET_SUBNET=${LOCALNET_SUBNET}
VOLUME_NAMESPACE=${VOLUME_NAMESPACE}
PAR=${PAR}
LOCAL_UID=${LOCAL_UID}
LOCAL_GID=${LOCAL_GID}
EOF
chmod 600 "${NETWORK_ENV}"

"${ROOT_DIR}/scripts/generate-inventory.sh"
"${ROOT_DIR}/scripts/generate-compose.sh"
docker compose -f "${ROOT_DIR}/compose.yaml" config --quiet
docker compose -f "${ROOT_DIR}/compose.yaml" build node-01 producer network-status
for ((i = 1; i <= NODE_COUNT; i++)); do
  volume="$(printf '%s-node-%02d-data' "${VOLUME_NAMESPACE}" "${i}")"
  docker volume create --label com.docker.compose.project=elements-localnet --label "com.docker.compose.volume=node-$(printf '%02d' "${i}")-data" "${volume}" >/dev/null
  docker run --rm --user 0 --entrypoint /bin/chown -v "${volume}:/data" "elements-localnet-node:${ELEMENTS_VERSION}" -R "${LOCAL_UID}:${LOCAL_GID}" /data >/dev/null
done
docker volume create --label com.docker.compose.project=elements-localnet --label com.docker.compose.volume=producer-lock "${VOLUME_NAMESPACE}-producer-lock" >/dev/null
docker volume create --label com.docker.compose.project=elements-localnet --label com.docker.compose.volume=producer-status "${VOLUME_NAMESPACE}-producer-status" >/dev/null
"${ROOT_DIR}/scripts/bootstrap-network.sh"

echo
echo "elements-localnet is ready. RPC endpoints are bound to localhost only:"
for ((i = 1; i <= NODE_COUNT; i++)); do
  printf '  node-%02d: http://127.0.0.1:%d\n' "${i}" "$((RPC_HOST_PORT_BASE + i - 1))"
done
echo "Credentials are stored in generated/secrets/ and were not printed."
echo "Commands: ./manage.sh status | height | peers | verify"
echo "Stop safely: ./manage.sh stop (named volumes are preserved)"
