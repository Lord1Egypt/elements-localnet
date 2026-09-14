#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GENERATED_DIR="${ROOT_DIR}/generated"
NETWORK_ENV="${GENERATED_DIR}/network.env"
TEMPLATE="${ROOT_DIR}/templates/elements.conf.template"
ASSET_REGISTRY_EXAMPLE="${ROOT_DIR}/config/assets.example.json"

ELEMENTS_VERSION="23.3.4"
NETWORK_NAME="elements-localnet"
NODE_COUNT="5"
BLOCK_REWARD_SATS="5000000000"
HALVING_INTERVAL="210000"
BLOCK_INTERVAL="60"
AUTO_MINE="yes"
TOPOLOGY="mesh"
EXTERNAL_PEERS="yes"
P2P_EXPOSURE="localhost"
MAX_SAFE_NODES="20"
RPC_HOST_PORT_BASE="7041"
P2P_HOST_PORT_BASE="7142"
DASHBOARD_HOST_PORT="8080"
EXPLORER_IMAGE_TAG="phase2a"
LOCALNET_SUBNET="172.28.239.0/24"
VOLUME_NAMESPACE="elements-localnet-phase11"
PAR="4"
PAYOUT_ADDRESS=""
LOCAL_UID="$(id -u)"
LOCAL_GID="$(id -g)"
if [[ "${LOCAL_UID}" == "0" ]]; then LOCAL_UID="10001"; LOCAL_GID="10001"; fi
NON_INTERACTIVE="no"
ALLOW_LARGE="no"
OVERWRITE="no"
ASSUME_YES="no"

usage() {
  cat <<'EOF'
Usage: ./setup-network.sh [options]

Run with no options on a terminal for the guided setup. Every value below can
also be supplied as a flag, which selects fully non-interactive mode.

Network shape
  --network-name NAME          Compose project, container, image and bridge prefix
                               (default: elements-localnet)
  --nodes N                    Number of managed Elements nodes (default: 5)
  --topology mesh|seed         mesh gives every pair exactly one link; seed dials
                               every node at node-01 (default: mesh)

Consensus and production
  --block-reward-sats N        Block subsidy in satoshis (default: 5000000000)
  --halving-interval N         Blocks between halvings (default: 210000)
  --block-interval N           Seconds between automatic blocks (default: 60)
  --auto-mine yes|no           Start the automatic block producer (default: yes)

Peering and host ports
  --external-peers yes|no      Allow Elements nodes outside Docker (for example
                               Elements-Qt on Windows) to connect (default: yes)
  --p2p-exposure MODE          localhost, lan, or disabled. localhost publishes
                               P2P on 127.0.0.1 only; lan publishes on every host
                               interface and must be chosen deliberately
                               (default: localhost)
  --p2p-port-base N            First published P2P host port (default: 7142)
  --rpc-port-base N            First published RPC host port, always loopback
                               (default: 7041)
  --dashboard-port N           Explorer host port, always loopback (default: 8080)

Payout
  --payout-address ADDRESS     Use an existing payout address; it is validated
                               through Elements RPC and no wallet is created
  --new-payout-wallet          Create a payout wallet, back it up, export only
                               the public address, then unload it (default)

Behaviour
  --allow-large-network        Permit more than 20 nodes
  --overwrite                  Replace generated files; never deletes volumes
  --non-interactive            Never prompt; fail instead of asking
  --yes                        Skip the final confirmation prompt
  -h, --help                   Show this help

Public RPC is never published. RPC and the explorer bind to 127.0.0.1 only.
EOF
}

is_uint() { [[ "$1" =~ ^[0-9]+$ ]]; }
is_positive() { is_uint "$1" && ((10#$1 > 0)); }
is_port() { is_uint "$1" && ((10#$1 >= 1024 && 10#$1 <= 65535)); }
is_yes_no() { [[ "$1" == "yes" || "$1" == "no" ]]; }
is_name() { [[ "$1" =~ ^[a-z][a-z0-9-]{1,40}$ ]]; }

need_value() { [[ $# -ge 2 ]] || { echo "$1 needs a value" >&2; exit 2; }; }

if (($# > 0)); then NON_INTERACTIVE="yes"; fi
while (($#)); do
  case "$1" in
    --network-name) need_value "$@"; NETWORK_NAME="$2"; shift 2 ;;
    --nodes) need_value "$@"; NODE_COUNT="$2"; shift 2 ;;
    --topology) need_value "$@"; TOPOLOGY="$2"; shift 2 ;;
    --block-reward-sats) need_value "$@"; BLOCK_REWARD_SATS="$2"; shift 2 ;;
    --halving-interval) need_value "$@"; HALVING_INTERVAL="$2"; shift 2 ;;
    --block-interval) need_value "$@"; BLOCK_INTERVAL="$2"; shift 2 ;;
    --auto-mine) need_value "$@"; AUTO_MINE="$2"; shift 2 ;;
    --external-peers) need_value "$@"; EXTERNAL_PEERS="$2"; shift 2 ;;
    --p2p-exposure) need_value "$@"; P2P_EXPOSURE="$2"; shift 2 ;;
    --p2p-port-base) need_value "$@"; P2P_HOST_PORT_BASE="$2"; shift 2 ;;
    --rpc-port-base) need_value "$@"; RPC_HOST_PORT_BASE="$2"; shift 2 ;;
    --dashboard-port) need_value "$@"; DASHBOARD_HOST_PORT="$2"; shift 2 ;;
    --payout-address) need_value "$@"; PAYOUT_ADDRESS="$2"; shift 2 ;;
    --new-payout-wallet) PAYOUT_ADDRESS=""; shift ;;
    --allow-large-network) ALLOW_LARGE="yes"; shift ;;
    --overwrite) OVERWRITE="yes"; shift ;;
    --non-interactive) NON_INTERACTIVE="yes"; shift ;;
    --yes) ASSUME_YES="yes"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# A pipe or CI runner must never block on a prompt.
if [[ ! -t 0 ]]; then NON_INTERACTIVE="yes"; fi

ask() {
  # ask VARIABLE "Question" "default" validator-function "error message"
  local variable="$1" question="$2" default="$3" validator="$4" message="$5" answer
  while true; do
    read -r -p "${question} [${default}]: " answer || { echo; echo "Input ended; cancelling." >&2; exit 1; }
    answer="${answer:-${default}}"
    if "${validator}" "${answer}"; then
      printf -v "${variable}" '%s' "${answer}"
      return 0
    fi
    echo "  ${message}" >&2
  done
}

is_topology() { [[ "$1" == "mesh" || "$1" == "seed" ]]; }
is_exposure() { [[ "$1" == "localhost" || "$1" == "lan" || "$1" == "disabled" ]]; }
is_payout_choice() { [[ "$1" == "new" || "$1" == "existing" ]]; }
is_address_shape() { [[ "$1" =~ ^[A-Za-z0-9]{20,120}$ ]]; }

if [[ "${NON_INTERACTIVE}" == "no" ]]; then
  echo "elements-localnet guided setup. Press Enter to accept the shown default."
  echo
  ask NETWORK_NAME "Network name" "${NETWORK_NAME}" is_name "Use lower-case letters, digits and hyphens (2-41 characters)."
  ask NODE_COUNT "Number of managed Docker nodes" "${NODE_COUNT}" is_positive "Enter a positive integer."
  ask BLOCK_INTERVAL "Block interval in seconds" "${BLOCK_INTERVAL}" is_positive "Enter a positive integer."
  ask BLOCK_REWARD_SATS "Block subsidy in satoshis" "${BLOCK_REWARD_SATS}" is_uint "Enter a non-negative integer."
  ask HALVING_INTERVAL "Halving interval in blocks" "${HALVING_INTERVAL}" is_positive "Enter a positive integer."
  ask AUTO_MINE "Enable automatic block production (yes/no)" "${AUTO_MINE}" is_yes_no "Answer yes or no."
  ask TOPOLOGY "Topology (mesh/seed)" "${TOPOLOGY}" is_topology "Answer mesh or seed."
  ask EXTERNAL_PEERS "Allow external Elements peers such as Windows Elements-Qt (yes/no)" "${EXTERNAL_PEERS}" is_yes_no "Answer yes or no."
  if [[ "${EXTERNAL_PEERS}" == "yes" ]]; then
    ask P2P_EXPOSURE "P2P exposure (localhost/lan/disabled)" "${P2P_EXPOSURE}" is_exposure "Answer localhost, lan, or disabled."
  else
    P2P_EXPOSURE="disabled"
  fi
  if [[ "${P2P_EXPOSURE}" != "disabled" ]]; then
    ask P2P_HOST_PORT_BASE "First published P2P host port" "${P2P_HOST_PORT_BASE}" is_port "Enter a port between 1024 and 65535."
  fi
  payout_choice="new"
  [[ -n "${PAYOUT_ADDRESS}" ]] && payout_choice="existing"
  ask payout_choice "Payout: create a new wallet or use an existing address (new/existing)" "${payout_choice}" is_payout_choice "Answer new or existing."
  if [[ "${payout_choice}" == "existing" ]]; then
    ask PAYOUT_ADDRESS "Existing payout address" "${PAYOUT_ADDRESS:-none}" is_address_shape "Enter a plausible Elements address."
  else
    PAYOUT_ADDRESS=""
  fi
fi

[[ "${EXTERNAL_PEERS}" == "no" ]] && P2P_EXPOSURE="disabled"

is_name "${NETWORK_NAME}" || { echo "Network name must be lower-case letters, digits and hyphens." >&2; exit 2; }
is_positive "${NODE_COUNT}" || { echo "Node count must be a positive integer." >&2; exit 2; }
is_uint "${BLOCK_REWARD_SATS}" || { echo "Reward must be a non-negative integer in satoshis." >&2; exit 2; }
is_positive "${HALVING_INTERVAL}" || { echo "Halving interval must be positive." >&2; exit 2; }
is_positive "${BLOCK_INTERVAL}" || { echo "Block interval must be positive." >&2; exit 2; }
is_yes_no "${AUTO_MINE}" || { echo "--auto-mine must be yes or no." >&2; exit 2; }
is_yes_no "${EXTERNAL_PEERS}" || { echo "--external-peers must be yes or no." >&2; exit 2; }
is_topology "${TOPOLOGY}" || { echo "--topology must be mesh or seed." >&2; exit 2; }
is_exposure "${P2P_EXPOSURE}" || { echo "--p2p-exposure must be localhost, lan, or disabled." >&2; exit 2; }
is_port "${RPC_HOST_PORT_BASE}" || { echo "--rpc-port-base must be a port between 1024 and 65535." >&2; exit 2; }
is_port "${DASHBOARD_HOST_PORT}" || { echo "--dashboard-port must be a port between 1024 and 65535." >&2; exit 2; }
if [[ "${P2P_EXPOSURE}" != "disabled" ]]; then
  is_port "${P2P_HOST_PORT_BASE}" || { echo "--p2p-port-base must be a port between 1024 and 65535." >&2; exit 2; }
fi
if [[ -n "${PAYOUT_ADDRESS}" ]]; then
  is_address_shape "${PAYOUT_ADDRESS}" || { echo "--payout-address is not a plausible Elements address." >&2; exit 2; }
fi

if ((10#${NODE_COUNT} > MAX_SAFE_NODES)) && [[ "${ALLOW_LARGE}" != "yes" ]]; then
  echo "Refusing ${NODE_COUNT} nodes (safe default maximum ${MAX_SAFE_NODES}). Use --allow-large-network explicitly." >&2
  exit 2
fi
if ((10#${NODE_COUNT} > 200)); then
  echo "Refusing more than 200 nodes; host ports and subnet capacity would be unsafe." >&2
  exit 2
fi
if ((10#${RPC_HOST_PORT_BASE} + 10#${NODE_COUNT} - 1 > 65535)); then
  echo "The RPC port range would run past 65535." >&2; exit 2
fi
if [[ "${P2P_EXPOSURE}" != "disabled" ]] && ((10#${P2P_HOST_PORT_BASE} + 10#${NODE_COUNT} - 1 > 65535)); then
  echo "The P2P port range would run past 65535." >&2; exit 2
fi

# Overlap between the RPC and P2P ranges would make the published ports ambiguous.
if [[ "${P2P_EXPOSURE}" != "disabled" ]]; then
  rpc_last=$((10#${RPC_HOST_PORT_BASE} + 10#${NODE_COUNT} - 1))
  p2p_last=$((10#${P2P_HOST_PORT_BASE} + 10#${NODE_COUNT} - 1))
  if ((10#${RPC_HOST_PORT_BASE} <= p2p_last && 10#${P2P_HOST_PORT_BASE} <= rpc_last)); then
    echo "The RPC and P2P host port ranges overlap." >&2; exit 2
  fi
fi

command -v docker >/dev/null || { echo "Docker is required." >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo "Docker daemon is unavailable. Start Docker Desktop or Docker Engine." >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "Docker Compose v2 ('docker compose') is required." >&2; exit 1; }
command -v openssl >/dev/null || { echo "openssl is required to generate credentials." >&2; exit 1; }

# Ports already published by this project's own containers are not collisions.
own_ports="$(docker ps --format '{{.Names}} {{.Ports}}' 2>/dev/null | awk -v prefix="${NETWORK_NAME}-" 'index($1, prefix) == 1' || true)"
port_in_use() {
  local port="$1"
  if grep -qE "127\.0\.0\.1:${port}->|0\.0\.0\.0:${port}->|:${port}->" <<<"${own_ports}"; then
    return 1
  fi
  (exec 3<>"/dev/tcp/127.0.0.1/${port}") >/dev/null 2>&1 && { exec 3<&- 3>&-; return 0; }
  return 1
}
collisions=()
for ((i = 0; i < NODE_COUNT; i++)); do
  port_in_use "$((RPC_HOST_PORT_BASE + i))" && collisions+=("$((RPC_HOST_PORT_BASE + i)) (RPC)")
  if [[ "${P2P_EXPOSURE}" != "disabled" ]]; then
    port_in_use "$((P2P_HOST_PORT_BASE + i))" && collisions+=("$((P2P_HOST_PORT_BASE + i)) (P2P)")
  fi
done
port_in_use "${DASHBOARD_HOST_PORT}" && collisions+=("${DASHBOARD_HOST_PORT} (explorer)")
if ((${#collisions[@]} > 0)); then
  echo "Refusing to change the runtime: these host ports are already in use by something else:" >&2
  printf '  %s\n' "${collisions[@]}" >&2
  exit 2
fi

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

cat <<EOF

Configuration summary
  Network name .............. ${NETWORK_NAME}
  Managed Docker nodes ...... ${NODE_COUNT}
  Topology .................. ${TOPOLOGY}
  Block interval ............ ${BLOCK_INTERVAL}s
  Block subsidy ............. ${BLOCK_REWARD_SATS} satoshis
  Halving interval .......... ${HALVING_INTERVAL} blocks
  Automatic production ...... ${AUTO_MINE}
  External peers allowed .... ${EXTERNAL_PEERS}
  P2P exposure .............. ${P2P_EXPOSURE}$([[ "${P2P_EXPOSURE}" == "disabled" ]] || printf ' (ports %s-%s)' "${P2P_HOST_PORT_BASE}" "$((P2P_HOST_PORT_BASE + NODE_COUNT - 1))")
  RPC host ports ............ 127.0.0.1:${RPC_HOST_PORT_BASE}-$((RPC_HOST_PORT_BASE + NODE_COUNT - 1))
  Explorer .................. http://127.0.0.1:${DASHBOARD_HOST_PORT}
  Payout .................... $([[ -n "${PAYOUT_ADDRESS}" ]] && echo "existing address ${PAYOUT_ADDRESS}" || echo "create a new wallet, back it up, unload it")

EOF
if [[ "${P2P_EXPOSURE}" == "lan" ]]; then
  echo "WARNING: P2P will be published on every host interface. Only do this on a trusted network."
  echo
fi
if [[ "${NON_INTERACTIVE}" == "no" && "${ASSUME_YES}" != "yes" ]]; then
  read -r -p "Apply this configuration? Type 'yes': " answer
  [[ "${answer}" == "yes" ]] || { echo "Cancelled."; exit 1; }
fi

mkdir -p "${GENERATED_DIR}/nodes" "${GENERATED_DIR}/secrets/wallet" "${GENERATED_DIR}/secrets/monitor" \
  "${GENERATED_DIR}/secrets/explorer" "${GENERATED_DIR}/public" "${GENERATED_DIR}/status"
chmod 700 "${GENERATED_DIR}/secrets" "${GENERATED_DIR}/secrets/wallet" "${GENERATED_DIR}/secrets/monitor" \
  "${GENERATED_DIR}/secrets/explorer"

rpcauth_line() {
  # rpcauth_line USER PASSWORD -> user:salt$hmac
  local user="$1" password="$2" salt hash
  salt="$(openssl rand -hex 16)"
  hash="$(printf '%s' "${password}" | openssl dgst -sha256 -hmac "${salt}" | awk '{print $NF}')"
  printf '%s:%s$%s' "${user}" "${salt}" "${hash}"
}

for ((i = 1; i <= NODE_COUNT; i++)); do
  node="$(printf 'node-%02d' "${i}")"
  node_dir="${GENERATED_DIR}/nodes/${node}"
  mkdir -p "${node_dir}"
  rpc_user="rpc_${node//-/_}"
  rpc_password="$(openssl rand -hex 32)"
  rpc_auth="$(rpcauth_line "${rpc_user}" "${rpc_password}")"
  monitor_user="monitor_${node//-/_}"
  monitor_password="$(openssl rand -hex 32)"
  monitor_auth="$(rpcauth_line "${monitor_user}" "${monitor_password}")"
  explorer_user="explorer_${node//-/_}"
  explorer_password="$(openssl rand -hex 32)"
  explorer_auth="$(rpcauth_line "${explorer_user}" "${explorer_password}")"
  txindex="0"
  wallet_setting="disablewallet=1"
  if ((i == 1)); then
    wallet_setting="blindedaddresses=0"
  elif ((i == 2)); then
    txindex="1"
  fi
  # mesh: node i dials only higher-numbered nodes, so every pair is linked once.
  # seed: every other node dials node-01, which is the only hub.
  # sed expands the \n escapes into real lines.
  peer_setting="# no peers are dialled by this node"
  if [[ "${TOPOLOGY}" == "mesh" ]]; then
    for ((j = i + 1; j <= NODE_COUNT; j++)); do
      if [[ "${peer_setting}" == \#* ]]; then peer_setting=""; else peer_setting+='\n'; fi
      peer_setting+="$(printf 'addnode=node-%02d:7042' "${j}")"
    done
  elif ((i > 1)); then
    peer_setting="addnode=node-01:7042"
  fi
  sed \
    -e "s|{{PAR}}|${PAR}|g" \
    -e "s|{{BLOCK_REWARD_SATS}}|${BLOCK_REWARD_SATS}|g" \
    -e "s|{{HALVING_INTERVAL}}|${HALVING_INTERVAL}|g" \
    -e "s|{{RPC_ALLOW_CIDR}}|${LOCALNET_SUBNET}|g" \
    -e "s|{{RPC_AUTH}}|${rpc_auth}|g" \
    -e "s|{{MONITOR_RPC_AUTH}}|${monitor_auth}|g" \
    -e "s|{{MONITOR_RPC_USER}}|${monitor_user}|g" \
    -e "s|{{EXPLORER_RPC_AUTH}}|${explorer_auth}|g" \
    -e "s|{{EXPLORER_RPC_USER}}|${explorer_user}|g" \
    -e "s|{{TXINDEX}}|${txindex}|g" \
    -e "s|{{WALLET_SETTING}}|${wallet_setting}|g" \
    -e "s|{{PEER_SETTING}}|${peer_setting}|g" \
    "${TEMPLATE}" >"${node_dir}/elements.conf"
  chmod 600 "${node_dir}/elements.conf"
  printf 'RPC_USER=%s\nRPC_PASSWORD=%s\n' "${rpc_user}" "${rpc_password}" >"${GENERATED_DIR}/secrets/${node}.rpc"
  chmod 600 "${GENERATED_DIR}/secrets/${node}.rpc"
  printf 'RPC_USER=%s\nRPC_PASSWORD=%s\n' "${monitor_user}" "${monitor_password}" >"${GENERATED_DIR}/secrets/monitor/${node}.rpc"
  chmod 600 "${GENERATED_DIR}/secrets/monitor/${node}.rpc"
  printf 'RPC_USER=%s\nRPC_PASSWORD=%s\n' "${explorer_user}" "${explorer_password}" >"${GENERATED_DIR}/secrets/explorer/${node}.rpc"
  chmod 600 "${GENERATED_DIR}/secrets/explorer/${node}.rpc"
done

if [[ ! -s "${GENERATED_DIR}/public/assets.json" ]]; then
  if [[ -f "${ASSET_REGISTRY_EXAMPLE}" ]]; then
    cp "${ASSET_REGISTRY_EXAMPLE}" "${GENERATED_DIR}/public/assets.json"
  else
    printf '{\n  "version": 1,\n  "assets": []\n}\n' >"${GENERATED_DIR}/public/assets.json"
  fi
  chmod 644 "${GENERATED_DIR}/public/assets.json"
fi

cat >"${NETWORK_ENV}" <<EOF
ELEMENTS_VERSION=${ELEMENTS_VERSION}
NETWORK_NAME=${NETWORK_NAME}
NODE_COUNT=${NODE_COUNT}
TOPOLOGY=${TOPOLOGY}
BLOCK_REWARD_SATS=${BLOCK_REWARD_SATS}
HALVING_INTERVAL=${HALVING_INTERVAL}
BLOCK_INTERVAL=${BLOCK_INTERVAL}
AUTO_MINE=${AUTO_MINE}
MAX_SAFE_NODES=${MAX_SAFE_NODES}
RPC_HOST_PORT_BASE=${RPC_HOST_PORT_BASE}
P2P_HOST_PORT_BASE=${P2P_HOST_PORT_BASE}
P2P_EXPOSURE=${P2P_EXPOSURE}
EXTERNAL_PEERS=${EXTERNAL_PEERS}
DASHBOARD_HOST_PORT=${DASHBOARD_HOST_PORT}
EXPLORER_IMAGE_TAG=${EXPLORER_IMAGE_TAG}
LOCALNET_SUBNET=${LOCALNET_SUBNET}
VOLUME_NAMESPACE=${VOLUME_NAMESPACE}
PAYOUT_ADDRESS=${PAYOUT_ADDRESS}
PAR=${PAR}
LOCAL_UID=${LOCAL_UID}
LOCAL_GID=${LOCAL_GID}
EOF
chmod 600 "${NETWORK_ENV}"

"${ROOT_DIR}/scripts/generate-inventory.sh"
"${ROOT_DIR}/scripts/generate-compose.sh"
docker compose -f "${ROOT_DIR}/compose.yaml" config --quiet
docker compose -f "${ROOT_DIR}/compose.yaml" build node-01 producer explorer
for ((i = 1; i <= NODE_COUNT; i++)); do
  volume="$(printf '%s-node-%02d-data' "${VOLUME_NAMESPACE}" "${i}")"
  docker volume create --label "com.docker.compose.project=${NETWORK_NAME}" --label "com.docker.compose.volume=node-$(printf '%02d' "${i}")-data" "${volume}" >/dev/null
  docker run --rm --user 0 --entrypoint /bin/chown -v "${volume}:/data" "${NETWORK_NAME}-node:${ELEMENTS_VERSION}" -R "${LOCAL_UID}:${LOCAL_GID}" /data >/dev/null
done
for suffix in producer-lock producer-status explorer-db; do
  docker volume create --label "com.docker.compose.project=${NETWORK_NAME}" --label "com.docker.compose.volume=${suffix}" "${VOLUME_NAMESPACE}-${suffix}" >/dev/null
done
"${ROOT_DIR}/scripts/bootstrap-network.sh"

echo
echo "${NETWORK_NAME} is ready. RPC endpoints are bound to localhost only:"
for ((i = 1; i <= NODE_COUNT; i++)); do
  printf '  node-%02d: http://127.0.0.1:%d\n' "${i}" "$((RPC_HOST_PORT_BASE + i - 1))"
done
echo "Explorer: http://127.0.0.1:${DASHBOARD_HOST_PORT}"
if [[ "${P2P_EXPOSURE}" == "disabled" ]]; then
  echo "External P2P is disabled; no node P2P port is published."
else
  echo "External peers may connect to ${P2P_EXPOSURE} ports ${P2P_HOST_PORT_BASE}-$((P2P_HOST_PORT_BASE + NODE_COUNT - 1))."
fi
echo "Credentials are stored in generated/secrets/ and were not printed."
echo "Commands: ./manage.sh status | height | peers | verify | explorer status"
echo "Stop safely: ./manage.sh stop (named volumes are preserved)"
