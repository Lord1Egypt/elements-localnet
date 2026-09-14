#!/usr/bin/env bash
set -Eeuo pipefail

# Adds a read-only explorer RPC identity to an already generated network without
# touching existing credentials, wallets, or chain data. Safe to re-run.
# Nodes must be restarted afterwards for elementsd to read the new rpcauth lines.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOT_DIR}/generated/network.env"
[[ -f "${ENV_FILE}" ]] || { echo "Missing ${ENV_FILE}; run setup-network.sh first." >&2; exit 1; }
# shellcheck disable=SC1090
source "${ENV_FILE}"
command -v openssl >/dev/null || { echo "openssl is required to generate credentials." >&2; exit 1; }

EXPLORER_METHODS="getblockchaininfo,getsidechaininfo,getblockhash,getblock,getblockheader,getrawtransaction,getrawmempool,getmempoolinfo,getmempoolentry,getchaintips"
SECRETS_DIR="${ROOT_DIR}/generated/secrets/explorer"
mkdir -p "${SECRETS_DIR}"
chmod 700 "${SECRETS_DIR}"

changed=0
for ((i = 1; i <= NODE_COUNT; i++)); do
  node="$(printf 'node-%02d' "${i}")"
  conf="${ROOT_DIR}/generated/nodes/${node}/elements.conf"
  secret="${SECRETS_DIR}/${node}.rpc"
  [[ -f "${conf}" ]] || { echo "Missing ${conf}." >&2; exit 1; }
  user="explorer_${node//-/_}"

  if [[ -f "${secret}" ]] && grep -q "^rpcwhitelist=${user}:" "${conf}"; then
    continue
  fi
  if [[ -f "${secret}" ]] && ! grep -q "^rpcwhitelist=${user}:" "${conf}"; then
    echo "Refusing to regenerate ${node} explorer credentials: ${secret} exists but its config entry is missing." >&2
    echo "Delete ${secret} deliberately if you intend to issue a new explorer credential." >&2
    exit 1
  fi

  password="$(openssl rand -hex 32)"
  salt="$(openssl rand -hex 16)"
  hash="$(printf '%s' "${password}" | openssl dgst -sha256 -hmac "${salt}" | awk '{print $NF}')"

  tmp_conf="${conf}.tmp.$$"
  cp "${conf}" "${tmp_conf}"
  {
    printf '\n# Read-only explorer identity added by scripts/ensure-explorer-credentials.sh\n'
    printf 'rpcauth=%s:%s$%s\n' "${user}" "${salt}" "${hash}"
    printf 'rpcwhitelist=%s:%s\n' "${user}" "${EXPLORER_METHODS}"
  } >>"${tmp_conf}"
  chmod 600 "${tmp_conf}"
  mv "${tmp_conf}" "${conf}"

  tmp_secret="${secret}.tmp.$$"
  printf 'RPC_USER=%s\nRPC_PASSWORD=%s\n' "${user}" "${password}" >"${tmp_secret}"
  chmod 600 "${tmp_secret}"
  mv "${tmp_secret}" "${secret}"
  changed=$((changed + 1))
  echo "Added a read-only explorer RPC identity to ${node}."
done

if ((changed == 0)); then
  echo "Every node already has a read-only explorer RPC identity."
else
  echo "Restart the Elements nodes so the new rpcauth entries take effect."
fi
