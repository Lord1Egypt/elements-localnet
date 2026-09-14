package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *store {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "explorer.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestParseSats(t *testing.T) {
	cases := map[string]int64{
		"50.00000000": 5000000000,
		"3.647e-05":   3647,
		"0":           0,
		"21000000.0":  2100000000000000,
		"0.00000001":  1,
	}
	for input, want := range cases {
		got, err := parseSats(json.Number(input))
		if err != nil {
			t.Fatalf("parseSats(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("parseSats(%q) = %d, want %d", input, got, want)
		}
	}
	if _, err := parseSats(json.Number("0.000000001")); err == nil {
		t.Error("expected sub-satoshi precision to be rejected")
	}
	if _, err := parseSats(json.Number("not-a-number")); err == nil {
		t.Error("expected a non-numeric amount to be rejected")
	}
}

func TestBlockSubsidyHalving(t *testing.T) {
	if got := blockSubsidy(5000000000, 210000, 1); got != 5000000000 {
		t.Errorf("first era subsidy = %d", got)
	}
	if got := blockSubsidy(5000000000, 210000, 210000); got != 2500000000 {
		t.Errorf("second era subsidy = %d", got)
	}
	if got := blockSubsidy(5000000000, 210000, 0); got != 0 {
		t.Errorf("genesis subsidy = %d, want 0", got)
	}
}

func TestApplyBlocksRecordsCursorAndAddresses(t *testing.T) {
	s := newTestStore(t)
	chain := newSyntheticChain("a")
	for i := 0; i < 3; i++ {
		chain.extend()
	}
	if err := s.applyBlocks(context.Background(), chain.blocks, testChainConfig()); err != nil {
		t.Fatalf("applyBlocks: %v", err)
	}
	cursor, err := s.cursor()
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cursor.Height != 2 || cursor.Hash != chain.tipHash() {
		t.Fatalf("cursor = %+v, want height 2 at %s", cursor, chain.tipHash())
	}
	summary, err := s.addressSummary(context.Background(), testAddress)
	if err != nil {
		t.Fatalf("addressSummary: %v", err)
	}
	if summary.TransactionCount != 3 || summary.OutputCount != 3 || summary.UnspentCount != 3 {
		t.Fatalf("address summary = %+v", summary)
	}
	if len(summary.ExplicitBalances) != 1 || summary.ExplicitBalances[0].BalanceSats != 15000000000 {
		t.Fatalf("explicit balances = %+v", summary.ExplicitBalances)
	}
}

func TestConfidentialIssuanceIsNotGivenAPublicSupply(t *testing.T) {
	s := newTestStore(t)
	chain := newSyntheticChain("a")
	chain.extend()
	chain.extend()
	issuance := issuanceTx(hashFor("issuance"), chain.coinbaseTxid(0), 0)
	chain.extend(issuance)
	if err := s.applyBlocks(context.Background(), chain.blocks, testChainConfig()); err != nil {
		t.Fatalf("applyBlocks: %v", err)
	}
	assetID := issuance.Vin[0].Issuance.Asset
	record, err := s.asset(context.Background(), assetID)
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	if record.IssuedSats != nil {
		t.Errorf("confidential issuance reported an explicit supply of %d", *record.IssuedSats)
	}
	if record.SupplyState != "not publicly verifiable" {
		t.Errorf("supply state = %q", record.SupplyState)
	}
	if !record.ConfidentialIssuance {
		t.Error("issuance should be flagged confidential")
	}
	if !record.DerivationVerified {
		t.Error("asset and token identifiers should verify against the entropy")
	}
}

func TestRollbackRestoresSpentOutputsAndRemovesAssets(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	chain := newSyntheticChain("a")
	chain.extend() // 0
	chain.extend() // 1
	chain.extend() // 2
	spend := spendTx(hashFor("spend"), chain.coinbaseTxid(1), 0)
	chain.extend(spend)                                                   // 3 spends the height-1 coinbase
	issuance := issuanceTx(hashFor("issuance"), chain.coinbaseTxid(2), 0) // 4 issues an asset
	chain.extend(issuance)
	if err := s.applyBlocks(ctx, chain.blocks, testChainConfig()); err != nil {
		t.Fatalf("applyBlocks: %v", err)
	}

	assetID := issuance.Vin[0].Issuance.Asset
	if _, err := s.asset(ctx, assetID); err != nil {
		t.Fatalf("asset should exist before rollback: %v", err)
	}

	for height := int64(4); height >= 3; height-- {
		if _, err := s.rollbackBlock(ctx, height); err != nil {
			t.Fatalf("rollbackBlock(%d): %v", height, err)
		}
	}

	cursor, err := s.cursor()
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cursor.Height != 2 || cursor.Hash != chain.blocks[2].Hash {
		t.Fatalf("cursor after rollback = %+v", cursor)
	}
	if _, err := s.asset(ctx, assetID); err != errNotFound {
		t.Errorf("asset survived rollback: %v", err)
	}
	if _, err := s.transactionDetail(ctx, spend.Txid, 2); err != errNotFound {
		t.Errorf("orphaned transaction survived rollback: %v", err)
	}

	var spentTxid *string
	if err := s.read.QueryRow(`SELECT spent_txid FROM outputs WHERE txid = ? AND vout = 0`, chain.coinbaseTxid(1)).Scan(&spentTxid); err != nil {
		t.Fatalf("read spent marker: %v", err)
	}
	if spentTxid != nil {
		t.Errorf("output is still marked spent by %s after rollback", *spentTxid)
	}
	summary, err := s.addressSummary(ctx, testAddress)
	if err != nil {
		t.Fatalf("addressSummary: %v", err)
	}
	if summary.UnspentCount != 3 || summary.OutputCount != 3 {
		t.Fatalf("address summary after rollback = %+v", summary)
	}
	var addressRows int
	if err := s.read.QueryRow(`SELECT COUNT(*) FROM address_txs WHERE address = ?`, testSpendAddr).Scan(&addressRows); err != nil {
		t.Fatalf("count address rows: %v", err)
	}
	if addressRows != 0 {
		t.Errorf("address history for the orphaned branch was not removed (%d rows)", addressRows)
	}
}

func TestSearchReturnsEveryMatchingIndex(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	chain := newSyntheticChain("a")
	chain.extend()
	chain.extend()
	issuance := issuanceTx(hashFor("issuance"), chain.coinbaseTxid(0), 0)
	chain.extend(issuance)
	if err := s.applyBlocks(ctx, chain.blocks, testChainConfig()); err != nil {
		t.Fatalf("applyBlocks: %v", err)
	}

	height := int64(1)
	hits, err := s.search(ctx, "1", &height)
	if err != nil {
		t.Fatalf("search height: %v", err)
	}
	if len(hits) != 1 || hits[0].Type != "block" {
		t.Fatalf("height search = %+v", hits)
	}
	if hits, err = s.search(ctx, chain.blocks[1].Hash, nil); err != nil || len(hits) != 1 || hits[0].Type != "block" {
		t.Fatalf("block hash search = %+v (%v)", hits, err)
	}
	if hits, err = s.search(ctx, issuance.Txid, nil); err != nil || len(hits) != 1 || hits[0].Type != "transaction" {
		t.Fatalf("txid search = %+v (%v)", hits, err)
	}
	assetID := issuance.Vin[0].Issuance.Asset
	if hits, err = s.search(ctx, assetID, nil); err != nil || len(hits) != 1 || hits[0].Type != "asset" {
		t.Fatalf("asset search = %+v (%v)", hits, err)
	}
	tokenID := issuance.Vin[0].Issuance.Token
	if hits, err = s.search(ctx, tokenID, nil); err != nil || len(hits) != 1 || hits[0].Value != assetID {
		t.Fatalf("reissuance token search = %+v (%v)", hits, err)
	}
	if hits, err = s.search(ctx, testAddress, nil); err != nil || len(hits) != 1 || hits[0].Type != "address" {
		t.Fatalf("address search = %+v (%v)", hits, err)
	}
	if hits, err = s.search(ctx, hashFor("absent"), nil); err != nil || len(hits) != 0 {
		t.Fatalf("unknown hash search = %+v (%v)", hits, err)
	}
}

func TestBlockListPagination(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	chain := newSyntheticChain("a")
	for i := 0; i < 12; i++ {
		chain.extend()
	}
	if err := s.applyBlocks(ctx, chain.blocks, testChainConfig()); err != nil {
		t.Fatalf("applyBlocks: %v", err)
	}
	blocks, page, err := s.blockList(ctx, 5, nil)
	if err != nil {
		t.Fatalf("blockList: %v", err)
	}
	if len(blocks) != 5 || page.Total != 12 || page.NextCursor == nil || *page.NextCursor != 6 {
		t.Fatalf("first page = %d blocks, page = %+v", len(blocks), page)
	}
	second, page, err := s.blockList(ctx, 5, page.NextCursor)
	if err != nil {
		t.Fatalf("blockList page 2: %v", err)
	}
	if len(second) != 5 || second[0].Height != 6 {
		t.Fatalf("second page starts at %d", second[0].Height)
	}
}

func TestExplicitFeesAreAttributedToTheBlock(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	chain := newSyntheticChain("a")
	chain.extend()
	chain.extend()
	chain.extend(spendTx(hashFor("spend"), chain.coinbaseTxid(0), 0))
	if err := s.applyBlocks(ctx, chain.blocks, testChainConfig()); err != nil {
		t.Fatalf("applyBlocks: %v", err)
	}
	detail, err := s.blockDetail(ctx, 2, 10, 0)
	if err != nil {
		t.Fatalf("blockDetail: %v", err)
	}
	if detail.FeesSats != 100000 {
		t.Errorf("block fees = %d, want 100000", detail.FeesSats)
	}
	if detail.SubsidySats != 5000000000 {
		t.Errorf("block subsidy = %d", detail.SubsidySats)
	}
	if detail.TxCount != 2 || len(detail.Transactions) != 2 {
		t.Errorf("transaction count = %d / %d", detail.TxCount, len(detail.Transactions))
	}
}
