#!/usr/bin/env bash
set -Eeuo pipefail

status="${STATUS_FILE:-/producer-status/status.json}"
[[ -r "${status}" ]] || exit 1
jq -e '.enabled == true and .running == true and (.consecutiveFailures // 0) < 2 and (.lastError // "") == ""' "${status}" >/dev/null
