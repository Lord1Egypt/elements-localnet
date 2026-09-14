#!/usr/bin/env bash
set -Eeuo pipefail

elements-cli \
  -chain=elements \
  -datadir=/data \
  -conf=/config/elements.conf \
  -rpcwait \
  -rpcwaittimeout=5 \
  getblockchaininfo >/dev/null

# Do not declare the seed healthy until its P2P listener is accepting connections.
timeout 2 bash -c '</dev/tcp/127.0.0.1/7042'
