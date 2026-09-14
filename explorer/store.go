package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

// store owns the rebuildable explorer index. The write handle is limited to one
// connection so indexing transactions never contend with each other, while a
// separate read pool keeps the browser responsive during a long catch-up.
type store struct {
	path  string
	write *sql.DB
	read  *sql.DB
}

const (
	metaSchemaVersion = "schema_version"
	metaIndexedHeight = "indexed_height"
	metaIndexedHash   = "indexed_hash"
	metaGenesisHash   = "genesis_hash"
	metaInitialSync   = "initial_sync_seconds"
	metaPolicyAsset   = "policy_asset"
)

func openStore(path string) (*store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("explorer database directory: %w", err)
	}
	writeDSN := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(15000)&_pragma=foreign_keys(0)"
	readDSN := writeDSN + "&_pragma=query_only(1)"
	write, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	if err := write.Ping(); err != nil {
		write.Close()
		return nil, err
	}
	read, err := sql.Open("sqlite", readDSN)
	if err != nil {
		write.Close()
		return nil, err
	}
	read.SetMaxOpenConns(4)
	read.SetMaxIdleConns(4)
	s := &store{path: path, write: write, read: read}
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) Close() {
	if s.read != nil {
		s.read.Close()
	}
	if s.write != nil {
		s.write.Close()
	}
}

func (s *store) migrate() error {
	var hasMeta int
	if err := s.write.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'`).Scan(&hasMeta); err != nil {
		return err
	}
	applied := 0
	if hasMeta == 1 {
		value, err := s.metaValue(metaSchemaVersion)
		if err != nil {
			return err
		}
		if value != "" {
			applied, err = strconv.Atoi(value)
			if err != nil {
				return errors.New("meta.schema_version is not an integer")
			}
		}
	}
	if applied > currentSchemaVersion {
		return fmt.Errorf("explorer database schema version %d is newer than this build supports (%d)", applied, currentSchemaVersion)
	}
	for version := applied; version < currentSchemaVersion; version++ {
		tx, err := s.write.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(schemaMigrations[version]); err != nil {
			tx.Rollback()
			return fmt.Errorf("schema migration %d: %w", version+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			metaSchemaVersion, strconv.Itoa(version+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) metaValue(key string) (string, error) {
	var value string
	err := s.read.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		// A brand new database may not have the read handle primed yet.
		err = s.write.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
	}
	return value, err
}

func setMeta(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

type indexCursor struct {
	Height int64
	Hash   string
}

func (s *store) cursor() (indexCursor, error) {
	height, err := s.metaValue(metaIndexedHeight)
	if err != nil {
		return indexCursor{Height: -1}, err
	}
	if height == "" {
		return indexCursor{Height: -1}, nil
	}
	parsed, err := strconv.ParseInt(height, 10, 64)
	if err != nil {
		return indexCursor{Height: -1}, errors.New("meta.indexed_height is not an integer")
	}
	hash, err := s.metaValue(metaIndexedHash)
	if err != nil {
		return indexCursor{Height: -1}, err
	}
	return indexCursor{Height: parsed, Hash: hash}, nil
}

func (s *store) sizeBytes() int64 {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(s.path + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}

// applyBlocks indexes a contiguous, already-validated batch in one transaction.
func (s *store) applyBlocks(ctx context.Context, blocks []rpcBlock, cfg chainConfig) error {
	if len(blocks) == 0 {
		return nil
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	touchedAssets := map[string]struct{}{}
	for _, block := range blocks {
		if err := insertBlock(tx, block, cfg, touchedAssets); err != nil {
			return err
		}
	}
	if err := refreshAssets(tx, touchedAssets); err != nil {
		return err
	}
	last := blocks[len(blocks)-1]
	if err := setMeta(tx, metaIndexedHeight, strconv.FormatInt(last.Height, 10)); err != nil {
		return err
	}
	if err := setMeta(tx, metaIndexedHash, last.Hash); err != nil {
		return err
	}
	if blocks[0].Height == 0 {
		if err := setMeta(tx, metaGenesisHash, blocks[0].Hash); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type chainConfig struct {
	SubsidySats     uint64
	HalvingInterval uint64
	PolicyAsset     string
}

func insertBlock(tx *sql.Tx, block rpcBlock, cfg chainConfig, touched map[string]struct{}) error {
	var fees int64
	subsidy := blockSubsidy(cfg.SubsidySats, cfg.HalvingInterval, block.Height)

	for position, transaction := range block.Tx {
		txFees, err := insertTransaction(tx, block, int64(position), transaction, cfg, touched)
		if err != nil {
			return err
		}
		fees += txFees
	}

	if _, err := tx.Exec(`INSERT INTO blocks(height, hash, prev_hash, merkle_root, block_time, median_time, version, size,
		stripped_size, weight, tx_count, subsidy_sats, fees_sats, signblock_challenge)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		block.Height, block.Hash, nullable(block.PreviousBlockHash), nullable(block.MerkleRoot), block.Time,
		block.MedianTime, block.Version, block.Size, block.StrippedSize, block.Weight,
		int64(len(block.Tx)), subsidy, fees, nullable(block.SignblockChalleng)); err != nil {
		return fmt.Errorf("insert block %d: %w", block.Height, err)
	}

	// Address history is derived from the rows just written, which keeps the
	// per-row Go code simple and the joins inside one transaction.
	if _, err := tx.Exec(`INSERT OR IGNORE INTO address_txs(address, txid, height, position)
		SELECT o.address, o.txid, o.height, t.position FROM outputs o
		JOIN transactions t ON t.txid = o.txid
		WHERE o.height = ? AND o.address IS NOT NULL`, block.Height); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO address_txs(address, txid, height, position)
		SELECT prev.address, i.txid, i.height, t.position FROM inputs i
		JOIN outputs prev ON prev.txid = i.prev_txid AND prev.vout = i.prev_vout
		JOIN transactions t ON t.txid = i.txid
		WHERE i.height = ? AND prev.address IS NOT NULL`, block.Height); err != nil {
		return err
	}
	return nil
}

func insertTransaction(tx *sql.Tx, block rpcBlock, position int64, t rpcTx, cfg chainConfig, touched map[string]struct{}) (int64, error) {
	isCoinbase := len(t.Vin) > 0 && t.Vin[0].Coinbase != ""
	hasIssuance, hasPegin, hasPegout := false, false, false
	for _, vin := range t.Vin {
		if vin.Issuance != nil {
			hasIssuance = true
		}
		if vin.IsPegin {
			hasPegin = true
		}
	}
	for _, vout := range t.Vout {
		if vout.ScriptPubKey.PegoutChain != "" {
			hasPegout = true
		}
	}

	fees := map[string]int64{}
	for asset, amount := range t.Fee {
		sats, err := parseSats(amount)
		if err != nil {
			return 0, fmt.Errorf("transaction %s fee: %w", t.Txid, err)
		}
		if sats != 0 {
			fees[asset] = sats
		}
	}
	var policyFee any
	if sats, ok := fees[cfg.PolicyAsset]; ok {
		policyFee = sats
	}
	var feesJSON any
	if len(fees) > 0 {
		encoded, err := json.Marshal(fees)
		if err != nil {
			return 0, err
		}
		feesJSON = string(encoded)
	}

	if _, err := tx.Exec(`INSERT INTO transactions(txid, wtxid, height, block_hash, position, block_time, size, vsize, weight,
		version, locktime, is_coinbase, has_issuance, has_pegin, has_pegout, fee_sats, fees_json)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.Txid, nullable(t.Wtxid), block.Height, block.Hash, position, block.Time, t.Size, t.Vsize, t.Weight,
		t.Version, t.Locktime, boolInt(isCoinbase), boolInt(hasIssuance), boolInt(hasPegin), boolInt(hasPegout),
		policyFee, feesJSON); err != nil {
		return 0, fmt.Errorf("insert transaction %s: %w", t.Txid, err)
	}

	for vinIndex, vin := range t.Vin {
		var prevTxid, prevVout any
		if vin.Coinbase == "" && vin.Txid != "" && vin.Vout != nil {
			prevTxid, prevVout = vin.Txid, *vin.Vout
		}
		var scriptHex any
		if vin.ScriptSig != nil && vin.ScriptSig.Hex != "" {
			scriptHex = vin.ScriptSig.Hex
		}
		if _, err := tx.Exec(`INSERT INTO inputs(txid, vin, prev_txid, prev_vout, is_coinbase, is_pegin, sequence, script_hex, height)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			t.Txid, vinIndex, prevTxid, prevVout, boolInt(vin.Coinbase != ""), boolInt(vin.IsPegin),
			vin.Sequence, scriptHex, block.Height); err != nil {
			return 0, fmt.Errorf("insert input %s:%d: %w", t.Txid, vinIndex, err)
		}
		if prevTxid != nil {
			if _, err := tx.Exec(`UPDATE outputs SET spent_txid = ?, spent_vin = ?, spent_height = ? WHERE txid = ? AND vout = ?`,
				t.Txid, vinIndex, block.Height, prevTxid, prevVout); err != nil {
				return 0, err
			}
		}
		if vin.Issuance != nil {
			if err := insertIssuance(tx, block, t, int64(vinIndex), vin, touched); err != nil {
				return 0, err
			}
		}
	}

	for _, vout := range t.Vout {
		var valueSats, asset, assetCommitment, valueCommitment any
		if vout.Value != nil {
			sats, err := parseSats(*vout.Value)
			if err != nil {
				return 0, fmt.Errorf("transaction %s output %d: %w", t.Txid, vout.N, err)
			}
			valueSats = sats
		}
		if vout.Asset != "" {
			asset = vout.Asset
		}
		if vout.AssetCommitment != "" {
			assetCommitment = vout.AssetCommitment
		}
		if vout.ValueCommitment != "" {
			valueCommitment = vout.ValueCommitment
		}
		if _, err := tx.Exec(`INSERT INTO outputs(txid, vout, asset, asset_commitment, value_sats, value_commitment,
			nonce_commitment, script_hex, script_asm, script_type, address, is_fee, pegout_chain, pegout_address, height)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			t.Txid, vout.N, asset, assetCommitment, valueSats, valueCommitment,
			nullable(vout.CommitmentNonce), nullable(vout.ScriptPubKey.Hex), nullable(vout.ScriptPubKey.Asm),
			nullable(vout.ScriptPubKey.Type), nullable(vout.ScriptPubKey.address()), boolInt(vout.isFee()),
			nullable(vout.ScriptPubKey.PegoutChain), nullable(vout.ScriptPubKey.PegoutAddress),
			block.Height); err != nil {
			return 0, fmt.Errorf("insert output %s:%d: %w", t.Txid, vout.N, err)
		}
	}

	if isCoinbase {
		return 0, nil
	}
	return fees[cfg.PolicyAsset], nil
}

func insertIssuance(tx *sql.Tx, block rpcBlock, t rpcTx, vinIndex int64, vin rpcVin, touched map[string]struct{}) error {
	issuance := vin.Issuance
	var assetSats, tokenSats any
	if issuance.AssetAmount != nil {
		sats, err := parseSats(*issuance.AssetAmount)
		if err != nil {
			return fmt.Errorf("issuance %s:%d asset amount: %w", t.Txid, vinIndex, err)
		}
		assetSats = sats
	} else if issuance.AssetAmountCommitment == "" {
		assetSats = int64(0)
	}
	if issuance.TokenAmount != nil {
		sats, err := parseSats(*issuance.TokenAmount)
		if err != nil {
			return fmt.Errorf("issuance %s:%d token amount: %w", t.Txid, vinIndex, err)
		}
		tokenSats = sats
	} else if issuance.TokenAmountCommitment == "" {
		tokenSats = int64(0)
	}
	var prevTxid, prevVout any
	if vin.Txid != "" && vin.Vout != nil {
		prevTxid, prevVout = vin.Txid, *vin.Vout
	}
	if _, err := tx.Exec(`INSERT INTO issuances(txid, vin, asset_id, token_id, entropy, blinding_nonce, is_reissuance,
		asset_amount_sats, asset_amount_commitment, token_amount_sats, token_amount_commitment,
		prev_txid, prev_vout, height, block_time)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.Txid, vinIndex, issuance.Asset, nullable(issuance.Token), nullable(issuance.AssetEntropy),
		nullable(issuance.AssetBlindingNonce), boolInt(issuance.IsReissuance),
		assetSats, nullable(issuance.AssetAmountCommitment), tokenSats, nullable(issuance.TokenAmountCommitment),
		prevTxid, prevVout, block.Height, block.Time); err != nil {
		return fmt.Errorf("insert issuance %s:%d: %w", t.Txid, vinIndex, err)
	}
	touched[issuance.Asset] = struct{}{}

	if issuance.IsReissuance {
		return nil
	}
	confidential := issuance.AssetAmountCommitment != ""
	verified := 0
	if issuance.AssetEntropy != "" {
		if derived, err := AssetIDFromEntropy(issuance.AssetEntropy); err == nil && derived == issuance.Asset {
			if token, err := ReissuanceTokenFromEntropy(issuance.AssetEntropy, confidential); err == nil && token == issuance.Token {
				verified = 1
			}
		}
	}
	_, err := tx.Exec(`INSERT INTO assets(asset_id, token_id, entropy, issuance_txid, issuance_vin, issuance_height,
		issuance_block_hash, issuance_time, confidential_issuance, derivation_verified)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(asset_id) DO NOTHING`,
		issuance.Asset, nullable(issuance.Token), nullable(issuance.AssetEntropy), t.Txid, vinIndex, block.Height,
		block.Hash, block.Time, boolInt(confidential), verified)
	return err
}

// refreshAssets recomputes every aggregate that is derived from the issuances
// table so apply and rollback share one definition of asset supply.
func refreshAssets(tx *sql.Tx, assetIDs map[string]struct{}) error {
	for assetID := range assetIDs {
		var issuanceCount, reissuanceCount, confidentialAsset, confidentialToken int64
		var assetSum, tokenSum sql.NullInt64
		err := tx.QueryRow(`SELECT
			COUNT(*),
			COALESCE(SUM(is_reissuance), 0),
			COALESCE(SUM(CASE WHEN asset_amount_sats IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN token_amount_sats IS NULL THEN 1 ELSE 0 END), 0),
			SUM(COALESCE(asset_amount_sats, 0)),
			SUM(COALESCE(token_amount_sats, 0))
			FROM issuances WHERE asset_id = ?`, assetID).
			Scan(&issuanceCount, &reissuanceCount, &confidentialAsset, &confidentialToken, &assetSum, &tokenSum)
		if err != nil {
			return err
		}
		if issuanceCount == 0 {
			continue
		}
		var issued, tokens any
		if confidentialAsset == 0 && assetSum.Valid {
			issued = assetSum.Int64
		}
		if confidentialToken == 0 && tokenSum.Valid {
			tokens = tokenSum.Int64
		}
		if _, err := tx.Exec(`UPDATE assets SET issued_sats = ?, token_sats = ?, issuance_count = ?,
			reissuance_count = ?, confidential_reissuance = ? WHERE asset_id = ?`,
			issued, tokens, issuanceCount, reissuanceCount, boolInt(confidentialAsset > 0 && reissuanceCount > 0), assetID); err != nil {
			return err
		}
	}
	return nil
}

// rollbackBlock removes one indexed block transactionally and restores the
// spent markers of the outputs its transactions consumed.
func (s *store) rollbackBlock(ctx context.Context, height int64) (indexCursor, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return indexCursor{Height: -1}, err
	}
	defer tx.Rollback()

	touched := map[string]struct{}{}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT asset_id FROM issuances WHERE height = ?`, height)
	if err != nil {
		return indexCursor{Height: -1}, err
	}
	for rows.Next() {
		var assetID string
		if err := rows.Scan(&assetID); err != nil {
			rows.Close()
			return indexCursor{Height: -1}, err
		}
		touched[assetID] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return indexCursor{Height: -1}, err
	}

	statements := []string{
		`UPDATE outputs SET spent_txid = NULL, spent_vin = NULL, spent_height = NULL WHERE spent_height = ?`,
		`DELETE FROM address_txs WHERE height = ?`,
		`DELETE FROM assets WHERE issuance_height = ?`,
		`DELETE FROM issuances WHERE height = ?`,
		`DELETE FROM outputs WHERE height = ?`,
		`DELETE FROM inputs WHERE height = ?`,
		`DELETE FROM transactions WHERE height = ?`,
		`DELETE FROM blocks WHERE height = ?`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, height); err != nil {
			return indexCursor{Height: -1}, err
		}
	}
	if err := refreshAssets(tx, touched); err != nil {
		return indexCursor{Height: -1}, err
	}

	cursor := indexCursor{Height: -1}
	err = tx.QueryRowContext(ctx, `SELECT height, hash FROM blocks ORDER BY height DESC LIMIT 1`).Scan(&cursor.Height, &cursor.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		cursor = indexCursor{Height: -1}
		if _, err := tx.ExecContext(ctx, `DELETE FROM meta WHERE key IN (?, ?)`, metaIndexedHeight, metaIndexedHash); err != nil {
			return indexCursor{Height: -1}, err
		}
	} else if err != nil {
		return indexCursor{Height: -1}, err
	} else {
		if err := setMeta(tx, metaIndexedHeight, strconv.FormatInt(cursor.Height, 10)); err != nil {
			return indexCursor{Height: -1}, err
		}
		if err := setMeta(tx, metaIndexedHash, cursor.Hash); err != nil {
			return indexCursor{Height: -1}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return indexCursor{Height: -1}, err
	}
	return cursor, nil
}

func (s *store) setMetaValue(key, value string) error {
	tx, err := s.write.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setMeta(tx, key, value); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *store) recordInitialSync(seconds float64) {
	tx, err := s.write.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	if err := setMeta(tx, metaInitialSync, strconv.FormatFloat(seconds, 'f', 3, 64)); err == nil {
		tx.Commit()
	}
}

func (s *store) replaceMempool(ctx context.Context, entries []mempoolRow) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM mempool`); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, entry := range entries {
		if _, err := tx.ExecContext(ctx, `INSERT INTO mempool(txid, size, vsize, weight, fee_sats, entry_time, first_seen, detail_json)
			VALUES(?,?,?,?,?,?,?,?)`,
			entry.Txid, entry.Size, entry.Vsize, entry.Weight, entry.FeeSats, entry.EntryTime, now, nullable(entry.DetailJSON)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type mempoolRow struct {
	Txid       string
	Size       any
	Vsize      any
	Weight     any
	FeeSats    any
	EntryTime  any
	DetailJSON string
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
