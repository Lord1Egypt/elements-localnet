# Acceptance test report — Phase 2A (read-only Elements block explorer)

- Date: 2026-09-15 (Europe/Istanbul)
- Host: Windows 11 WSL2, x86_64; Docker 29.7.2; Compose 5.5.1
- Elements Core `23.3.4`, archive SHA-256
  `a758151ace3f21008ab162067ffce9e0e526a1b5d55995e2c30d9cd7ccda41a0`
- Explorer image `elements-localnet-explorer:phase2a`, local ID
  `sha256:91da3960df0ce1df3fdf178499a4bc1de16b36dc1ff0445a8c488ea3969f89f1`
- Index schema version `1`
- Chain height during testing: 28,960 → 28,973

The Phase 1.1 baseline was preserved throughout. The chain was never reset, no
invalidation experiment was run, and the 20,000-block test was not repeated.

## Result summary

| # | Requirement | Result | Evidence |
|---:|---|---|---|
| 1 | Existing five-node network stays healthy | **PASS** | `./manage.sh verify`: all five nodes healthy, identical genesis, 5 peers each, matching height 28,967 |
| 2 | Producer remains the only automatic producer | **PASS** | `verify`: "single producer"; `docker ps` shows exactly one `elements-localnet-producer`, healthy |
| 3 | Automatic 60-second production continues during explorer operation | **PASS** | Height advanced 28,960 → 28,973 across the session while the explorer indexed and reindexed; `producer.blockInterval == 60` |
| 4 | Explorer indexes from genesis to the current tip | **PASS** | First run: 28,965 blocks in **21.5 s**; rebuild after `reindex`: genesis → tip in ~22 s, ending `synchronized: true`, `lagBlocks: 0` |
| 5 | Indexer resumes from its stored cursor after restart | **PASS** | After `docker compose up -d --force-recreate explorer`: `blocksIndexedThisRun: 0`, still at the tip. Also covered by `TestIndexerResumesFromItsStoredCursor` |
| 6 | Indexed block hashes match direct RPC at genesis, tip, sampled heights | **PASS** | `tests/dashboard-check.sh`: genesis, tip and five random heights compared against `elements-cli getblockhash` |
| 7 | Indexed transaction counts match block RPC data | **PASS** | Same check compares `txCount` against `getblock … 1`'s `nTx` at five random heights |
| 8 | The three known Asset IDs are discoverable | **PASS** | All three resolve via `/api/v1/assets/{id}` and `/api/v1/search`. Two further explicit issuances made from Elements-Qt at height 28,734 were also discovered; five assets total |
| 9 | Confidential issuance is labelled accurately | **PASS** | The three blinded issuances return `issuedSats: null`, `confidentialIssuance: true`, `supplyState: "not publicly verifiable"`. The two explicit ones return a verifiable 21,000,000.00000000 |
| 10 | Search resolves height, block hash, txid, Asset ID, address | **PASS** | All five verified live; a reissuance-token hash resolves to its asset. Also `TestSearchReturnsEveryMatchingIndex` |
| 11 | Pagination limits are enforced | **PASS** | `limit=500` clamped to 100; `limit=0` rejected with HTTP 400. Also `TestPageParamsEnforcesBounds` and `TestBlockListPagination` |
| 12 | Reorg logic covered by an isolated fixture, not the live chain | **PASS** | `TestIndexerDetectsReorgAndFollowsTheNewBranch` serves a synthetic chain from an in-process fake node, forks it at height 2, and asserts rollback of 3 blocks and adoption of the replacement branch. Plus `TestRollbackRestoresSpentOutputsAndRemovesAssets` |
| 13 | Explorer database survives container restart | **PASS** | Index intact and at the tip after container recreation, with zero blocks re-indexed |
| 14 | Explorer UI remains responsive while indexing | **PASS** | During a full rebuild, `/api/v1/explorer/status` and `/api/v1/blocks` answered HTTP 200 in 1–5 ms at every 2-second sample from 0 % to 100 % |
| 15 | Existing network-status endpoints remain compatible | **PASS** | `/healthz`, `/api/v1/network`, `/nodes`, `/nodes/{id}`, `/topology`, `/producer`, `/economics` all pass their original Phase 1 assertions |
| 16 | No wallet RPC is made | **PASS** | Explorer credentials are whitelisted to ten read-only chain methods with `rpcwhitelistdefault=0`; there is no generic RPC route; node-01 `listwallets == []` throughout |
| 17 | No wallet file is mounted | **PASS** | `docker inspect` mounts: inventory, assets.json, monitor secrets, explorer secrets, producer status (all `rw=false`) and the index volume. No wallet path |
| 18 | No Docker socket is mounted | **PASS** | Same mount list contains no `/var/run/docker.sock` |
| 19 | No secrets in HTML, JS, API JSON, logs, image layers, or Git diff | **PASS** | `/`, `/app.js`, `/styles.css`, `/api/v1/network`, `/api/v1/explorer/status`, `/api/v1/nodes` scanned for `rpcauth`, `rpcpassword`, `RPC_PASSWORD`, secret paths and `payout-wallet`: 0 hits. `git ls-files 'generated/**'` is empty except `.gitkeep` |
| 20 | Port 8080 binds only to `127.0.0.1` | **PASS** | `HostConfig.PortBindings` = `{"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"8080"}]}` |
| 21 | P2P defaults to localhost-only | **PASS** | All five nodes now publish `127.0.0.1:7142`–`7146`. `P2P_EXPOSURE=localhost` is the default, `lan` is an explicit opt-in that warns, `disabled` publishes nothing |
| 22 | Go tests pass | **PASS** | `go vet ./...` clean, `gofmt -l` clean, `go test ./...` ok — also re-run inside the image build |
| 23 | Bash syntax and static checks pass | **PASS** | `tests/static-checks.sh`: scanner self-test, syntax and strict mode, forbidden patterns, loopback publishing, no wallet mount, read-only credentials |
| 24 | `docker compose config` passes | **PASS** | `docker compose config --quiet` exits 0 |
| 25 | Report memory, database size, initial indexing duration | **PASS** | 18.93 MiB RSS; 74.4 MB database + 6.8 MB WAL (81.2 MB on the volume, 61 MiB compacted); 21.3 s initial sync |

Two checks report **SKIP**, both for the same reason and both unchanged from Phase 1:

| Check | Result | Reason |
|---|---|---|
| `shellcheck` | **SKIP** | `shellcheck` is not installed on this host |
| ripgrep scanning path | **SKIP** | `ripgrep` is not installed on this host, so `tests/static-checks.sh` exercised the grep fallback instead |

## Measured figures

| Metric | Value |
|---|---|
| Initial index from genesis | 21.3 s for 28,965 blocks (~1,350 blocks/s) |
| Full rebuild after `explorer reindex` | ~22 s, API responsive throughout |
| Explorer resident memory | 18.93 MiB |
| Explorer image size | 15.8 MB (`FROM scratch`) |
| Database on volume | 81,205,704 bytes (74.4 MB db + 6.8 MB WAL) |
| Database compacted (`VACUUM INTO`) | 64,430,080 bytes |
| Indexed rows | 28,973 blocks · 28,978 transactions · 28,971 inputs · 57,946 outputs · 5 issuances · 5 assets · 28,980 address rows |
| Busiest address page (28,972 transactions) | 0.40 s summary, 0.075 s UTXO page |
| API latency during a full rebuild | 1–5 ms |

## Loose ends closed

### `tests/static-checks.sh` could PASS with `rg` missing

Previously the forbidden-pattern check ran `rg` directly inside `if`. With ripgrep
absent the command exited 127, the `if` took the else branch, and the script printed
PASS. Now the scanner is selected explicitly, grep is a supported fallback, "no
match" (status 1) is distinguished from a scanner error (status 2 or above), and a
self-test runs the active scanner against a dirty fixture and a clean fixture before
any real check.

All three paths were exercised:

| Path | Observed |
|---|---|
| grep fallback, clean tree | `PASS scanner self-test (grep)` and `PASS no obsolete options…` |
| grep fallback, real violation planted | `FAIL forbidden obsolete/exposure patterns` plus the offending file and line |
| neither scanner on `PATH` | `FAIL scanner: neither ripgrep nor grep is installed; pattern checks cannot run`, exit 1 |

### P2P published on `0.0.0.0`

Corrected to `127.0.0.1` by default with an explicit `localhost` / `lan` / `disabled`
setting. Verified end to end: after rebinding, Windows Elements-Qt reconnected
through WSL2 localhost forwarding and every node reported five peers (four internal
mesh links plus the external Qt node). The WSL-IP fallback is documented in the
README for hosts that cannot forward localhost, rather than defaulting back to
`0.0.0.0`.

### `.gitignore`

Reviewed before committing. `generated/` remains ignored, with explicit belt-and-
braces entries for `generated/secrets/`, `generated/backups/`, `*.rpc`, `wallet.dat`,
`*wallet*.backup`, `.cookie`, SQLite files, and the local Go caches. `git status` and
`git ls-files 'generated/**'` were reviewed before the commit; no generated secret or
wallet material is tracked. The commit is local only — remote `origin` exists but was
not pushed to.

## Not tested

- Peg-in and peg-out rendering: this chain sets `validatepegin=0` and has produced no
  peg traffic, so those code paths are exercised only by their absence.
- A live reorg on the production chain: deliberately not performed. Reorg behaviour is
  covered by the isolated fixture instead.
- Multi-architecture images: only `linux/amd64` is built.
