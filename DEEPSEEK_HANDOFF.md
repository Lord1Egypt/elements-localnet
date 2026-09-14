# DeepSeek Flash handoff — Elements localnet Phase 1.1

## Purpose and architecture

`elements-localnet` is a Docker Compose generator for a private, OP_TRUE-signed
Elements development chain. It uses unmodified Elements, one walletless
scheduler, isolated node datadirs, and a read-only Go dashboard. It is not a
public network, explorer, controller, or wallet UI.

Pinned upstream: Elements Core `23.3.4`, tag `elements-23.3.4`, upstream short
commit `ca17280`. Official amd64 archive SHA-256:
`a758151ace3f21008ab162067ffce9e0e526a1b5d55995e2c30d9cd7ccda41a0`.

## Final topology and decisions

- `node-01`: role `producer`; sole target of the separate producer service.
- `node-02`: role `validator`, capabilities `archive` and `txindex`; unpruned.
- `node-03`, `node-04`, `node-05`: role `validator`.
- Each node has its own external named volume, config, admin RPC credential, and
  whitelist-restricted monitor credential. Validators disable wallet RPC.
- One producer sends exactly `generatetoaddress 1 <public-address>` every 1s.
  Manual bulk mining is separate and requires stopping the producer.
- A project volume `flock` plus fixed container name prevents duplicate loops.
- Producer signals stop sleeps/current curl and prevent subsequent requests.
  Repeated production failures update sanitized status and fail health checks.
- Dashboard polling remains concurrent/read-only, without Docker socket,
  wallet RPC, arbitrary RPC, database, or controller actions.
- Dashboard adds a 60-second block-rate window, integer halving-time estimate,
  producer timestamp/status, capabilities, and stopped-producer generation
  warnings. Browser RPC errors are codes such as `RPC_TIMEOUT`; URLs/secrets are
  not exposed.
- One second is local-development-only: 86,400 blocks/day, 4,320,000 subsidy
  units/day at 50 units/block, maturity around 100s, first halving around 58h20m.
- Destructive fork tests must use a separate disposable Compose project and
  volumes. Never invalidate the demonstration chain.

## Current runtime

Expected services: five healthy node containers, healthy `producer`, and
healthy `network-status`. Dashboard: <http://127.0.0.1:8080>. RPC ports
7041–7045 are IPv4-loopback only. The producer is enabled with effective
`BLOCK_INTERVAL=1`; node-01 has no loaded wallet and validators use
`disablewallet=1`. Stable test tip was height 215 with hash
`14c407a423b855fa41b4c3896e721d340026b20ad60032d53261b7365e1bbf86`;
height advances continuously.

Active volumes:

```text
elements-localnet-phase11-node-01-data
elements-localnet-phase11-node-02-data
elements-localnet-phase11-node-03-data
elements-localnet-phase11-node-04-data
elements-localnet-phase11-node-05-data
elements-localnet-phase11-producer-lock
elements-localnet-phase11-producer-status
```

Detached legacy invalidation-test volumes, deliberately not deleted:

```text
elements-localnet-node-01-data
elements-localnet-node-02-data
elements-localnet-node-03-data
elements-localnet-producer-lock
elements-localnet-producer-status
```

Their matching ignored files are at
`legacy/phase1-invalidated-20260914/generated/`. Only the owner should decide
whether to delete these later.

## Files changed in Phase 1.1

```text
.gitignore
README.md
DECISIONS.md
PROJECT_STATE.md
ROADMAP.md
ACCEPTANCE_TEST_REPORT.md
DEEPSEEK_HANDOFF.md
compose.yaml
setup-network.sh
manage.sh
scripts/generate-compose.sh
scripts/generate-inventory.sh
scripts/create-payout-wallet.sh
scripts/verify-network.sh
producer/Dockerfile
producer/block-producer.sh
producer/healthcheck.sh
network-status/main.go
network-status/main_test.go
network-status/static/app.js
tests/dashboard-check.sh
```

## Commands

```bash
cd /home/lordegypt/ChainProject/elements-localnet
./manage.sh status
./manage.sh height
./manage.sh peers
./manage.sh verify
./tests/dashboard-check.sh
./tests/static-checks.sh
(cd network-status && GOTOOLCHAIN=local GOCACHE=/tmp/elements-localnet-gocache go test ./...)
curl -fsS http://127.0.0.1:8080/healthz
./manage.sh producer interval 1
./manage.sh producer stop
./manage.sh producer start
./manage.sh stop
./manage.sh start
```

Never use `destroy` casually. Its explicit form is
`./manage.sh destroy --yes-i-understand`, and it targets only active Phase 1.1
volumes plus generated files; detached legacy volumes are intentionally outside
that command.

## Verification performed

All 22 Phase 1.1 bounded checks pass; see `ACCEPTANCE_TEST_REPORT.md`. Highlights:
five healthy nodes; same genesis; steady identical height/hash; peers 4,1,1,1,1;
one producer; walletless; six consecutive automatic blocks at about 1s; stop
held height 103; start/restart resumed and retained 1s; Compose restart
preserved data; dashboard roles/interval/hash passed; node logs contained no
invalid-chain warning; localhost/socket/secret/config/Go/Bash checks passed.
No long fork fixture was run. Shellcheck was unavailable.

## Known limitations / incomplete items

- linux/amd64 only.
- Optional failed-node JSON nullability and separately exposed last-known
  snapshot were deferred. Required sanitized structured errors are implemented.
- Detached legacy volumes can cause harmless Compose warnings because they keep
  old project labels; they remain intentionally unattached.
- A local Git repository was initialized after ignore verification. No remote
  was added; inspect `git status` and `git rev-parse HEAD` before editing.

## Recommended next task

If the user authorizes more Phase 1 hardening, create a small disposable fork
classification harness with a different Compose project name and disposable
volumes. Do **not** start Phase 2, an indexer, explorer, or control dashboard
until the user explicitly authorizes Phase 2.
