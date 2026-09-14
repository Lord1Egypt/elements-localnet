#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ADDRESS_FILE="${ROOT_DIR}/generated/public/payout-address"
BACKUP_FILE="${ROOT_DIR}/generated/secrets/wallet/payout-wallet.backup"
COMPOSE=(docker compose -f "${ROOT_DIR}/compose.yaml")
CLI=(elements-cli -chain=elements -datadir=/data -conf=/config/elements.conf)

wallet_loaded="no"
cleanup() {
  if [[ "${wallet_loaded}" == "yes" ]]; then
    "${COMPOSE[@]}" exec -T node-01 "${CLI[@]}" unloadwallet payout >/dev/null 2>&1 || true
  fi
  "${COMPOSE[@]}" exec -T node-01 sh -c 'rm -f /data/payout-wallet.bootstrap-backup' >/dev/null 2>&1 || true
}
trap cleanup EXIT

wallets="$("${COMPOSE[@]}" exec -T node-01 "${CLI[@]}" listwalletdir)"
if [[ -s "${ADDRESS_FILE}" && -s "${BACKUP_FILE}" ]]; then
  if printf '%s' "${wallets}" | grep -q '"payout"'; then
    echo "Payout wallet backup, public address, and unloaded node wallet already exist."
    exit 0
  fi
  docker cp "${BACKUP_FILE}" elements-localnet-node-01:/data/payout-wallet.bootstrap-backup >/dev/null
  "${COMPOSE[@]}" exec -T node-01 "${CLI[@]}" restorewallet payout /data/payout-wallet.bootstrap-backup >/dev/null
  wallet_loaded="yes"
  "${COMPOSE[@]}" exec -T node-01 "${CLI[@]}" unloadwallet payout >/dev/null
  wallet_loaded="no"
  "${COMPOSE[@]}" exec -T node-01 sh -c 'rm -f /data/payout-wallet.bootstrap-backup'
  trap - EXIT
  echo "Restored the payout wallet from the ignored backup and unloaded it."
  exit 0
fi

if printf '%s' "${wallets}" | grep -q '"payout"'; then
  "${COMPOSE[@]}" exec -T node-01 "${CLI[@]}" loadwallet payout >/dev/null
else
  "${COMPOSE[@]}" exec -T node-01 "${CLI[@]}" createwallet payout false false '' false true false >/dev/null
fi
wallet_loaded="yes"

address="$("${COMPOSE[@]}" exec -T node-01 "${CLI[@]}" -rpcwallet=payout getnewaddress 'block rewards' bech32 | tr -d '\r\n')"
[[ "${address}" =~ ^[A-Za-z0-9]{20,120}$ ]] || { echo "Elements returned an invalid payout address." >&2; exit 1; }
"${COMPOSE[@]}" exec -T node-01 "${CLI[@]}" -rpcwallet=payout backupwallet /data/payout-wallet.bootstrap-backup >/dev/null
"${COMPOSE[@]}" exec -T node-01 "${CLI[@]}" unloadwallet payout >/dev/null
wallet_loaded="no"

mkdir -p "$(dirname "${ADDRESS_FILE}")" "$(dirname "${BACKUP_FILE}")"
printf '%s\n' "${address}" >"${ADDRESS_FILE}"
chmod 644 "${ADDRESS_FILE}"
docker cp elements-localnet-node-01:/data/payout-wallet.bootstrap-backup "${BACKUP_FILE}" >/dev/null
chmod 600 "${BACKUP_FILE}"
"${COMPOSE[@]}" exec -T node-01 sh -c 'rm -f /data/payout-wallet.bootstrap-backup'
trap - EXIT
echo "Created payout address and an ignored local wallet backup; wallet is unloaded."
