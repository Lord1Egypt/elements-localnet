# Explorer architecture

Phase 2A turns the Phase 1 `network-status` service into a single unified explorer
service. One Go binary, one origin, one image.

- Source: `explorer/`
- Image: `elements-localnet-explorer:phase2a`
- Origin: <http://127.0.0.1:8080>
- Index schema version: `1`

## Why one service

Everything the browser needs is on one origin, so the page needs no CORS, no proxy,
and no second container. The telemetry poller and the chain indexer are independent
goroutines inside the same process; a slow or failing indexer cannot stall the
telemetry poll, and vice versa.

```text
                   ┌──────────────────── explorer (Go) ────────────────────┐
  browser ──HTTP──▶│  net/http mux                                         │
   :8080           │    ├── /api/v1/network, /nodes, /topology, /producer,  │
                   │    │   /economics            ← telemetry poller        │
                   │    ├── /api/v1/blocks, /transactions, /addresses,      │
                   │    │   /assets, /mempool, /search  ← SQLite index      │
                   │    ├── /api/v1/.../raw       ← live RPC passthrough    │
                   │    └── /  embedded HTML, CSS, JS                       │
                   │                                                        │
                   │  telemetry poller ──▶ every node (monitor identity)    │
                   │  indexer          ──▶ node-02 first (explorer identity)│
                   │                       ↓                                │
                   │                   SQLite (WAL) in its own volume       │
                   └────────────────────────────────────────────────────────┘
```

## Data sources

| Question | Answered by |
|---|---|
| Is each node healthy, what is the canonical tip, who is peered | Telemetry poller, live RPC on every node |
| Block/transaction/address/asset history, search | The local SQLite index |
| Raw JSON for one block or transaction | A single live RPC call, on demand |
| Mempool contents | `getrawmempool true` on the indexing node, refreshed when the index is at the tip |

The Elements node remains the canonical source of chain data. The index exists only
to answer queries the node cannot answer cheaply (address history, asset history,
search). It is fully rebuildable and is never mixed into a node datadir.

## RPC identities and failover

Each Elements node carries two `rpcauth` identities, each constrained by
`rpcwhitelist` (with `rpcwhitelistdefault=0`):

- `monitor_node_NN` — the eight telemetry methods
- `explorer_node_NN` — `getblockchaininfo`, `getsidechaininfo`, `getblockhash`,
  `getblock`, `getblockheader`, `getrawtransaction`, `getrawmempool`,
  `getmempoolinfo`, `getmempoolentry`, `getchaintips`

Neither identity can reach a wallet method. The explorer has no generic RPC route,
so a browser cannot choose an RPC method.

`newRPCPool` ranks targets: the configured primary (`node-02`, which has `archive`
and `txindex`) first, then any other node advertising `txindex`, then the rest. Every
call walks that order and fails over on transport or protocol failure. Historical
records are read from one node, not polled across all five.

## Indexer

One bounded unit of work per step: a rollback, one batch of blocks, or a mempool
refresh. Each step is followed by a status update, so `/api/v1/explorer/status`
always reflects reality.

1. Learn the policy (fee/subsidy) asset once from `getsidechaininfo`, cache it in
   `meta.policy_asset`.
2. Read the chain tip with `getblockchaininfo`.
3. Read the cursor from `meta.indexed_height` / `meta.indexed_hash`. A fresh database
   starts at height `-1`, so indexing begins at genesis.
4. If the cursor equals the chain tip, refresh the mempool and idle for the poll
   interval.
5. Otherwise confirm continuity: `getblockhash(indexed_height)` must still return
   `indexed_hash`. If it does not, or the index is ahead of the node, roll back one
   block and start again.
6. Fetch the next batch (default 100 blocks) as one JSON-RPC batch of `getblockhash`
   followed by one batch of `getblock … 2`.
7. Verify every block's height and `previousblockhash` against the cursor and its
   predecessor in the batch. A mismatch at the first block triggers a rollback; a
   mismatch mid-batch truncates the batch and retries.
8. Apply the whole batch inside one SQLite transaction and advance the cursor in the
   same transaction.

Failures increment a bounded exponential backoff (1 s doubling to a 60 s ceiling) and
are recorded in `lastError`. The service does not exit or restart because it is
behind; being behind is a reported state, not a fault.

### Reorg handling

`rollbackBlock(height)` runs in one transaction:

1. Collect the asset IDs that had issuances at that height.
2. Clear `spent_txid` / `spent_vin` / `spent_height` on every output spent *by* that
   height, restoring those outputs to unspent.
3. Delete that height's `address_txs`, `assets`, `issuances`, `outputs`, `inputs`,
   `transactions`, `blocks` rows.
4. Recompute the aggregates of the collected assets from the surviving `issuances`
   rows, so supply figures never drift.
5. Move the cursor to the new highest indexed block, or clear it if the index is now
   empty.

The indexer then re-enters step 5 above and keeps unwinding until the cursor matches
the node's branch, then indexes the replacement branch forward.

This is covered by `TestIndexerDetectsReorgAndFollowsTheNewBranch`, which serves a
synthetic chain from an in-process fake node, swaps in a competing branch that forks
at height 2, and asserts the index unwinds and follows it. The live chain is never
used for reorg testing.

### Memory bounds

Batches are bounded by block count, every RPC response is read through an
`io.LimitReader`, results are decoded into fixed structs, and nothing accumulates
across steps. Measured resident set on a 29,000-block chain: ~19 MiB.

## Elements-specific handling

- **Explicit vs confidential values.** `vout.value` present means an explicit amount,
  stored as an exact integer satoshi count parsed with `math/big` (Elements prints
  amounts in scientific notation such as `3.647e-05`; float parsing is not used).
  Absent means blinded: `value_sats` is `NULL` and `value_commitment` is stored.
- **Explicit vs confidential assets.** `vout.asset` present means an explicit Asset
  ID. Absent means blinded: `asset` is `NULL` and `asset_commitment` is stored.
- **Fee outputs.** Elements fees are real outputs with `scriptPubKey.type == "fee"`.
  They are stored with `is_fee = 1`, and block fees are the sum of explicit policy
  asset fees across the non-coinbase transactions in the block.
- **Coinbase.** `vin[0].coinbase` marks the coinbase; its subsidy comes from the
  configured `con_blocksubsidy` shifted by the halving era, not from the output.
  The 100-block maturity rule is stated on the transaction page.
- **Issuance and reissuance.** Every `vin.issuance` is stored. A new issuance also
  creates an `assets` row; a reissuance only updates the aggregates.
- **Peg-in / peg-out.** `vin.is_pegin` and `scriptPubKey.pegout_*` are indexed and
  shown when present. This chain has `validatepegin=0` and no peg traffic so far.
- **Addresses.** Only the unconfidential address derived from the output script is
  stored, and it is labelled as a script address. The confidential address a sender
  used is not on chain and is never claimed.

### Asset ID derivation

`explorer/assetid.go` reimplements the upstream derivation so the identifiers
reported by the node can be checked rather than trusted blindly:

```text
entropy = FastMerkle( SHA256d(serialize(prevout)), contract_hash )
asset   = FastMerkle( entropy, 0x00…00 )
token   = FastMerkle( entropy, 0x01…00 or 0x02…00 if the issuance amount is blinded )
```

`FastMerkle(a, b)` is upstream's `ComputeFastMerkleRoot` for two leaves: one raw
SHA-256 compression of `a‖b` from the standard IV, with no length padding. It is not
`SHA256(a‖b)`.

Each indexed issuance recomputes the Asset ID and reissuance token from the entropy
the node reported and sets `assets.derivation_verified` only when both match.
`explorer/assetid_test.go` pins the algorithm against the five issuances that exist
on this chain — three confidential and two explicit — covering both token tags.

### Supply reporting

| Condition | `supplyState` |
|---|---|
| Every issuance for the asset has an explicit amount | `publicly verifiable`, with the summed satoshi total |
| Any issuance amount is blinded | `not publicly verifiable`, and `issuedSats` is `null` |

| Condition | `reissuanceState` |
|---|---|
| Reissuance token amount is blinded | `unknown (confidential reissuance token amount)` |
| Reissuance token amount is explicitly `0` | `fixed supply (no reissuance token was issued)` |
| Reissuance token amount is explicit and positive | `reissuable (a reissuance token exists)` |

A number a wallet owner reports is never promoted into either field.

## Browser application

`explorer/static/` is embedded with `go:embed`. It is plain HTML, CSS and JavaScript
with no build step, no framework, and no external request — the Content-Security-
Policy is `default-src 'self'`, and the only external string in the assets is the SVG
XML namespace.

- Client-side routing over the History API; the Go handler serves the shell for
  `/blocks`, `/transactions`, `/assets`, `/mempool`, `/nodes`, `/search`, `/block/…`,
  `/tx/…`, `/address/…` and `/asset/…` so deep links and refreshes work.
- A sticky indexing strip appears whenever the index is behind, is retrying, or has
  no RPC node, and disappears when synchronized.
- Loading, empty, not-found, error and offline states are explicit; a 404 while the
  index is still catching up says so.
- Every identifier is truncated with the full value in `title` and a copy button.
- Responsive from 400 px up; a keyboard-reachable skip link, visible focus rings,
  `aria-current` on the active nav item, and `aria-live` on the view region.
- Overview, Nodes and Mempool refresh every 10 s, and only while the tab is visible.
- No wallet control, no node control, no mutation of any kind.

## Container and security posture

| Property | Value |
|---|---|
| Build | Multi-stage; `go vet` and `go test` run inside the build |
| Final image | `FROM scratch` (pure-Go SQLite, `CGO_ENABLED=0`), ~15.8 MB |
| User | Non-root, UID/GID from `LOCAL_UID`/`LOCAL_GID` |
| Capabilities | `cap_drop: ALL` |
| Privilege escalation | `no-new-privileges:true` |
| Root filesystem | `read_only: true` |
| Writable paths | `/var/lib/elements-explorer` (named volume) and a 64 MB `tmpfs` at `/tmp` |
| Credentials | `generated/secrets/monitor` and `generated/secrets/explorer`, both `:ro` |
| Wallet | No wallet file mounted, no wallet RPC reachable |
| Docker socket | Not mounted |
| Published port | `127.0.0.1:8080` only |
| Healthcheck | `explorer -healthcheck` against `/healthz` |
| Shutdown | SIGTERM cancels the indexer context and drains HTTP with a 10 s deadline |

### Backups

`./manage.sh explorer backup` calls `explorer -backup <path>` inside the container,
which runs SQLite `VACUUM INTO`. That is consistent while the indexer keeps writing
in WAL mode, so no stop is required. The backup path is validated to be a
`backup-<stamp>.db` file directly inside the database directory, so the backup mode
can never read or delete anything else.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `INVENTORY_FILE` | `/run/localnet/inventory.json` | Node inventory and chain economics |
| `ASSET_REGISTRY_FILE` | `/run/localnet/assets.json` | Local asset metadata |
| `EXPLORER_DB_PATH` | `/var/lib/elements-explorer/explorer.db` | Index location |
| `EXPLORER_SECRETS_DIR` | `/run/explorer-secrets` | Per-node explorer RPC credentials |
| `EXPLORER_PRIMARY_NODE` | `node-02` | Preferred indexing source |
| `EXPLORER_BATCH_BLOCKS` | `100` | Blocks per batch and per transaction |
| `EXPLORER_POLL_SECONDS` | `5` | Idle poll interval once synchronized |
| `EXPLORER_RPC_TIMEOUT_SECONDS` | `60` | Deadline for one indexing step |
| `EXPLORER_LISTEN_ADDR` | `:8080` | In-container listen address |

## Known limitations

- Blinded amounts and blinded assets stay unknown. No range-proof or surjection-proof
  interpretation is attempted, and none is planned for a read-only explorer.
- `assets` covers assets created by an on-chain issuance. The policy asset has no
  issuance transaction, so it has no `assets` row; it is labelled as the policy asset
  wherever it appears.
- Address pages cover script addresses only; outputs whose script yields no address
  are counted but not attributed.
- Mempool entries carry the fields `getrawmempool true` returns. Full mempool
  transaction bodies are not indexed.
- Peg-in and peg-out fields are indexed and rendered but untested against real peg
  traffic, because this chain sets `validatepegin=0`.
- The index grows at roughly 2.2 KB per block on a one-transaction-per-block chain.
- Only `linux/amd64` is built.
