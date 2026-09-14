# Project state

- Working name: `elements-localnet`
- Current phase: **Phase 2A complete** — unified read-only block explorer
- Date: 2026-09-15 (Europe/Istanbul)
- Elements: official `elements-23.3.4`, upstream short commit `ca17280`
- Archive SHA-256: `a758151ace3f21008ab162067ffce9e0e526a1b5d55995e2c30d9cd7ccda41a0`
- Host: Windows 11 WSL2, x86_64; Docker 29.7.2; Compose 5.5.1
- Repository: local Git repository, no remote configured, nothing pushed.
  Use `git rev-parse HEAD` for the current commit.

## Active runtime

- Five healthy nodes: `node-01` producer role; `node-02`–`node-05` validator role.
  `node-02` capabilities are `archive` and `txindex`.
- Topology: full mesh, exactly one link per pair (10 internal links).
- One healthy `producer` service, one block per request, interval `60s`.
- One healthy `explorer` service replacing Phase 1's `network-status`:
  <http://127.0.0.1:8080>.
- Chain height at the time of writing: **28,972**; all five nodes agree.
- Genesis: `8c314e7041f731ab5fbcacb20eb31c8567b73643af4fdbf05ecae52e9552ac8d`
- Policy (fee/subsidy) asset:
  `6dd9e5bee5f86e1857389d1c8a0be8eeba4e463b902ec07224d6bf519b7321ee`
- Active volumes use prefix `elements-localnet-phase11-`, now including
  `elements-localnet-phase11-explorer-db`.
- Wallet state: node-01 `listwallets == []`; node-02..05 use `disablewallet=1`.
  The payout wallet is used from Windows Elements-Qt; the Docker copy stays unloaded.
- Windows Elements-Qt is connected as an external sixth node and reaches all five
  managed nodes through `127.0.0.1:7142`–`7146`.

## Explorer

- Image: `elements-localnet-explorer:phase2a`
- Local image ID: `sha256:91da3960df0ce1df3fdf178499a4bc1de16b36dc1ff0445a8c488ea3969f89f1`
  (no registry digest; nothing has been pushed)
- Image size: 15.8 MB, `FROM scratch`, non-root, `cap_drop: ALL`,
  `no-new-privileges`, read-only root filesystem
- Index schema version: **1** (`explorer/schema.go`)
- Indexed height / chain height: **28,972 / 28,972**, lag 0, synchronized
- Indexed: 28,973 blocks, 28,978 transactions, 5 assets, 0 mempool transactions
- Initial sync from genesis: **21.3 s** (a fresh rebuild measured 22 s)
- Database: 74.4 MB plus a 6.8 MB WAL (81.2 MB on the volume); 61 MiB compacted
- Explorer resident memory: ~19 MiB
- RPC source: `node-02` preferred, failover to any other healthy node

## Assets on this chain

| Asset ID | Issuance | Supply |
|---|---|---|
| `525cac3bdad6dffaf99d2dec79e9659ecf68b11d618cf27c8bf91d406e235d05` | confidential | not publicly verifiable |
| `4b1c0cf75639b61f486b1057d11fd4b8155c5cddf8058badfc7dff36a5502aab` | confidential | not publicly verifiable |
| `8071f132279fd8765b89d9ac8a87a735ca753ea7f0f5956dba6413d9a38ea2a8` | confidential | not publicly verifiable |
| `6d177c3509ad78f34455de95a6815e7f1f57f512f0bc257559953606382e96b1` | explicit | 21,000,000.00000000 |
| `72f6aa6d36504910cac7f8af1fadb2ad2b119a32777cc1e43a4fd1d134d42212` | explicit | 21,000,000.00000000 |

The first three were issued at heights 4048, 4049 and 4053 with blinded amounts. The
last two were issued at height 28,734 from Windows Elements-Qt with explicit amounts.
All five recomputed Asset IDs and reissuance tokens match the node
(`derivationVerified: true`), and all five have an explicit zero reissuance token,
so all five are fixed supply. None is marked official; all are test assets.

Descriptive metadata for the first three lives in `generated/public/assets.json`
(git-ignored, seeded from the tracked `config/assets.example.json`). The two Qt
issuances have no metadata entry, so the explorer shows them without a name.

## Exact build, start, stop and test commands

```bash
cd /home/lordegypt/ChainProject/elements-localnet

# Build only the explorer image (runs go vet and go test inside the build)
docker compose build explorer

# Start / stop everything; volumes and chain state are always preserved
./manage.sh start
./manage.sh stop
./manage.sh restart

# Explorer lifecycle
./manage.sh explorer status
./manage.sh explorer logs
./manage.sh explorer restart
./manage.sh explorer backup
./manage.sh explorer reindex --yes-i-understand   # deletes only the index volume

# Tests
docker run --rm -v "$PWD/explorer":/src -w /src \
  docker.io/library/golang:1.25.7-bookworm sh -c 'go vet ./... && go test ./...'
./tests/static-checks.sh
./tests/dashboard-check.sh
./manage.sh verify
docker compose config --quiet
```

## Test status

All Phase 2A checks passed; see `ACCEPTANCE_TEST_REPORT.md` for the full list and the
two SKIPs. Go tests cover asset-ID derivation against all five on-chain issuances,
exact satoshi parsing, subsidy halving, apply/rollback, reorg following via an
in-process fake node, cursor resume, search across every index, pagination bounds,
and error sanitization. Live checks cover genesis, tip and five sampled heights
against direct RPC, transaction counts, asset discovery, confidential labelling,
search, pagination limits, isolation, and secret scanning.

## Known limitations

- Only `linux/amd64` is built.
- Blinded amounts and blinded assets remain unknown; no proof interpretation is
  attempted. Supply for a confidential issuance is reported as not publicly
  verifiable and is never filled in from an owner's claim.
- The policy asset has no issuance transaction, so it has no `assets` row; it is
  labelled as the policy asset wherever it appears.
- Peg-in and peg-out fields are indexed and rendered but untested against real peg
  traffic (`validatepegin=0` on this chain).
- Mempool rows carry only what `getrawmempool true` returns; full mempool
  transaction bodies are not indexed.
- Index growth is roughly 2.2 KB per block at one transaction per block.
- `shellcheck` and `ripgrep` are not installed on this host, so those checks report
  SKIP and the grep fallback path is the one exercised.
- Compose may still mention detached Phase 1 legacy volumes sharing old labels; they
  are intentionally retained and attached to nothing.

## Resume / exact next step

The working network is healthy and the explorer is synchronized. Nothing is blocked.

```bash
cd /home/lordegypt/ChainProject/elements-localnet
./manage.sh status
./manage.sh verify
./manage.sh explorer status
./tests/dashboard-check.sh
```

Next safe resume point is Phase 2B (explorer depth: charts and history series,
richer mempool detail, per-asset holder views, peg traffic rendering). Do **not**
start wallet, faucet, asset-control, or node-control work; those are Phase 3 and
Phase 4 and require authentication and stronger isolation.

Nothing has been pushed. Creating a remote or pushing requires explicit owner
authorization.
