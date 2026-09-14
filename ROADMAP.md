# Roadmap

## Phase 1 / 1.1 — complete baseline and hardening

Private generated Elements network, one walletless scheduled producer, archive
and validator roles, safe payout bootstrap/backup, management scripts, runtime
verification, and a read-only network status dashboard.

## Phase 2A — read-only Elements block explorer (complete)

`network-status` became the unified `explorer` service: one Go binary, one image,
one origin at <http://127.0.0.1:8080>. A resumable, reorg-aware incremental indexer
reads from the archival `node-02` and writes blocks, transactions, inputs, outputs,
issuances, assets, address history and mempool into a SQLite index in its own named
volume. The browser application adds Overview, Blocks, Transactions, Assets,
Mempool, Nodes, detail pages and global search, with confidential values reported
honestly and never estimated. P2P publishing moved to loopback by default, and
`setup-network.sh` gained a guided wizard that keeps full non-interactive support.

Out of scope in 2A and still unimplemented: sending transactions, wallet loading or
creation from the browser, private key handling, a faucet, `issueasset` or
reissuance controls, browser-based node lifecycle, a Docker control API, public
deployment, rebranding, and consensus changes.

## Phase 2B — explorer depth (not started)

Charts and history series, richer mempool detail, per-asset holder views, an
optional export API, and peg-in/peg-out rendering validated against real peg
traffic.

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
