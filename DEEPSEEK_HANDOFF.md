# Handoff — elements-localnet Phase 2A

## Purpose and architecture

`elements-localnet` is a Docker Compose generator for a private, `OP_TRUE`-signed
Elements development chain. It uses unmodified Elements Core, one walletless block
scheduler, isolated node datadirs, and a read-only Go service that is now both the
network telemetry dashboard and a full block explorer. It is not a public network, a
controller, or a wallet UI.

Pinned upstream: Elements Core `23.3.4`, tag `elements-23.3.4`, upstream short commit
`ca17280`. Official amd64 archive SHA-256:
`a758151ace3f21008ab162067ffce9e0e526a1b5d55995e2c30d9cd7ccda41a0`.

## Topology

- `node-01` — role `producer`; sole target of the separate producer service;
  wallet-capable but normally `listwallets == []`.
- `node-02` — role `validator`, capabilities `archive` and `txindex`; unpruned;
  preferred RPC source for the explorer index.
- `node-03`, `node-04`, `node-05` — role `validator`, `disablewallet=1`.
- Full mesh, exactly one link per pair. Lower-numbered nodes dial higher-numbered
  ones, so each node has `NODE_COUNT - 1` internal peers.
- Windows Elements-Qt connects as an external sixth node through
  `127.0.0.1:7142`–`7146`. It is not one of the five managed nodes.
- Each node owns one external named volume, one config, an admin RPC credential, a
  whitelist-restricted monitor credential, and a whitelist-restricted explorer
  credential.
- The producer sends exactly `generatetoaddress 1 <public-address>` every 60 s. A
  project volume `flock` plus a fixed container name prevents duplicate loops.

## What Phase 2A changed

- `network-status/` became `explorer/`. The service keeps every Phase 1 endpoint and
  adds the explorer API, the SQLite index, and a new browser application.
- A third RPC identity per node, `explorer_node_NN`, whitelisted to ten read-only
  chain methods. `scripts/ensure-explorer-credentials.sh` adds it to an already
  generated network without regenerating any existing credential.
- P2P publishing moved from `0.0.0.0` to `127.0.0.1` by default, with an explicit
  `P2P_EXPOSURE` of `localhost`, `lan`, or `disabled`.
- `setup-network.sh` gained a twelve-question guided wizard, full flag coverage,
  port-collision detection, topology selection, and an existing-payout-address path.
  Non-interactive mode never prompts.
- `manage.sh` gained `explorer status|logs|restart|backup|reindex`.
- `tests/static-checks.sh` can no longer PASS because a scanner is missing.
- New docs: `docs/EXPLORER_ARCHITECTURE.md`, `docs/EXPLORER_SCHEMA.md`.

## Explorer in one page

- One Go binary, one image `elements-localnet-explorer:phase2a`, one origin
  <http://127.0.0.1:8080>. Static assets are embedded; no CDN, no Node.js runtime.
- Telemetry poller and chain indexer are independent goroutines. A stalled indexer
  cannot block telemetry.
- The index is SQLite in WAL mode (`modernc.org/sqlite`, pure Go, `CGO_ENABLED=0`) in
  its own named volume `<namespace>-explorer-db`. Schema version `1`.
- Indexing is resumable: the cursor lives in `meta.indexed_height` /
  `meta.indexed_hash`, continuity is checked every step, reorgs roll back one block
  at a time inside a transaction, and the replacement branch is then indexed forward.
- Bounded work per step, bounded batches, bounded exponential backoff (1 s → 60 s),
  `io.LimitReader` on every response. ~19 MiB resident on a 29,000-block chain.
- Confidentiality is enforced in the schema: a blinded amount is `NULL`, never `0`,
  and aggregates exclude `NULL`. `assets.issued_sats` is `NULL` whenever any issuance
  of that asset is blinded.
- Asset IDs and reissuance tokens are recomputed locally with upstream's fast merkle
  hash and compared against what the node reports; `derivation_verified` records the
  result. Unit tests pin the algorithm against all five issuances on this chain.

## Current runtime

Expected services: five healthy node containers, one healthy `producer`, one healthy
`explorer`. Explorer at <http://127.0.0.1:8080>. RPC ports `127.0.0.1:7041`–`7045`.
P2P ports `127.0.0.1:7142`–`7146`. Chain height at handoff: 28,972, index
synchronized, five assets discovered. See `PROJECT_STATE.md` for the live figures and
`ACCEPTANCE_TEST_REPORT.md` for the tested baseline.

## Rules for the next session

- Read `PROJECT_STATE.md` first, then confirm the running state with read-only
  commands. Do not trust documentation over the running system.
- Never read, move, load, print, modify, overwrite, index, or commit
  `generated/secrets/wallet/payout-wallet.backup`. The payout wallet is in use from
  Windows Elements-Qt; do not load the Docker copy of the same wallet.
- Never reset, invalidate, or fast-forward the live chain. Destructive fork tests go
  in a separate disposable Compose project with separate volumes.
- `./manage.sh explorer reindex --yes-i-understand` may delete only the explorer
  index volume. It must never touch node volumes, chain data, wallets, credentials,
  or `generated/public/assets.json`.
- Public RPC stays forbidden. P2P stays on loopback unless the owner explicitly
  chooses `lan`.
- Nothing has been pushed and no remote exists. Creating a remote or pushing requires
  explicit owner authorization.

## Out of scope until a later phase

Sending transactions from the browser, wallet loading or creation from the browser,
private key handling, a faucet, `issueasset` or reissuance controls, browser-based
node creation or removal, a Docker control API, public internet deployment,
rebranding Elements Core, consensus changes, and any new coin or source fork. These
need authentication and stronger isolation and belong to Phase 3 and later.

## Commands

```bash
cd /home/lordegypt/ChainProject/elements-localnet
./manage.sh status
./manage.sh verify
./manage.sh explorer status
./tests/dashboard-check.sh
./tests/static-checks.sh
docker compose build explorer     # runs go vet and go test inside the build
```
