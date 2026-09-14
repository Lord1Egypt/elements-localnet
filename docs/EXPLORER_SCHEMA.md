# Explorer index schema

- Engine: SQLite in WAL mode, pure-Go driver `modernc.org/sqlite` (no CGO)
- Location: `/var/lib/elements-explorer/explorer.db` inside the named volume
  `<VOLUME_NAMESPACE>-explorer-db`
- Current schema version: **1**
- Definition: `explorer/schema.go`

The index is **rebuildable state**. It never shares a directory with an Elements node
datadir and may be deleted at any time with
`./manage.sh explorer reindex --yes-i-understand`.

## Migrations

`schemaMigrations` is an ordered list of SQL scripts. On open, the applied count is
read from `meta.schema_version`; each pending migration runs inside its own
transaction together with the version bump, so a failure leaves the recorded version
untouched. A database whose version is *newer* than the binary supports is refused
rather than opened.

Applied migrations are never edited in place. A schema change adds a new entry.

## Connections

Two `database/sql` handles over the same file:

| Handle | Connections | Purpose |
|---|---|---|
| write | 1 | Indexing transactions; a single writer removes lock contention |
| read | 4, `query_only(1)` | Every API query |

WAL means the four readers are never blocked by a long indexing transaction, which is
what keeps the UI responsive during a full rebuild.

Pragmas: `journal_mode(WAL)`, `synchronous(NORMAL)`, `busy_timeout(15000)`,
`foreign_keys(0)`.

## Tables

### `meta`

| Column | Type | Notes |
|---|---|---|
| `key` | TEXT PK | |
| `value` | TEXT | |

| Key | Meaning |
|---|---|
| `schema_version` | Number of applied migrations |
| `indexed_height` | Cursor height; absent means the index is empty |
| `indexed_hash` | Block hash at the cursor height; checked for continuity every step |
| `genesis_hash` | Hash recorded when height 0 was indexed |
| `policy_asset` | Chain default (fee/subsidy) asset, from `getsidechaininfo` |
| `initial_sync_seconds` | Wall-clock duration of the first catch-up |

### `blocks`

`height` INTEGER PK, `hash` TEXT UNIQUE, `prev_hash`, `merkle_root`, `block_time`,
`median_time`, `version`, `size`, `stripped_size`, `weight`, `tx_count`,
`subsidy_sats`, `fees_sats`, `signblock_challenge`.

`subsidy_sats` is derived from the configured subsidy and halving interval, not from
the coinbase output. `fees_sats` is the sum of explicit policy-asset fee outputs
across the block's non-coinbase transactions.

Index: `blocks_block_time`.

### `transactions`

`txid` TEXT PK, `wtxid`, `height`, `block_hash`, `position`, `block_time`, `size`,
`vsize`, `weight`, `version`, `locktime`, `is_coinbase`, `has_issuance`, `has_pegin`,
`has_pegout`, `fee_sats`, `fees_json`.

`fee_sats` is the explicit policy-asset fee. `fees_json` is a JSON object of every
explicit fee keyed by Asset ID, for the rare multi-asset fee case.

Index: `transactions_height (height DESC, position)`.

### `inputs`

`(txid, vin)` PK, `prev_txid`, `prev_vout`, `is_coinbase`, `is_pegin`, `sequence`,
`script_hex`, `height`.

`prev_txid`/`prev_vout` are `NULL` for a coinbase input. `height` is denormalised
from the transaction so rollback is a single indexed delete.

Indexes: `inputs_prevout (prev_txid, prev_vout)`, `inputs_height`.

### `outputs`

`(txid, vout)` PK, `asset`, `asset_commitment`, `value_sats`, `value_commitment`,
`nonce_commitment`, `script_hex`, `script_asm`, `script_type`, `address`, `is_fee`,
`pegout_chain`, `pegout_address`, `height`, `spent_txid`, `spent_vin`, `spent_height`.

The confidentiality contract lives here:

| State | `asset` | `asset_commitment` | `value_sats` | `value_commitment` |
|---|---|---|---|---|
| Fully explicit | set | NULL | set | NULL |
| Blinded amount | set | NULL | **NULL** | set |
| Blinded asset and amount | **NULL** | set | **NULL** | set |

`NULL` means *not publicly knowable*, never zero. Aggregates deliberately exclude
`NULL` amounts rather than treating them as `0`.

`spent_height` is what makes rollback cheap: unwinding height *h* clears every
`spent_*` triple where `spent_height = h`.

Indexes: `outputs_address (address, height DESC)` partial on `address IS NOT NULL`,
`outputs_asset (asset, height DESC)` partial on `asset IS NOT NULL`, `outputs_height`,
`outputs_spent_height` partial on `spent_height IS NOT NULL`.

### `issuances`

`(txid, vin)` PK, `asset_id`, `token_id`, `entropy`, `blinding_nonce`,
`is_reissuance`, `asset_amount_sats`, `asset_amount_commitment`, `token_amount_sats`,
`token_amount_commitment`, `prev_txid`, `prev_vout`, `height`, `block_time`.

One row per issuance or reissuance input. A `NULL` amount with a commitment present
means blinded. An absent amount *and* absent commitment is recorded as an explicit
`0`, which is how Elements represents "no reissuance token was created".

Indexes: `issuances_asset (asset_id, height)`, `issuances_height`.

### `assets`

`asset_id` TEXT PK, `token_id`, `entropy`, `issuance_txid`, `issuance_vin`,
`issuance_height`, `issuance_block_hash`, `issuance_time`, `confidential_issuance`,
`issued_sats`, `token_sats`, `issuance_count`, `reissuance_count`,
`confidential_reissuance`, `derivation_verified`.

Created by the first non-reissuance issuance of an Asset ID. Every aggregate column
is recomputed from `issuances` by `refreshAssets`, which runs after both apply and
rollback, so the two paths share one definition of supply.

`issued_sats` is `NULL` whenever any issuance of that asset is blinded — that is the
database-level guarantee behind "Supply: Not publicly verifiable".
`derivation_verified` is `1` only when the locally recomputed Asset ID *and*
reissuance token both match what the node reported.

Indexes: `assets_issuance_height (issuance_height DESC)`, `assets_token` partial on
`token_id IS NOT NULL`.

### `address_txs`

`(address, txid)` PK, `height`, `position`.

Written by two set-based statements per block after the block's rows exist: one for
outputs paying an address, one joining inputs to the addresses of their prevouts.

Indexes: `address_txs_height (address, height DESC, position DESC)`,
`address_txs_rollback (height)`.

### `mempool`

`txid` TEXT PK, `size`, `vsize`, `weight`, `fee_sats`, `entry_time`, `first_seen`,
`detail_json`.

Replaced wholesale on each refresh; `entry_time` comes from the node and is what the
UI ages against.

## Query safety

Every statement is a compile-time constant string with bound parameters. No SQL is
built by concatenating user input anywhere in the service. Ordering and direction are
fixed in the query text; only `LIMIT`, `OFFSET` and cursor values come from the
request, each validated first (`limit` 1–100, `offset` 0–1,000,000, cursor a
non-negative integer).

## Sizing

Measured on this network at height 28,966 — 28,966 blocks, 28,971 transactions,
57,946 outputs, 5 assets:

| Segment | Size |
|---|---|
| `outputs` | 18.9 MiB |
| `blocks` | 6.9 MiB |
| `transactions` | 6.7 MiB |
| `outputs_asset` index | 4.3 MiB |
| `outputs` primary key | 4.2 MiB |
| `address_txs` + its indexes | 8.3 MiB |
| Everything else | ~11 MiB |
| **Compacted total (`VACUUM INTO`)** | **61 MiB** |
| Live file with WAL | ~75 MiB |

Roughly 2.2 KB per block at one transaction per block, dominated by 64-character
hexadecimal identifiers. Expect proportionally more on a busier chain.
