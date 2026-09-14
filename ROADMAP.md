# Roadmap

## Phase 1 / 1.1 — complete baseline and hardening

Private generated Elements network, one walletless scheduled producer, archive
and validator roles, safe payout bootstrap/backup, management scripts, runtime
verification, and a read-only network status dashboard.

## Phase 2 — indexer and Elements block explorer

Add an Elements-compatible indexer/explorer backed by the dedicated archival
RPC node. Do not fold explorer transaction/address behavior into
`network-status`.

Phase 2 remains explicitly unauthorized until the user approves it.

## Phase 3 — control dashboard and localhost controller API

Add authenticated localhost-only lifecycle/control operations without browser
Docker-socket access. Keep monitoring and mutation boundaries explicit.

## Phase 4 — wallet and reward scalability

Implement reward-wallet epochs, matured-UTXO consolidation, wallet archival and
unloading, an optional faucet, PSET/raw construction, external signing, and
rescan avoidance for very long chains.

## Phase 5 — unique chain identity and branding

Source-level name/ticker/icon changes, address prefixes, ports, data directory,
and a deliberately unique chain identity. Preserve all upstream notices.

## Phase 6 — public-network consensus security

Replace the private OP_TRUE signing model with a properly designed Strong
Federation or real Proof-of-Work system. OP_TRUE is never a public security
model.

## Phase 7 — release engineering

Release packaging, multi-architecture images, CI, security hardening,
reproducible builds, signed artifacts, and upgrade/migration policy.
