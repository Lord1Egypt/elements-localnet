# Architecture decisions

## Phase 1 boundary

Phase 1 includes the private Elements network generator and a monitoring-only
status page. It excludes explorer/indexer functionality and all browser control,
wallet, issuance, terminal, and arbitrary-RPC features.

## Upstream binary and chain configuration

Elements Core `23.3.4` is used without patches. The official x86_64 release
archive is checksum-pinned. The Debian build/runtime base and Go builder base
are also digest-pinned. Live `elementsd -help` and `-help-debug` checks confirmed
the requested options; `con_nsubsidyhalvinginterval` and
`anyonecanspendaremine` are accepted debug/custom-chain settings even though
only the latter is printed by current debug help. Obsolete
`verification.threads` and `con_dyna_deploy_start` are not used.

All consensus settings are generated identically under the `[elements]`
network section. `chain=elements`, disabled DNS discovery, and explicit
`addnode` links ensure this chain is not Liquid. The default policy asset is
also Elements' default subsidy asset, so no redundant explicit subsidy asset is
set.

## Roles and wallet isolation

One named volume belongs to exactly one node. Node-01 is the sole producer RPC
target. It remains wallet-capable only so the backed-up `payout` wallet can be
loaded deliberately; normal service has an empty `listwallets`. All other nodes
use `disablewallet=1`. A one-node topology necessarily combines producer and
temporary archive/RPC responsibility and is documented as reduced redundancy.

The safest upstream-compatible bootstrap is a temporary descriptor wallet on
node-01 with `load_on_start=false`. `backupwallet` writes a short-lived copy to
node-01's data volume because the container root filesystem is read-only; setup
copies it to the ignored host secrets directory, deletes the temporary copy,
and unloads the wallet. Mining thereafter uses only the public address. This
avoids source modification and a permanently loaded large mining wallet.

## Block scheduling and singleton behavior

`generatetoaddress` creates blocks; it does not enforce time. The producer owns
the wall-clock schedule, bounded retry/backoff, and clean signal handling. A
lock in a shared named volume plus a fixed Compose container name prevents
duplicate producer instances. A separate shared volume contains sanitized JSON
status. The dashboard mounts it read-only.

Phase 1.1 sets the supported default to one block per second. Each scheduled
iteration sends exactly `generatetoaddress 1 <address>`; bulk fast-forwarding
remains a separate stopped-producer management action. Producer health uses
the sanitized failure counter/status, not process existence alone.

## Phase 1.1 topology and clean-volume migration

The final topology is one producer-role node plus four nodes whose primary role
is `validator`. Node-02 retains archive/txindex behavior as capabilities. The
invalidation-test volumes were detached without deletion. The live chain uses
the `elements-localnet-phase11-*` namespace. Future invalidation tests must use
a different disposable Compose project and separate volumes.

Named volumes are declared external in rendered Compose and created with exact
project-scoped names before startup. Destructive removal remains an explicit,
confirmed `manage.sh destroy` operation.

## Networking

The project uses a dedicated bridge with predictable addresses. It is not
marked Docker `internal` because Docker Engine 29/Desktop accepted but did not
activate host-published ports on an internal bridge during testing. Privacy is
instead enforced by publishing only RPC/dashboard ports on `127.0.0.1`, never
publishing P2P, disabling DNS discovery, and adding only local peers. No host
networking or Docker socket is used.

RPC listens on the private container interface so peer services can reach it;
Docker's host mapping remains loopback-only. Admin and monitor identities are
separate. Elements 23.3.4 live help confirmed `rpcauth`, `rpcwhitelist`, and
`rpcwhitelistdefault`. Monitor accounts are restricted to the eight methods the
Go service calls; the application itself has no generic RPC route.

## Status aggregation

The Go backend uses one batch request plus `getblockheader` per node, concurrent
goroutines, a two-second per-node deadline, response-size limits, and in-memory
snapshots. One timeout cannot serialize or block other polls. Canonical chain
selection is the modal `(height, best block hash)` pair with deterministic tie
handling. Classification priority is offline/error, fork evidence, equal-height
hash divergence, height lag, stale tip, then healthy.

Aggregate disk size is labeled as replicated bytes; it is useful for resource
planning but is not unique blockchain data. Reward arithmetic uses unsigned
integer satoshis and bit shifts, never floating point. Producer liveness comes
from its sanitized status heartbeat because the dashboard is intentionally
denied Docker access.

The backend keeps a bounded 60-second in-memory height history for block rate,
estimates time to halving from integer block counts and configured seconds, and
flags height growth while the producer reports stopped. Browser-facing RPC
failures use structured codes and never include internal RPC URLs.

## Future wallet scalability (required)

Phase 4 must implement bounded reward epochs, consolidation of mature coinbase
UTXOs, archival/unloading of old wallets, a separate optional faucet, and
PSET/raw or external signing. Wallet birthdays/checkpoints and descriptor
imports must prevent multi-million-block full rescans. A monolithic always-open
reward wallet is explicitly rejected.
