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

## Phase 2A: unified explorer service

### One service, one origin

`network-status` was evolved in place into `explorer/` rather than adding a second
container. The telemetry poller and the chain indexer are independent goroutines in
one Go binary serving one origin, so the browser needs no CORS and no proxy, and a
stalled indexer cannot block telemetry. Every Phase 1 endpoint
(`/api/v1/network`, `/nodes`, `/nodes/{id}`, `/topology`, `/producer`, `/economics`,
`/healthz`) is preserved unchanged; `/readyz` and the `/api/v1/explorer/*`,
`/blocks`, `/transactions`, `/addresses`, `/assets`, `/mempool` and `/search`
endpoints were added alongside them.

### SQLite, pure Go, its own volume

The index is a single SQLite database in WAL mode via `modernc.org/sqlite`, which is
pure Go. That keeps `CGO_ENABLED=0`, which in turn allows a `FROM scratch` final
image of about 15.8 MB with no C runtime. A separate database container was rejected:
the working set is one writer and a handful of readers over tens of megabytes, which
is squarely inside SQLite's range, and a second container would add a credential, a
network path, and a failure mode for no benefit.

The database lives in its own named volume, `<namespace>-explorer-db`, and never
shares a directory with an Elements node datadir. It is rebuildable state: deleting
it costs an index rebuild, measured at roughly 22 seconds for 29,000 blocks, and
nothing else.

Two `database/sql` handles are opened over the same file — one writer connection and
four read-only connections. Under WAL this is what keeps the browser responsive while
a full rebuild is running; measured API latency during a rebuild stayed at 1–5 ms.

`modernc.org/sqlite` is not vendored. Reproducibility comes from `go.sum` plus the
digest-pinned builder image; vendoring it would add tens of megabytes of generated C
translation to the repository for no additional guarantee, since the image build
already requires network access for its base images.

### A third RPC identity, not a wider monitor whitelist

The monitor identity is deliberately restricted to eight telemetry methods, and the
admin identity can reach wallet RPC. Rather than widening one or reusing the other,
each node gained a third `rpcauth` identity, `explorer_node_NN`, whitelisted to ten
read-only chain methods. The explorer mounts monitor and explorer credentials
read-only and has no generic RPC route, so no browser input ever selects an RPC
method.

`scripts/ensure-explorer-credentials.sh` adds this identity to an already generated
network without regenerating any existing credential, and refuses to issue a new one
if a secret file exists whose config entry is missing. Nodes must be restarted to
read new `rpcauth` lines; a rolling restart preserves the chain because each node
keeps its own external volume.

### Indexing from one node, not all five

`node-02` carries `archive` and `txindex`, so it is the preferred indexing source.
The RPC pool ranks the configured primary first, then any other node advertising
`txindex`, then the rest, and fails over on transport or protocol failure. Historical
records are read from one node; the five-node fan-out remains telemetry-only.

The policy (fee and subsidy) asset is learned once from `getsidechaininfo` and cached
in `meta.policy_asset` rather than being configured by hand, so fee attribution
cannot drift from the chain.

### Confidentiality is a database-level guarantee, not a UI convention

A blinded amount is stored as `NULL`, never `0`, and every aggregate excludes `NULL`
rather than coercing it. `assets.issued_sats` is `NULL` whenever any issuance of that
asset is blinded, which is what makes "Supply: Not publicly verifiable" a property of
the data rather than a string the UI happens to print. A figure a wallet owner
reports is a claim and is never promoted into a supply field.

Only the unconfidential address derived from the output script is stored, and both
the API and the UI label it as a script address. The confidential address a sender
used is not recorded on chain, so the explorer never claims to show one.

### Asset identifiers are recomputed, not trusted

Elements reports `asset` and `token` per issuance. The explorer reimplements the
upstream derivation — `ComputeFastMerkleRoot`, which is one raw SHA-256 compression
of two 32-byte leaves without length padding, not `SHA256(a‖b)` — and sets
`assets.derivation_verified` only when both recomputed identifiers match. The
algorithm is pinned by unit tests against the five issuances on this chain, three
confidential and two explicit, which covers both reissuance-token tags. No unverified
custom derivation is used, and the node's values remain what is stored.

### Reorg handling is transactional and tested off the live chain

Rollback of one height clears the spent markers of outputs consumed at that height,
deletes that height's rows, and recomputes the affected assets' aggregates, all in
one transaction, then moves the cursor. Apply and rollback share one aggregate
definition, so supply figures cannot drift apart.

Reorg behaviour is covered by an in-process fake Elements node serving a synthetic
chain that forks at height 2. The live chain is never used for reorg testing, and the
20,000-block test was not repeated.

### P2P defaults to loopback

Phase 1.1 published P2P on `0.0.0.0:7142-7146`. That is now a deliberate opt-in:
`P2P_EXPOSURE` accepts `localhost` (the default, `127.0.0.1` only), `lan` (every host
interface, with a warning at setup), or `disabled` (no published P2P port at all).
Public RPC remains forbidden in every mode.

Loopback publishing was verified end to end: after rebinding, the Windows Elements-Qt
node reconnected to all five nodes through WSL2 localhost forwarding, and every node
reported five peers again. The documented fallback for hosts that cannot forward
localhost into WSL is the `wsl hostname -I` address in `addnode`, not a permanent
`0.0.0.0` bind.

### Guided setup that never blocks automation

`setup-network.sh` asks twelve questions with shown defaults and per-answer
validation when run interactively, then prints a summary and requires confirmation.
Any flag, or a non-terminal stdin, selects non-interactive mode, so CI cannot block
on a prompt. Host-port collisions are detected before anything is changed, and ports
already published by this project's own containers are not counted as collisions.

An existing payout address is validated through `validateaddress` and used as-is with
no wallet created. The wallet path is unchanged: create, back up, export only the
public address, unload — and an existing wallet backup is never overwritten.

### Static checks cannot pass by accident

`tests/static-checks.sh` previously ran `rg` inside an `if`, so a missing ripgrep
produced exit status 127, took the else branch, and printed PASS. The scanner is now
selected explicitly, grep is a first-class fallback, "no match" (status 1) is
distinguished from a scanner error (status 2 or above), and a self-test runs the
active scanner against both a dirty and a clean fixture before any real check. With
neither scanner installed the script fails instead of passing. All three paths were
exercised.
