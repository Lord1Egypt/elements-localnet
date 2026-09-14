package main

// schemaMigrations are applied in order. Each entry is applied exactly once and
// recorded in meta.schema_version; never edit an applied migration in place.
var schemaMigrations = []string{
	`
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE blocks (
  height              INTEGER PRIMARY KEY,
  hash                TEXT NOT NULL UNIQUE,
  prev_hash           TEXT,
  merkle_root         TEXT,
  block_time          INTEGER NOT NULL,
  median_time         INTEGER,
  version             INTEGER,
  size                INTEGER,
  stripped_size       INTEGER,
  weight              INTEGER,
  tx_count            INTEGER NOT NULL,
  subsidy_sats        INTEGER NOT NULL,
  fees_sats           INTEGER NOT NULL,
  signblock_challenge TEXT
);
CREATE INDEX blocks_block_time ON blocks(block_time);

CREATE TABLE transactions (
  txid         TEXT PRIMARY KEY,
  wtxid        TEXT,
  height       INTEGER NOT NULL,
  block_hash   TEXT NOT NULL,
  position     INTEGER NOT NULL,
  block_time   INTEGER NOT NULL,
  size         INTEGER,
  vsize        INTEGER,
  weight       INTEGER,
  version      INTEGER,
  locktime     INTEGER,
  is_coinbase  INTEGER NOT NULL,
  has_issuance INTEGER NOT NULL,
  has_pegin    INTEGER NOT NULL,
  has_pegout   INTEGER NOT NULL,
  fee_sats     INTEGER,
  fees_json    TEXT
);
CREATE INDEX transactions_height ON transactions(height DESC, position);

CREATE TABLE inputs (
  txid        TEXT NOT NULL,
  vin         INTEGER NOT NULL,
  prev_txid   TEXT,
  prev_vout   INTEGER,
  is_coinbase INTEGER NOT NULL,
  is_pegin    INTEGER NOT NULL,
  sequence    INTEGER,
  script_hex  TEXT,
  height      INTEGER NOT NULL,
  PRIMARY KEY (txid, vin)
);
CREATE INDEX inputs_prevout ON inputs(prev_txid, prev_vout);
CREATE INDEX inputs_height ON inputs(height);

CREATE TABLE outputs (
  txid             TEXT NOT NULL,
  vout             INTEGER NOT NULL,
  asset            TEXT,
  asset_commitment TEXT,
  value_sats       INTEGER,
  value_commitment TEXT,
  nonce_commitment TEXT,
  script_hex       TEXT,
  script_asm       TEXT,
  script_type      TEXT,
  address          TEXT,
  is_fee           INTEGER NOT NULL,
  pegout_chain     TEXT,
  pegout_address   TEXT,
  height           INTEGER NOT NULL,
  spent_txid       TEXT,
  spent_vin        INTEGER,
  spent_height     INTEGER,
  PRIMARY KEY (txid, vout)
);
CREATE INDEX outputs_address ON outputs(address, height DESC) WHERE address IS NOT NULL;
CREATE INDEX outputs_asset ON outputs(asset, height DESC) WHERE asset IS NOT NULL;
CREATE INDEX outputs_height ON outputs(height);
CREATE INDEX outputs_spent_height ON outputs(spent_height) WHERE spent_height IS NOT NULL;

CREATE TABLE issuances (
  txid                    TEXT NOT NULL,
  vin                     INTEGER NOT NULL,
  asset_id                TEXT NOT NULL,
  token_id                TEXT,
  entropy                 TEXT,
  blinding_nonce          TEXT,
  is_reissuance           INTEGER NOT NULL,
  asset_amount_sats       INTEGER,
  asset_amount_commitment TEXT,
  token_amount_sats       INTEGER,
  token_amount_commitment TEXT,
  prev_txid               TEXT,
  prev_vout               INTEGER,
  height                  INTEGER NOT NULL,
  block_time              INTEGER NOT NULL,
  PRIMARY KEY (txid, vin)
);
CREATE INDEX issuances_asset ON issuances(asset_id, height);
CREATE INDEX issuances_height ON issuances(height);

CREATE TABLE assets (
  asset_id                TEXT PRIMARY KEY,
  token_id                TEXT,
  entropy                 TEXT,
  issuance_txid           TEXT NOT NULL,
  issuance_vin            INTEGER NOT NULL,
  issuance_height         INTEGER NOT NULL,
  issuance_block_hash     TEXT,
  issuance_time           INTEGER NOT NULL,
  confidential_issuance   INTEGER NOT NULL,
  issued_sats             INTEGER,
  token_sats              INTEGER,
  issuance_count          INTEGER NOT NULL DEFAULT 1,
  reissuance_count        INTEGER NOT NULL DEFAULT 0,
  confidential_reissuance INTEGER NOT NULL DEFAULT 0,
  derivation_verified     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX assets_issuance_height ON assets(issuance_height DESC);
CREATE INDEX assets_token ON assets(token_id) WHERE token_id IS NOT NULL;

CREATE TABLE address_txs (
  address  TEXT NOT NULL,
  txid     TEXT NOT NULL,
  height   INTEGER NOT NULL,
  position INTEGER NOT NULL,
  PRIMARY KEY (address, txid)
);
CREATE INDEX address_txs_height ON address_txs(address, height DESC, position DESC);
CREATE INDEX address_txs_rollback ON address_txs(height);

CREATE TABLE mempool (
  txid        TEXT PRIMARY KEY,
  size        INTEGER,
  vsize       INTEGER,
  weight      INTEGER,
  fee_sats    INTEGER,
  entry_time  INTEGER,
  first_seen  INTEGER NOT NULL,
  detail_json TEXT
);
`,
}

var currentSchemaVersion = len(schemaMigrations)
