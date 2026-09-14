# Phase 1 acceptance test report

Date: 2026-09-14 (Europe/Istanbul)

## Tested build

- Host: Windows 11 + WSL2, x86_64, kernel
  `6.6.87.2-microsoft-standard-WSL2`
- Docker Engine: `29.7.2`, build `a7dcaa6`
- Docker Compose: `v5.5.1`
- Elements: official `v23.3.4`, release tag `elements-23.3.4`, upstream short
  commit `ca17280`
- Elements archive SHA-256:
  `a758151ace3f21008ab162067ffce9e0e526a1b5d55995e2c30d9cd7ccda41a0`
- Debian base index digest:
  `sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171`
- Go builder index digest:
  `sha256:564e366a28ad1d70f460a2b97d1d299a562f08707eb0ecb24b659e5bd6c108e1`
- Local image IDs at the recorded build:
  - node: `sha256:63dc36b42fa6b516176d866bc5f62648cc9d8c2b667d51f08091d695417cd5d8`
  - producer: `sha256:265878f9fdf0cc786463c114f633867e6ddca51f05e921f556f8a50c5b3995fe`
  - network-status: `sha256:e30f3605001fd2c22a009c63897a8a17590b27589ccff2b3405e55e31f700c57`

Local image IDs may change after a source rebuild; the upstream inputs remain
digest/checksum pinned.

## Exact commands

```bash
./setup-network.sh --nodes 3 --block-reward-sats 5000000000 \
  --halving-interval 210000 --block-interval 60 --auto-mine yes
(cd network-status && GOTOOLCHAIN=local \
  GOCACHE=/tmp/elements-localnet-gocache go test ./...)
./tests/static-checks.sh
./manage.sh verify --acceptance
./manage.sh verify
./tests/dashboard-check.sh
docker stats --no-stream elements-localnet-network-status
```

The outage/recovery test stopped only node-03, measured `/api/v1/network`,
generated a walletless block gap, restarted node-03, and polled its status. A
20,000-block gap was used to observe real `SYNCING`; after capturing the state,
the test-only branch was reversibly rolled back with `invalidateblock` at height
109 on all nodes because full single-seed catch-up was too slow for the test
window. No volume was deleted. The active network was restored to a common tip
and production resumed.

## Core network criteria

| # | Criterion | Result | Evidence |
|---:|---|---|---|
| 1 | Three-node network starts | PASS | node-01..03 running |
| 2 | All containers healthy | PASS | Nodes, producer, and dashboard all report Docker `healthy` |
| 3 | Same genesis | PASS | `8c314e7041f731ab5fbcacb20eb31c8567b73643af4fdbf05ecae52e9552ac8d` |
| 4 | Nodes connect to peers | PASS | counts 2, 1, 1 |
| 5 | Only one producer | PASS | exactly `elements-localnet-producer` |
| 6 | Producer pays 50 units | PASS | block 1 output `50.00000000` to payout address |
| 7 | Block synchronizes everywhere | PASS | matching heights/hashes |
| 8 | First reward immature | PASS | deliberate load reported immature balance (`150.00000000` total at height 3) |
| 9 | First reward spendable at 101 | PASS | trusted balance `50.00000000` at height 101 |
| 10 | Wallet unloaded after test | PASS | corrected compact `listwallets` result `[]` |
| 11 | Normal operation walletless | PASS | `listwallets == []`; validators disable wallet |
| 12 | Restart preserves data | PASS | height 101 before and after restart |
| 13 | Heights match after restart | PASS | all height 101 in maturity run |
| 14 | No secrets tracked/output | PASS | generated tree ignored; runtime HTML/API/log scans found none; workspace is not a Git repository |
| 15 | Shellcheck if available | SKIP | shellcheck not installed; `bash -n` and strict-mode tests passed |
| 16 | `docker compose config` | PASS | quiet validation succeeded |
| 17 | Clean verifier | PASS | final `./manage.sh verify` passed |

## Dashboard amendment criteria

| # | Criterion | Result | Evidence |
|---:|---|---|---|
| 1 | Image builds | PASS | multi-stage Go build and in-image tests succeeded |
| 2 | `/healthz` healthy | PASS | HTTP 200, `{"status":"ok"}` |
| 3 | Browser page loads | PASS | `GET /` HTTP 200 on `127.0.0.1:8080` |
| 4 | Three nodes appear | PASS | API `totalNodes=3` and three node records |
| 5 | Roles correct | PASS | producer/archive/validator |
| 6 | All synchronized nodes healthy | PASS | three `HEALTHY` records |
| 7 | Height/hash match RPC | PASS | direct/API both height 109 and identical hash in recorded comparison |
| 8 | Producer/reward schedule accurate | PASS | enabled/running, 60s, 5,000,000,000 sats, era 0, next 210000 |
| 9 | One stopped validator offline | PASS | node-03 only `OFFLINE`; online 2, offline 1 |
| 10 | Restart shows syncing then healthy | PASS | real `SYNCING` captured; restored common-tip node later `HEALTHY` |
| 11 | Equal-height mismatch diverged | PASS | Go unit `TestEqualHeightMismatchIsDiverged` |
| 12 | Dashboard never accesses wallet | PASS | hard-coded method set has no wallet RPC; whitelist test denies wallet RPC with HTTP 403 |
| 13 | No Docker socket | PASS | runtime mounts empty for socket; source scan clean |
| 14 | No secrets in output | PASS | HTML, JSON, and status logs scanned against generated RPC passwords |
| 15 | Responsive during timeout | PASS | API response `<0.01s` with node-03 offline; two nodes remained online |
| 16 | Memory recorded | PASS | network-status `4.113 MiB`, `0.03%` in the final runtime sample |
| 17 | Docs and report updated | PASS | README, PROJECT_STATE, DECISIONS, this report |

## Additional checks

- All documented read-only API endpoints returned HTTP 200.
- Unknown node ID returned HTTP 404.
- Monitor identity wallet call returned HTTP 403 from Elements RPC whitelist.
- Dashboard runtime user was `status:status`, not root.
- Published ports were exactly `127.0.0.1:7041-7043` and
  `127.0.0.1:8080`; P2P had no host mapping.
- Go unit tests cover divergence, syncing/fork/stale priority, integer reward
  economics, canonical aggregation, offline visibility, and disagreement.

## Known limitations

- Shellcheck could not be run because it is absent.
- The parent directory is not a Git worktree, so there is no commit ID and no
  actual Git index to inspect. `.gitignore`, `.dockerignore`, and runtime secret
  scans passed.
- A very large catch-up on a single seed is intentionally not optimized in
  Phase 1. The dashboard remained responsive and did not serialize on the slow
  node.
- Only amd64 is supported by the pinned Elements artifact in this phase.

## Phase 1.1 hardening report

Date: 2026-09-14. No 20,000-block fixture was repeated. The normal runtime was
migrated to fresh `elements-localnet-phase11-*` volumes; all five legacy Phase
1 volumes remain detached and preserved.

| # | Result | Evidence |
|---:|:---:|---|
| 1 | PASS | Five node containers healthy |
| 2 | PASS | Genesis `8c314e7041f731ab5fbcacb20eb31c8567b73643af4fdbf05ecae52e9552ac8d` on all |
| 3 | PASS | Stable tip height 215/hash `14c407a423b855fa41b4c3896e721d340026b20ad60032d53261b7365e1bbf86` on all |
| 4 | PASS | Peer counts 4,1,1,1,1 |
| 5 | PASS | Exactly one healthy producer service |
| 6 | PASS | Validators have no producer loop or generation credentials |
| 7 | PASS | node-01 `listwallets=[]`; validators `disablewallet=1` |
| 8 | PASS | Heights 97–102 logged consecutively at approximately one-second cadence |
| 9 | PASS | All four validators reached the steady producer tip/hash |
| 10 | PASS | `producer stop` stopped scheduled generation |
| 11 | PASS | Height remained 103 for two additional polling cycles |
| 12 | PASS | Start resumed production from 103 to 107 |
| 13 | PASS | Producer restart retained `1s`, height 107 to 111 |
| 14 | PASS | Compose restart preserved genesis/data and five-node topology |
| 15 | PASS | API reports one producer, four validators, node-02 archive capability |
| 16 | PASS | API hash equals direct `getblockhash` at reported height |
| 17 | PASS | Active node log scan found no invalid-chain/corruption warning |
| 18 | PASS | RPC 7041–7045 and dashboard 8080 bind only to `127.0.0.1` |
| 19 | PASS | Dashboard has no Docker socket mount |
| 20 | PASS | Credential/output/static secret scans clean |
| 21 | PASS | `docker compose config --quiet` |
| 22 | PASS | Go tests, Bash syntax/static, verifier, dashboard checks |

Invalid interval values `0`, `-1`, empty, and non-numeric all exited 2. A valid
`producer interval 1` preserved all five node container IDs and confirmed the
effective value. Shellcheck was still unavailable; `bash -n` passed.
