package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"
)

// IndexerStatus is the browser-facing view of indexing progress. Every field is
// derived from local state; it never contains RPC URLs or credentials.
type IndexerStatus struct {
	SchemaVersion      int        `json:"schemaVersion"`
	PolicyAsset        string     `json:"policyAsset,omitempty"`
	PrimaryNode        string     `json:"primaryRpcNode"`
	IndexedHeight      int64      `json:"indexedHeight"`
	IndexedBlockHash   string     `json:"indexedBlockHash,omitempty"`
	ChainHeight        int64      `json:"chainHeight"`
	ChainBestBlockHash string     `json:"chainBestBlockHash,omitempty"`
	Lag                int64      `json:"lagBlocks"`
	PercentComplete    float64    `json:"percentComplete"`
	Synchronized       bool       `json:"synchronized"`
	Indexing           bool       `json:"indexing"`
	ReorgsHandled      int64      `json:"reorgsHandled"`
	BlocksIndexed      int64      `json:"blocksIndexedThisRun"`
	LastError          string     `json:"lastError,omitempty"`
	LastErrorAt        *time.Time `json:"lastErrorAt,omitempty"`
	StartedAt          time.Time  `json:"startedAt"`
	InitialSyncSeconds float64    `json:"initialSyncSeconds,omitempty"`
	DatabaseBytes      int64      `json:"databaseBytes"`
	MempoolIndexed     int64      `json:"mempoolTransactionsIndexed"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type indexer struct {
	store        *store
	pool         *rpcPool
	cfg          chainConfig
	batchSize    int
	pollInterval time.Duration
	rpcTimeout   time.Duration

	mu     sync.RWMutex
	status IndexerStatus
}

func newIndexer(s *store, pool *rpcPool, cfg chainConfig, primary string, batchSize int, pollInterval, rpcTimeout time.Duration) *indexer {
	cursor, _ := s.cursor()
	if cfg.PolicyAsset == "" {
		if value, err := s.metaValue(metaPolicyAsset); err == nil {
			cfg.PolicyAsset = value
		}
	}
	initial := 0.0
	if value, err := s.metaValue(metaInitialSync); err == nil && value != "" {
		initial, _ = strconv.ParseFloat(value, 64)
	}
	return &indexer{
		store:        s,
		pool:         pool,
		cfg:          cfg,
		batchSize:    batchSize,
		pollInterval: pollInterval,
		rpcTimeout:   rpcTimeout,
		status: IndexerStatus{
			SchemaVersion:      currentSchemaVersion,
			PolicyAsset:        cfg.PolicyAsset,
			PrimaryNode:        primary,
			IndexedHeight:      cursor.Height,
			IndexedBlockHash:   cursor.Hash,
			ChainHeight:        -1,
			StartedAt:          time.Now().UTC(),
			InitialSyncSeconds: initial,
			DatabaseBytes:      s.sizeBytes(),
			UpdatedAt:          time.Now().UTC(),
		},
	}
}

func (ix *indexer) Status() IndexerStatus {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.status
}

func (ix *indexer) update(mutate func(*IndexerStatus)) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	mutate(&ix.status)
	s := &ix.status
	if s.ChainHeight >= 0 {
		s.Lag = s.ChainHeight - s.IndexedHeight
		if s.Lag < 0 {
			s.Lag = 0
		}
		total := s.ChainHeight + 1
		done := s.IndexedHeight + 1
		if done < 0 {
			done = 0
		}
		if total > 0 {
			s.PercentComplete = float64(done) / float64(total) * 100
		}
		s.Synchronized = s.IndexedHeight == s.ChainHeight && s.ChainBestBlockHash == s.IndexedBlockHash
		s.Indexing = !s.Synchronized
	}
	s.UpdatedAt = time.Now().UTC()
}

func (ix *indexer) Run(ctx context.Context) {
	runStart := time.Now()
	caughtUp := false
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		progressed, err := ix.step(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			attempt++
			message := sanitizeIndexerError(err)
			at := time.Now().UTC()
			ix.update(func(s *IndexerStatus) {
				s.LastError = message
				s.LastErrorAt = &at
			})
			log.Printf("indexer: %s (retry %d)", message, attempt)
			if !sleepContext(ctx, backoff(attempt-1, time.Second, 60*time.Second)) {
				return
			}
			continue
		}
		attempt = 0
		ix.update(func(s *IndexerStatus) {
			s.LastError = ""
			s.LastErrorAt = nil
			s.DatabaseBytes = ix.store.sizeBytes()
		})
		if !caughtUp && ix.Status().Synchronized {
			caughtUp = true
			elapsed := time.Since(runStart).Seconds()
			if ix.Status().InitialSyncSeconds == 0 {
				ix.store.recordInitialSync(elapsed)
				ix.update(func(s *IndexerStatus) { s.InitialSyncSeconds = elapsed })
			}
			log.Printf("indexer: caught up with the chain tip in %.1fs", elapsed)
		}
		if !progressed {
			if !sleepContext(ctx, ix.pollInterval) {
				return
			}
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// step performs at most one bounded unit of work: a rollback, one batch of
// blocks, or a mempool refresh. It returns true when the index moved.
func (ix *indexer) step(ctx context.Context) (bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, ix.rpcTimeout)
	defer cancel()

	if ix.cfg.PolicyAsset == "" {
		if err := ix.resolvePolicyAsset(callCtx); err != nil {
			return false, err
		}
	}

	info, err := ix.pool.chainInfo(callCtx)
	if err != nil {
		return false, err
	}
	ix.update(func(s *IndexerStatus) {
		s.ChainHeight = info.Blocks
		s.ChainBestBlockHash = info.BestBlockHash
	})

	cursor, err := ix.store.cursor()
	if err != nil {
		return false, storeFailure("cursor read", err)
	}

	if cursor.Height == info.Blocks && cursor.Hash == info.BestBlockHash {
		if err := ix.refreshMempool(callCtx); err != nil {
			return false, err
		}
		return false, nil
	}

	if cursor.Height >= 0 {
		canonical, err := ix.pool.blockHash(callCtx, min64(cursor.Height, info.Blocks))
		if err != nil {
			return false, err
		}
		if cursor.Height > info.Blocks || canonical != cursor.Hash {
			return ix.rollbackOne(ctx, cursor.Height)
		}
	}

	from := cursor.Height + 1
	to := from + int64(ix.batchSize) - 1
	if to > info.Blocks {
		to = info.Blocks
	}
	if to < from {
		return false, nil
	}

	requests := make([]rpcRequest, 0, to-from+1)
	for height := from; height <= to; height++ {
		requests = append(requests, rpcRequest{Method: "getblockhash", Params: []any{height}})
	}
	results, err := ix.pool.call(callCtx, requests...)
	if err != nil {
		return false, err
	}
	hashes := make([]string, len(results))
	for i, raw := range results {
		if err := json.Unmarshal(raw, &hashes[i]); err != nil || len(hashes[i]) != 64 {
			return false, errors.New("invalid getblockhash response")
		}
	}

	blocks, err := ix.pool.blocks(callCtx, hashes)
	if err != nil {
		return false, err
	}

	previous := cursor.Hash
	for i := range blocks {
		expectedHeight := from + int64(i)
		if blocks[i].Height != expectedHeight {
			return false, errors.New("node returned a block at an unexpected height")
		}
		if expectedHeight > 0 && previous != "" && blocks[i].PreviousBlockHash != previous {
			// The branch changed underneath us; unwind the tip and retry.
			if i == 0 {
				return ix.rollbackOne(ctx, cursor.Height)
			}
			blocks = blocks[:i]
			break
		}
		previous = blocks[i].Hash
	}
	if len(blocks) == 0 {
		return false, nil
	}

	if err := ix.store.applyBlocks(ctx, blocks, ix.cfg); err != nil {
		return false, storeFailure("block write", err)
	}
	last := blocks[len(blocks)-1]
	indexed := int64(len(blocks))
	ix.update(func(s *IndexerStatus) {
		s.IndexedHeight = last.Height
		s.IndexedBlockHash = last.Hash
		s.BlocksIndexed += indexed
	})
	return true, nil
}

// resolvePolicyAsset learns the chain's default (fee/subsidy) asset from the
// node once and caches it, so explicit fee outputs can be attributed.
func (ix *indexer) resolvePolicyAsset(ctx context.Context) error {
	raw, err := ix.pool.callOne(ctx, "getsidechaininfo")
	if err != nil {
		return err
	}
	var info struct {
		PeggedAsset string `json:"pegged_asset"`
	}
	if err := json.Unmarshal(raw, &info); err != nil || len(info.PeggedAsset) != 64 {
		return errors.New("invalid getsidechaininfo response")
	}
	ix.cfg.PolicyAsset = info.PeggedAsset
	ix.update(func(s *IndexerStatus) { s.PolicyAsset = info.PeggedAsset })
	if err := ix.store.setMetaValue(metaPolicyAsset, info.PeggedAsset); err != nil {
		return storeFailure("policy asset write", err)
	}
	return nil
}

func (ix *indexer) rollbackOne(ctx context.Context, height int64) (bool, error) {
	cursor, err := ix.store.rollbackBlock(ctx, height)
	if err != nil {
		return false, storeFailure("rollback", err)
	}
	ix.update(func(s *IndexerStatus) {
		s.IndexedHeight = cursor.Height
		s.IndexedBlockHash = cursor.Hash
		s.ReorgsHandled++
	})
	log.Printf("indexer: rolled back orphaned block at height %d", height)
	return true, nil
}

func (ix *indexer) refreshMempool(ctx context.Context) error {
	raw, err := ix.pool.callOne(ctx, "getrawmempool", true)
	if err != nil {
		return err
	}
	var entries map[string]rpcMempoolEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return errors.New("invalid getrawmempool response")
	}
	rows := make([]mempoolRow, 0, len(entries))
	for txid, entry := range entries {
		row := mempoolRow{Txid: txid, Vsize: entry.Vsize, Weight: entry.Weight, EntryTime: entry.Time}
		if base, ok := entry.Fees["base"]; ok {
			if sats, err := parseSats(base); err == nil {
				row.FeeSats = sats
			}
		}
		rows = append(rows, row)
	}
	if err := ix.store.replaceMempool(ctx, rows); err != nil {
		return storeFailure("mempool write", err)
	}
	count := int64(len(rows))
	ix.update(func(s *IndexerStatus) { s.MempoolIndexed = count })
	return nil
}

// storeFailure logs the database detail and returns a message that is safe to
// show in the browser: no SQL text, file path, or driver internals.
func storeFailure(operation string, err error) error {
	log.Printf("indexer: %s failed: %v", operation, err)
	return fmt.Errorf("explorer database %s failed", operation)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// sanitizeIndexerError keeps node identifiers and method names but drops
// anything that could carry a URL, credential, or file path.
func sanitizeIndexerError(err error) string {
	var methodErr *rpcMethodError
	if errors.As(err, &methodErr) {
		return methodErr.Error()
	}
	message := err.Error()
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}
