#!/usr/bin/env bash
set -Eeuo pipefail

RPC_URL="${RPC_URL:-http://node-01:7040}"
RPC_CREDENTIALS_FILE="${RPC_CREDENTIALS_FILE:-/run/localnet/node-01.rpc}"
PAYOUT_ADDRESS_FILE="${PAYOUT_ADDRESS_FILE:-/run/localnet/payout-address}"
BLOCK_INTERVAL="${BLOCK_INTERVAL:-60}"
PRODUCER_MODE="${PRODUCER_MODE:-automatic}"
STATUS_FILE="${STATUS_FILE:-/producer-status/status.json}"

[[ "${BLOCK_INTERVAL}" =~ ^[1-9][0-9]*$ ]] || { echo "BLOCK_INTERVAL must be positive." >&2; exit 2; }
[[ "${PRODUCER_MODE}" == "automatic" || "${PRODUCER_MODE}" == "manual" ]] || { echo "PRODUCER_MODE must be automatic or manual." >&2; exit 2; }
[[ -r "${RPC_CREDENTIALS_FILE}" ]] || { echo "RPC credential file is unavailable." >&2; exit 1; }
[[ -r "${PAYOUT_ADDRESS_FILE}" ]] || { echo "Payout address file is unavailable." >&2; exit 1; }

RPC_USER="$(awk -F= '$1 == "RPC_USER" {print substr($0, index($0, "=") + 1)}' "${RPC_CREDENTIALS_FILE}")"
RPC_PASSWORD="$(awk -F= '$1 == "RPC_PASSWORD" {print substr($0, index($0, "=") + 1)}' "${RPC_CREDENTIALS_FILE}")"
PAYOUT_ADDRESS="$(tr -d '\r\n' <"${PAYOUT_ADDRESS_FILE}")"
[[ -n "${RPC_USER}" && -n "${RPC_PASSWORD}" ]] || { echo "RPC credential file is malformed." >&2; exit 1; }
[[ "${PAYOUT_ADDRESS}" =~ ^[A-Za-z0-9]{20,120}$ ]] || { echo "Payout address is malformed." >&2; exit 1; }

exec 9>/producer-lock/producer.lock
if ! flock -n 9; then
  echo "Another block producer holds the shared lock; refusing to start." >&2
  exit 1
fi

masked_payout="${PAYOUT_ADDRESS:0:6}...${PAYOUT_ADDRESS: -6}"
stopping="no"
sleep_pid=""
rpc_pid=""
RPC_RESULT=""
last_attempt=""
last_success=""
last_height="0"
last_hash=""
next_attempt=""
last_error=""
consecutive_failures="0"
write_status() {
  local running="$1" tmp="${STATUS_FILE}.tmp.$$"
  jq -n \
    --argjson enabled true --argjson running "${running}" \
    --arg payout "${masked_payout}" --argjson interval "${BLOCK_INTERVAL}" \
    --arg attempt "${last_attempt}" --arg success "${last_success}" \
    --argjson height "${last_height}" --arg hash "${last_hash}" \
    --arg next "${next_attempt}" --arg error "${last_error}" --argjson failures "${consecutive_failures}" \
    '{enabled:$enabled,running:$running,payoutAddressMasked:$payout,blockInterval:$interval,lastAttempt:$attempt,lastSuccess:$success,lastProducedTimestamp:$success,lastProducedHeight:$height,lastProducedBlockHash:$hash,nextScheduledAttempt:$next,lastError:$error,consecutiveFailures:$failures}' >"${tmp}"
  chmod 644 "${tmp}"
  mv "${tmp}" "${STATUS_FILE}"
}
shutdown() {
  stopping="yes"
  if [[ -n "${sleep_pid}" ]]; then kill "${sleep_pid}" 2>/dev/null || true; fi
  if [[ -n "${rpc_pid}" ]]; then kill "${rpc_pid}" 2>/dev/null || true; fi
  next_attempt=""
  write_status false || true
}
trap shutdown TERM INT

rpc() {
  local method="$1"
  local params="$2"
  local payload response_file rc
  payload="$(jq -cn --arg method "${method}" --argjson params "${params}" '{jsonrpc:"1.0",id:"elements-localnet-producer",method:$method,params:$params}')"
  response_file="$(mktemp)"
  curl --fail-with-body --silent --show-error \
    --connect-timeout 2 --max-time 5 \
    --user "${RPC_USER}:${RPC_PASSWORD}" \
    --header 'content-type: application/json' \
    --data-binary "${payload}" "${RPC_URL}/" >"${response_file}" &
  rpc_pid=$!
  rc=0; wait "${rpc_pid}" || rc=$?; rpc_pid=""
  if ((rc != 0)) || [[ "${stopping}" != "no" ]]; then rm -f "${response_file}"; return 1; fi
  RPC_RESULT="$(<"${response_file}")"; rm -f "${response_file}"
  if [[ "$(jq -r '.error // empty' <<<"${RPC_RESULT}")" != "" ]]; then
    jq -c '.error | {code,message}' <<<"${RPC_RESULT}" >&2
    return 1
  fi
  RPC_RESULT="$(jq -c '.result' <<<"${RPC_RESULT}")"
}

mine_once() {
  local attempt result block_hash height delay
  for attempt in 1 2 3 4 5; do
    last_attempt="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
    write_status true
    if rpc generatetoaddress "$(jq -cn --arg address "${PAYOUT_ADDRESS}" '[1,$address]')"; then
      result="${RPC_RESULT}"
      block_hash="$(jq -r '.[0]' <<<"${result}")"
      rpc getblockcount '[]' || return 1
      height="$(tr -d '[:space:]' <<<"${RPC_RESULT}")"
      last_success="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
      last_height="${height}"
      last_hash="${block_hash}"
      last_error=""
      consecutive_failures="0"
      next_attempt="$(date -u -d "@$(( $(date +%s) + BLOCK_INTERVAL ))" +'%Y-%m-%dT%H:%M:%SZ')"
      write_status true
      printf '%s height=%s hash=%s payout=%s\n' "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" "${height}" "${block_hash}" "${PAYOUT_ADDRESS}"
      return 0
    fi
    delay=$((2 ** (attempt - 1)))
    last_error="RPC attempt ${attempt} failed; retry scheduled"
    write_status true
    echo "RPC attempt ${attempt}/5 failed; retrying in ${delay}s." >&2
    sleep "${delay}" & sleep_pid=$!; wait "${sleep_pid}" || true; sleep_pid=""
    [[ "${stopping}" == "no" ]] || return 1
  done
  consecutive_failures=$((consecutive_failures + 1))
  last_error="block production failed after 5 bounded retries"
  write_status true
  return 1
}

echo "Block producer acquired the singleton lock; mode=${PRODUCER_MODE}, interval=${BLOCK_INTERVAL}s, payout=${PAYOUT_ADDRESS}."
write_status true
if [[ "${PRODUCER_MODE}" == "manual" ]]; then
  mine_once
  write_status false
  exit
fi

while [[ "${stopping}" == "no" ]]; do
  mine_once || true
  [[ "${stopping}" == "no" ]] || break
  sleep "${BLOCK_INTERVAL}" & sleep_pid=$!; wait "${sleep_pid}" || true; sleep_pid=""
done
write_status false
echo "Block producer stopped cleanly."
