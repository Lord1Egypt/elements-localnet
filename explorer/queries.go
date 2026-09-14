package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

var errNotFound = errors.New("not found")

type Page struct {
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset,omitempty"`
	Total      int64  `json:"total"`
	NextOffset *int   `json:"nextOffset,omitempty"`
	NextCursor *int64 `json:"nextCursor,omitempty"`
}

type BlockSummary struct {
	Height      int64  `json:"height"`
	Hash        string `json:"hash"`
	Time        int64  `json:"time"`
	TxCount     int64  `json:"txCount"`
	Size        int64  `json:"size"`
	Weight      int64  `json:"weight"`
	SubsidySats int64  `json:"subsidySats"`
	FeesSats    int64  `json:"feesSats"`
}

type BlockDetail struct {
	BlockSummary
	PreviousBlockHash  *string     `json:"previousBlockHash"`
	NextBlockHash      *string     `json:"nextBlockHash"`
	MerkleRoot         *string     `json:"merkleRoot"`
	Version            int64       `json:"version"`
	StrippedSize       int64       `json:"strippedSize"`
	MedianTime         int64       `json:"medianTime"`
	SignblockChallenge *string     `json:"signblockChallenge"`
	Transactions       []TxSummary `json:"transactions"`
	TransactionPage    Page        `json:"transactionPage"`
}

type TxSummary struct {
	Txid        string `json:"txid"`
	Height      int64  `json:"height"`
	BlockHash   string `json:"blockHash"`
	BlockTime   int64  `json:"blockTime"`
	Size        int64  `json:"size"`
	Vsize       int64  `json:"vsize"`
	Weight      int64  `json:"weight"`
	IsCoinbase  bool   `json:"isCoinbase"`
	HasIssuance bool   `json:"hasIssuance"`
	HasPegin    bool   `json:"hasPegin"`
	HasPegout   bool   `json:"hasPegout"`
	FeeSats     *int64 `json:"feeSats"`
	InputCount  int64  `json:"inputCount"`
	OutputCount int64  `json:"outputCount"`
}

type TxDetail struct {
	TxSummary
	Wtxid         *string          `json:"wtxid"`
	Version       int64            `json:"version"`
	Locktime      int64            `json:"locktime"`
	Confirmations int64            `json:"confirmations"`
	Fees          map[string]int64 `json:"explicitFees"`
	Inputs        []TxInput        `json:"inputs"`
	Outputs       []TxOutput       `json:"outputs"`
	Issuances     []IssuanceView   `json:"issuances"`
}

type Prevout struct {
	Asset           *string `json:"asset"`
	AssetCommitment *string `json:"assetCommitment"`
	ValueSats       *int64  `json:"valueSats"`
	ValueCommitment *string `json:"valueCommitment"`
	ScriptAddress   *string `json:"scriptAddress"`
	ScriptType      *string `json:"scriptType"`
	Confidential    bool    `json:"confidential"`
}

type TxInput struct {
	Vin        int64         `json:"vin"`
	PrevTxid   *string       `json:"prevTxid"`
	PrevVout   *int64        `json:"prevVout"`
	IsCoinbase bool          `json:"isCoinbase"`
	IsPegin    bool          `json:"isPegin"`
	Sequence   int64         `json:"sequence"`
	Prevout    *Prevout      `json:"prevout"`
	Issuance   *IssuanceView `json:"issuance"`
}

type TxOutput struct {
	Vout            int64   `json:"vout"`
	Asset           *string `json:"asset"`
	AssetCommitment *string `json:"assetCommitment"`
	ValueSats       *int64  `json:"valueSats"`
	ValueCommitment *string `json:"valueCommitment"`
	NonceCommitment *string `json:"nonceCommitment"`
	ScriptHex       *string `json:"scriptHex"`
	ScriptAsm       *string `json:"scriptAsm"`
	ScriptType      *string `json:"scriptType"`
	ScriptAddress   *string `json:"scriptAddress"`
	IsFee           bool    `json:"isFee"`
	PegoutChain     *string `json:"pegoutChain"`
	PegoutAddress   *string `json:"pegoutAddress"`
	SpentTxid       *string `json:"spentTxid"`
	SpentVin        *int64  `json:"spentVin"`
	Confidential    bool    `json:"confidential"`
}

type IssuanceView struct {
	Txid                  string  `json:"txid"`
	Vin                   int64   `json:"vin"`
	AssetID               string  `json:"assetId"`
	TokenID               *string `json:"tokenId"`
	Entropy               *string `json:"entropy"`
	IsReissuance          bool    `json:"isReissuance"`
	AssetAmountSats       *int64  `json:"assetAmountSats"`
	AssetAmountCommitment *string `json:"assetAmountCommitment"`
	TokenAmountSats       *int64  `json:"tokenAmountSats"`
	TokenAmountCommitment *string `json:"tokenAmountCommitment"`
	Height                int64   `json:"height"`
	BlockTime             int64   `json:"blockTime"`
	Confidential          bool    `json:"confidential"`
}

func (s *store) blockList(ctx context.Context, limit int, beforeHeight *int64) ([]BlockSummary, Page, error) {
	var total int64
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocks`).Scan(&total); err != nil {
		return nil, Page{}, err
	}
	var rows *sql.Rows
	var err error
	if beforeHeight != nil {
		rows, err = s.read.QueryContext(ctx, `SELECT height, hash, block_time, tx_count, size, weight, subsidy_sats, fees_sats
			FROM blocks WHERE height <= ? ORDER BY height DESC LIMIT ?`, *beforeHeight, limit+1)
	} else {
		rows, err = s.read.QueryContext(ctx, `SELECT height, hash, block_time, tx_count, size, weight, subsidy_sats, fees_sats
			FROM blocks ORDER BY height DESC LIMIT ?`, limit+1)
	}
	if err != nil {
		return nil, Page{}, err
	}
	defer rows.Close()
	out := []BlockSummary{}
	for rows.Next() {
		var b BlockSummary
		if err := rows.Scan(&b.Height, &b.Hash, &b.Time, &b.TxCount, &b.Size, &b.Weight, &b.SubsidySats, &b.FeesSats); err != nil {
			return nil, Page{}, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, Page{}, err
	}
	page := Page{Limit: limit, Total: total}
	if len(out) > limit {
		next := out[limit].Height
		page.NextCursor = &next
		out = out[:limit]
	}
	return out, page, nil
}

func (s *store) blockDetail(ctx context.Context, height int64, txLimit, txOffset int) (*BlockDetail, error) {
	var d BlockDetail
	err := s.read.QueryRowContext(ctx, `SELECT b.height, b.hash, b.block_time, b.tx_count, b.size, b.weight, b.subsidy_sats,
		b.fees_sats, b.prev_hash, b.merkle_root, b.version, b.stripped_size, b.median_time, b.signblock_challenge,
		(SELECT hash FROM blocks WHERE height = b.height + 1)
		FROM blocks b WHERE b.height = ?`, height).
		Scan(&d.Height, &d.Hash, &d.Time, &d.TxCount, &d.Size, &d.Weight, &d.SubsidySats, &d.FeesSats,
			&d.PreviousBlockHash, &d.MerkleRoot, &d.Version, &d.StrippedSize, &d.MedianTime, &d.SignblockChallenge,
			&d.NextBlockHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	txs, page, err := s.transactionsInBlock(ctx, height, txLimit, txOffset)
	if err != nil {
		return nil, err
	}
	d.Transactions = txs
	d.TransactionPage = page
	return &d, nil
}

func (s *store) heightForHash(ctx context.Context, hash string) (int64, error) {
	var height int64
	err := s.read.QueryRowContext(ctx, `SELECT height FROM blocks WHERE hash = ?`, hash).Scan(&height)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errNotFound
	}
	return height, err
}

const txSummaryColumns = `t.txid, t.height, t.block_hash, t.block_time, t.size, t.vsize, t.weight,
	t.is_coinbase, t.has_issuance, t.has_pegin, t.has_pegout, t.fee_sats,
	(SELECT COUNT(*) FROM inputs i WHERE i.txid = t.txid),
	(SELECT COUNT(*) FROM outputs o WHERE o.txid = t.txid)`

func scanTxSummaries(rows *sql.Rows) ([]TxSummary, error) {
	out := []TxSummary{}
	for rows.Next() {
		var t TxSummary
		if err := rows.Scan(&t.Txid, &t.Height, &t.BlockHash, &t.BlockTime, &t.Size, &t.Vsize, &t.Weight,
			&t.IsCoinbase, &t.HasIssuance, &t.HasPegin, &t.HasPegout, &t.FeeSats, &t.InputCount, &t.OutputCount); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *store) transactionsInBlock(ctx context.Context, height int64, limit, offset int) ([]TxSummary, Page, error) {
	var total int64
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions WHERE height = ?`, height).Scan(&total); err != nil {
		return nil, Page{}, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT `+txSummaryColumns+` FROM transactions t
		WHERE t.height = ? ORDER BY t.position LIMIT ? OFFSET ?`, height, limit, offset)
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

func (s *store) recentTransactions(ctx context.Context, limit int) ([]TxSummary, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+txSummaryColumns+` FROM transactions t
		ORDER BY t.height DESC, t.position DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTxSummaries(rows)
}

func makePage(limit, offset int, total int64) Page {
	page := Page{Limit: limit, Offset: offset, Total: total}
	if int64(offset+limit) < total {
		next := offset + limit
		page.NextOffset = &next
	}
	return page
}

func (s *store) transactionDetail(ctx context.Context, txid string, chainHeight int64) (*TxDetail, error) {
	var d TxDetail
	var feesJSON *string
	err := s.read.QueryRowContext(ctx, `SELECT `+txSummaryColumns+`, t.wtxid, t.version, t.locktime, t.fees_json
		FROM transactions t WHERE t.txid = ?`, txid).
		Scan(&d.Txid, &d.Height, &d.BlockHash, &d.BlockTime, &d.Size, &d.Vsize, &d.Weight,
			&d.IsCoinbase, &d.HasIssuance, &d.HasPegin, &d.HasPegout, &d.FeeSats, &d.InputCount, &d.OutputCount,
			&d.Wtxid, &d.Version, &d.Locktime, &feesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	if chainHeight >= d.Height {
		d.Confirmations = chainHeight - d.Height + 1
	}
	d.Fees = map[string]int64{}
	if feesJSON != nil {
		_ = json.Unmarshal([]byte(*feesJSON), &d.Fees)
	}

	issuances, err := s.issuancesForTx(ctx, txid)
	if err != nil {
		return nil, err
	}
	byVin := map[int64]*IssuanceView{}
	for i := range issuances {
		byVin[issuances[i].Vin] = &issuances[i]
	}
	d.Issuances = issuances

	rows, err := s.read.QueryContext(ctx, `SELECT i.vin, i.prev_txid, i.prev_vout, i.is_coinbase, i.is_pegin, i.sequence,
		p.asset, p.asset_commitment, p.value_sats, p.value_commitment, p.address, p.script_type
		FROM inputs i LEFT JOIN outputs p ON p.txid = i.prev_txid AND p.vout = i.prev_vout
		WHERE i.txid = ? ORDER BY i.vin`, txid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d.Inputs = []TxInput{}
	for rows.Next() {
		var in TxInput
		var prev Prevout
		if err := rows.Scan(&in.Vin, &in.PrevTxid, &in.PrevVout, &in.IsCoinbase, &in.IsPegin, &in.Sequence,
			&prev.Asset, &prev.AssetCommitment, &prev.ValueSats, &prev.ValueCommitment, &prev.ScriptAddress, &prev.ScriptType); err != nil {
			return nil, err
		}
		if in.PrevTxid != nil && (prev.Asset != nil || prev.AssetCommitment != nil || prev.ScriptAddress != nil || prev.ValueSats != nil) {
			prev.Confidential = prev.ValueSats == nil
			copied := prev
			in.Prevout = &copied
		}
		in.Issuance = byVin[in.Vin]
		d.Inputs = append(d.Inputs, in)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	outRows, err := s.read.QueryContext(ctx, `SELECT vout, asset, asset_commitment, value_sats, value_commitment,
		nonce_commitment, script_hex, script_asm, script_type, address, is_fee, pegout_chain, pegout_address,
		spent_txid, spent_vin FROM outputs WHERE txid = ? ORDER BY vout`, txid)
	if err != nil {
		return nil, err
	}
	defer outRows.Close()
	d.Outputs = []TxOutput{}
	for outRows.Next() {
		var o TxOutput
		if err := outRows.Scan(&o.Vout, &o.Asset, &o.AssetCommitment, &o.ValueSats, &o.ValueCommitment,
			&o.NonceCommitment, &o.ScriptHex, &o.ScriptAsm, &o.ScriptType, &o.ScriptAddress, &o.IsFee,
			&o.PegoutChain, &o.PegoutAddress, &o.SpentTxid, &o.SpentVin); err != nil {
			return nil, err
		}
		o.Confidential = o.ValueSats == nil
		d.Outputs = append(d.Outputs, o)
	}
	return &d, outRows.Err()
}

func (s *store) issuancesForTx(ctx context.Context, txid string) ([]IssuanceView, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT txid, vin, asset_id, token_id, entropy, is_reissuance,
		asset_amount_sats, asset_amount_commitment, token_amount_sats, token_amount_commitment, height, block_time
		FROM issuances WHERE txid = ? ORDER BY vin`, txid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIssuances(rows)
}

func scanIssuances(rows *sql.Rows) ([]IssuanceView, error) {
	out := []IssuanceView{}
	for rows.Next() {
		var v IssuanceView
		if err := rows.Scan(&v.Txid, &v.Vin, &v.AssetID, &v.TokenID, &v.Entropy, &v.IsReissuance,
			&v.AssetAmountSats, &v.AssetAmountCommitment, &v.TokenAmountSats, &v.TokenAmountCommitment,
			&v.Height, &v.BlockTime); err != nil {
			return nil, err
		}
		v.Confidential = v.AssetAmountSats == nil
		out = append(out, v)
	}
	return out, rows.Err()
}
