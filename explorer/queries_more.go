package main

import (
	"context"
	"database/sql"
	"errors"
)

type AddressAssetBalance struct {
	AssetID      string `json:"assetId"`
	ReceivedSats int64  `json:"receivedSats"`
	SpentSats    int64  `json:"spentSats"`
	BalanceSats  int64  `json:"balanceSats"`
	OutputCount  int64  `json:"outputCount"`
	UnspentCount int64  `json:"unspentOutputs"`
}

type AddressSummary struct {
	Address             string                `json:"address"`
	AddressKind         string                `json:"addressKind"`
	TransactionCount    int64                 `json:"transactionCount"`
	OutputCount         int64                 `json:"outputCount"`
	UnspentCount        int64                 `json:"unspentOutputs"`
	ConfidentialOutputs int64                 `json:"confidentialOutputs"`
	ExplicitBalances    []AddressAssetBalance `json:"explicitBalances"`
}

type AddressUTXO struct {
	Txid            string  `json:"txid"`
	Vout            int64   `json:"vout"`
	Height          int64   `json:"height"`
	Asset           *string `json:"asset"`
	AssetCommitment *string `json:"assetCommitment"`
	ValueSats       *int64  `json:"valueSats"`
	ValueCommitment *string `json:"valueCommitment"`
	ScriptType      *string `json:"scriptType"`
	Confidential    bool    `json:"confidential"`
}

func (s *store) addressSummary(ctx context.Context, address string) (*AddressSummary, error) {
	summary := AddressSummary{
		Address: address,
		// The confidential address a sender used is not recorded on chain; only
		// the unconfidential script address can be derived from the output.
		AddressKind:      "script address derived from the output scriptPubKey",
		ExplicitBalances: []AddressAssetBalance{},
	}
	err := s.read.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM address_txs WHERE address = ?),
		(SELECT COUNT(*) FROM outputs WHERE address = ?),
		(SELECT COUNT(*) FROM outputs WHERE address = ? AND spent_txid IS NULL),
		(SELECT COUNT(*) FROM outputs WHERE address = ? AND value_sats IS NULL)`,
		address, address, address, address).
		Scan(&summary.TransactionCount, &summary.OutputCount, &summary.UnspentCount, &summary.ConfidentialOutputs)
	if err != nil {
		return nil, err
	}
	if summary.OutputCount == 0 && summary.TransactionCount == 0 {
		return nil, errNotFound
	}
	rows, err := s.read.QueryContext(ctx, `SELECT asset,
		COALESCE(SUM(value_sats), 0),
		COALESCE(SUM(CASE WHEN spent_txid IS NOT NULL THEN value_sats ELSE 0 END), 0),
		COUNT(*),
		COALESCE(SUM(CASE WHEN spent_txid IS NULL THEN 1 ELSE 0 END), 0)
		FROM outputs WHERE address = ? AND asset IS NOT NULL AND value_sats IS NOT NULL
		GROUP BY asset ORDER BY 2 DESC`, address)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b AddressAssetBalance
		if err := rows.Scan(&b.AssetID, &b.ReceivedSats, &b.SpentSats, &b.OutputCount, &b.UnspentCount); err != nil {
			return nil, err
		}
		b.BalanceSats = b.ReceivedSats - b.SpentSats
		summary.ExplicitBalances = append(summary.ExplicitBalances, b)
	}
	return &summary, rows.Err()
}

func (s *store) addressTransactions(ctx context.Context, address string, limit, offset int) ([]TxSummary, Page, error) {
	var total int64
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM address_txs WHERE address = ?`, address).Scan(&total); err != nil {
		return nil, Page{}, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT `+txSummaryColumns+` FROM address_txs a
		JOIN transactions t ON t.txid = a.txid
		WHERE a.address = ? ORDER BY a.height DESC, a.position DESC LIMIT ? OFFSET ?`, address, limit, offset)
	if err != nil {
		return nil, Page{}, err
	}
	defer rows.Close()
	out, err := scanTxSummaries(rows)
	if err != nil {
		return nil, Page{}, err
	}
	return out, makePage(limit, offset, total), nil
}

func (s *store) addressUTXOs(ctx context.Context, address string, limit, offset int) ([]AddressUTXO, Page, error) {
	var total int64
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM outputs WHERE address = ? AND spent_txid IS NULL`, address).Scan(&total); err != nil {
		return nil, Page{}, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT txid, vout, height, asset, asset_commitment, value_sats,
		value_commitment, script_type FROM outputs WHERE address = ? AND spent_txid IS NULL
		ORDER BY height DESC, txid, vout LIMIT ? OFFSET ?`, address, limit, offset)
	if err != nil {
		return nil, Page{}, err
	}
	defer rows.Close()
	out := []AddressUTXO{}
	for rows.Next() {
		var u AddressUTXO
		if err := rows.Scan(&u.Txid, &u.Vout, &u.Height, &u.Asset, &u.AssetCommitment, &u.ValueSats,
			&u.ValueCommitment, &u.ScriptType); err != nil {
			return nil, Page{}, err
		}
		u.Confidential = u.ValueSats == nil
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, Page{}, err
	}
	return out, makePage(limit, offset, total), nil
}

type AssetRecord struct {
	AssetID              string         `json:"assetId"`
	TokenID              *string        `json:"reissuanceTokenId"`
	Entropy              *string        `json:"entropy"`
	IssuanceTxid         string         `json:"issuanceTxid"`
	IssuanceVin          int64          `json:"issuanceVin"`
	IssuanceHeight       int64          `json:"issuanceHeight"`
	IssuanceBlockHash    *string        `json:"issuanceBlockHash"`
	IssuanceTime         int64          `json:"issuanceTime"`
	ConfidentialIssuance bool           `json:"confidentialIssuance"`
	IssuedSats           *int64         `json:"issuedSats"`
	ReissuanceTokenSats  *int64         `json:"reissuanceTokenSats"`
	IssuanceCount        int64          `json:"issuanceCount"`
	ReissuanceCount      int64          `json:"reissuanceCount"`
	ConfidentialReissue  bool           `json:"confidentialReissuance"`
	DerivationVerified   bool           `json:"derivationVerified"`
	SupplyState          string         `json:"supplyState"`
	ReissuanceState      string         `json:"reissuanceState"`
	OutputCount          int64          `json:"outputCount,omitempty"`
	Metadata             *AssetMetadata `json:"metadata,omitempty"`
}

const assetColumns = `asset_id, token_id, entropy, issuance_txid, issuance_vin, issuance_height,
	issuance_block_hash, issuance_time, confidential_issuance, issued_sats, token_sats,
	issuance_count, reissuance_count, confidential_reissuance, derivation_verified`

func scanAsset(scan func(dest ...any) error) (AssetRecord, error) {
	var a AssetRecord
	err := scan(&a.AssetID, &a.TokenID, &a.Entropy, &a.IssuanceTxid, &a.IssuanceVin, &a.IssuanceHeight,
		&a.IssuanceBlockHash, &a.IssuanceTime, &a.ConfidentialIssuance, &a.IssuedSats, &a.ReissuanceTokenSats,
		&a.IssuanceCount, &a.ReissuanceCount, &a.ConfidentialReissue, &a.DerivationVerified)
	if err != nil {
		return a, err
	}
	a.describe()
	return a, nil
}

// describe converts indexed facts into the wording the UI must use. An asset
// whose issuance amount is blinded is never reported with a public supply.
func (a *AssetRecord) describe() {
	switch {
	case a.IssuedSats != nil:
		a.SupplyState = "publicly verifiable"
	default:
		a.SupplyState = "not publicly verifiable"
	}
	switch {
	case a.ReissuanceTokenSats == nil:
		a.ReissuanceState = "unknown (confidential reissuance token amount)"
	case *a.ReissuanceTokenSats == 0:
		a.ReissuanceState = "fixed supply (no reissuance token was issued)"
	default:
		a.ReissuanceState = "reissuable (a reissuance token exists)"
	}
}

func (s *store) assetList(ctx context.Context, limit, offset int) ([]AssetRecord, Page, error) {
	var total int64
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM assets`).Scan(&total); err != nil {
		return nil, Page{}, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT `+assetColumns+` FROM assets
		ORDER BY issuance_height DESC, asset_id LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, Page{}, err
	}
	defer rows.Close()
	out := []AssetRecord{}
	for rows.Next() {
		record, err := scanAsset(rows.Scan)
		if err != nil {
			return nil, Page{}, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, Page{}, err
	}
	return out, makePage(limit, offset, total), nil
}

func (s *store) asset(ctx context.Context, assetID string) (*AssetRecord, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+assetColumns+` FROM assets WHERE asset_id = ?`, assetID)
	record, err := scanAsset(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM outputs WHERE asset = ?`, assetID).Scan(&record.OutputCount); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *store) assetIssuances(ctx context.Context, assetID string, limit, offset int) ([]IssuanceView, Page, error) {
	var total int64
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM issuances WHERE asset_id = ?`, assetID).Scan(&total); err != nil {
		return nil, Page{}, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT txid, vin, asset_id, token_id, entropy, is_reissuance,
		asset_amount_sats, asset_amount_commitment, token_amount_sats, token_amount_commitment, height, block_time
		FROM issuances WHERE asset_id = ? ORDER BY height DESC, txid LIMIT ? OFFSET ?`, assetID, limit, offset)
	if err != nil {
		return nil, Page{}, err
	}
	defer rows.Close()
	out, err := scanIssuances(rows)
	if err != nil {
		return nil, Page{}, err
	}
	return out, makePage(limit, offset, total), nil
}

func (s *store) assetTransactions(ctx context.Context, assetID string, limit, offset int) ([]TxSummary, Page, error) {
	var total int64
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(DISTINCT txid) FROM outputs WHERE asset = ?`, assetID).Scan(&total); err != nil {
		return nil, Page{}, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT `+txSummaryColumns+` FROM transactions t
		WHERE t.txid IN (SELECT DISTINCT txid FROM outputs WHERE asset = ?)
		ORDER BY t.height DESC, t.position DESC LIMIT ? OFFSET ?`, assetID, limit, offset)
	if err != nil {
		return nil, Page{}, err
	}
	defer rows.Close()
	out, err := scanTxSummaries(rows)
	if err != nil {
		return nil, Page{}, err
	}
	return out, makePage(limit, offset, total), nil
}

type MempoolEntry struct {
	Txid      string `json:"txid"`
	Vsize     *int64 `json:"vsize"`
	Weight    *int64 `json:"weight"`
	FeeSats   *int64 `json:"feeSats"`
	EntryTime *int64 `json:"entryTime"`
}

func (s *store) mempool(ctx context.Context, limit, offset int) ([]MempoolEntry, Page, error) {
	var total int64
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM mempool`).Scan(&total); err != nil {
		return nil, Page{}, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT txid, vsize, weight, fee_sats, entry_time FROM mempool
		ORDER BY entry_time DESC, txid LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, Page{}, err
	}
	defer rows.Close()
	out := []MempoolEntry{}
	for rows.Next() {
		var e MempoolEntry
		if err := rows.Scan(&e.Txid, &e.Vsize, &e.Weight, &e.FeeSats, &e.EntryTime); err != nil {
			return nil, Page{}, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, Page{}, err
	}
	return out, makePage(limit, offset, total), nil
}

type SearchHit struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

// search never guesses between entity types: a 64-character hex value is looked
// up in every index that can hold one and all matches are returned.
func (s *store) search(ctx context.Context, query string, height *int64) ([]SearchHit, error) {
	hits := []SearchHit{}
	if height != nil {
		var hash string
		err := s.read.QueryRowContext(ctx, `SELECT hash FROM blocks WHERE height = ?`, *height).Scan(&hash)
		if err == nil {
			hits = append(hits, SearchHit{Type: "block", Value: hash, Label: "Block at height " + formatInt(*height), Path: "/block/" + hash})
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if len(query) == 64 && isHex(query) {
		lookups := []struct {
			query string
			kind  string
			label string
			path  string
		}{
			{`SELECT hash FROM blocks WHERE hash = ?`, "block", "Block", "/block/"},
			{`SELECT txid FROM transactions WHERE txid = ?`, "transaction", "Transaction", "/tx/"},
			{`SELECT txid FROM mempool WHERE txid = ?`, "mempool", "Mempool transaction", "/tx/"},
			{`SELECT asset_id FROM assets WHERE asset_id = ?`, "asset", "Asset", "/asset/"},
			{`SELECT asset_id FROM assets WHERE token_id = ?`, "asset", "Reissuance token of asset", "/asset/"},
		}
		for _, lookup := range lookups {
			var value string
			err := s.read.QueryRowContext(ctx, lookup.query, query).Scan(&value)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			hits = append(hits, SearchHit{Type: lookup.kind, Value: value, Label: lookup.label, Path: lookup.path + value})
		}
		return hits, nil
	}
	var address string
	err := s.read.QueryRowContext(ctx, `SELECT address FROM outputs WHERE address = ? LIMIT 1`, query).Scan(&address)
	if err == nil {
		hits = append(hits, SearchHit{Type: "address", Value: address, Label: "Address", Path: "/address/" + address})
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return hits, nil
}

type ChainStats struct {
	IndexedBlocks     int64   `json:"indexedBlocks"`
	IndexedTxs        int64   `json:"indexedTransactions"`
	IndexedAssets     int64   `json:"indexedAssets"`
	AverageBlockGap   float64 `json:"averageBlockIntervalSeconds"`
	AverageTxPerBlock float64 `json:"averageTransactionsPerBlock"`
	SampleBlocks      int64   `json:"sampleBlocks"`
}

// chainStats samples the most recent window so the overview stays cheap on a
// long chain.
func (s *store) chainStats(ctx context.Context, window int) (ChainStats, error) {
	var stats ChainStats
	if err := s.read.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM blocks),
		(SELECT COUNT(*) FROM transactions),
		(SELECT COUNT(*) FROM assets)`).
		Scan(&stats.IndexedBlocks, &stats.IndexedTxs, &stats.IndexedAssets); err != nil {
		return stats, err
	}
	var first, last, count, txs sql.NullInt64
	err := s.read.QueryRowContext(ctx, `SELECT MIN(block_time), MAX(block_time), COUNT(*), COALESCE(SUM(tx_count), 0)
		FROM (SELECT block_time, tx_count FROM blocks ORDER BY height DESC LIMIT ?)`, window).
		Scan(&first, &last, &count, &txs)
	if err != nil {
		return stats, err
	}
	if count.Valid && count.Int64 > 1 && first.Valid && last.Valid {
		stats.SampleBlocks = count.Int64
		stats.AverageBlockGap = float64(last.Int64-first.Int64) / float64(count.Int64-1)
		stats.AverageTxPerBlock = float64(txs.Int64) / float64(count.Int64)
	}
	return stats, nil
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return len(s) > 0
}
