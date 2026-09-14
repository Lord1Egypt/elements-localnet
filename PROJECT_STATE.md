# Project state

- Working name: `elements-localnet`
- Current phase: Phase 1.1 hardening complete; Phase 2 not started
- Date: 2026-09-14 (Europe/Istanbul)
- Elements: official `elements-23.3.4`, upstream short commit `ca17280`
- Archive SHA-256: `a758151ace3f21008ab162067ffce9e0e526a1b5d55995e2c30d9cd7ccda41a0`
- Host: Windows 11 WSL2, x86_64; Docker 29.7.2; Compose 5.5.1
- Repository: local Git repository initialized; no remote configured. Use
  `git rev-parse HEAD` for the final local commit.

## Active runtime

- Five healthy nodes: node-01 producer role; node-02 through node-05 validator
  role. Node-02 capabilities are `archive` and `txindex`.
- One healthy producer service, one block per request, effective interval `1s`.
- Dashboard: <http://127.0.0.1:8080>, five-node dynamic inventory.
- Active volumes use prefix `elements-localnet-phase11-`.
- Wallet state: node-01 `listwallets == []`; node-02..05 use
  `disablewallet=1`.
- Stable comparison at height 215: every node hash
  `14c407a423b855fa41b4c3896e721d340026b20ad60032d53261b7365e1bbf86`.
  Height continues increasing while the producer runs.
- Active node logs contain no invalid-chain/database-corruption warning.

## Detached legacy runtime

Preserved and not deleted:

```text
elements-localnet-node-01-data
elements-localnet-node-02-data
elements-localnet-node-03-data
elements-localnet-producer-lock
elements-localnet-producer-status
```

Their generated configuration is preserved under the ignored path
`legacy/phase1-invalidated-20260914/generated/`. These volumes contain the
earlier intentional invalidation fixture. Remove them only after an explicit
owner decision.

## Test status

All 22 bounded Phase 1.1 checks passed. Six producer records showed heights
97–102 at one-to-two-second timestamp spacing. Stop polling held height 103
across multiple cycles; start advanced to 107; producer restart advanced to 111
with `BLOCK_INTERVAL=1`. Compose restart preserved genesis and data. Go tests,
Bash syntax/static checks, Compose config, verifier, dashboard checks, and
secret/socket/loopback scans passed. Shellcheck remains unavailable.

## Known limitations

- Only linux/amd64 is built.
- The optional failed-node null/last-known-snapshot API redesign was deferred;
  public failures are nevertheless sanitized structured codes with no RPC URL.
- Compose may mention detached legacy volumes sharing old Compose labels; they
  are intentionally retained and not attached to Phase 1.1 services.

## Resume / exact next step

Read `DEEPSEEK_HANDOFF.md`, then run:

```bash
cd /home/lordegypt/ChainProject/elements-localnet
./manage.sh status
./manage.sh verify
./tests/dashboard-check.sh
```

Do not start Phase 2 until explicitly authorized. The recommended next task is
a small isolated test harness for destructive fork tests using a different
Compose project and disposable volumes.
