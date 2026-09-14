package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPageSize = 25
	maxPageSize     = 100
	maxPageOffset   = 1_000_000
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiErrorBody struct {
	Error apiError `json:"error"`
}

type server struct {
	poller   *poller
	store    *store
	indexer  *indexer
	registry *assetRegistry
	rpc      *rpcPool
	inv      Inventory
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// writeError returns a structured, sanitized failure. Internal detail such as
// SQL text, RPC URLs, or file paths never reaches the browser.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiErrorBody{Error: apiError{Code: code, Message: message}})
}

func internalError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusServiceUnavailable, "TIMEOUT", "The explorer took too long to answer this request.")
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL", "The explorer could not complete this request.")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func pageParams(r *http.Request) (int, int, error) {
	limit := defaultPageSize
	offset := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		if parsed > maxPageSize {
			parsed = maxPageSize
		}
		limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > maxPageOffset {
			return 0, 0, errors.New("offset must be between 0 and 1000000")
		}
		offset = parsed
	}
	return limit, offset, nil
}

func isTxid(s string) bool { return len(s) == 64 && isHex(s) }

func (s *server) routes(mux *http.ServeMux, web http.Handler) {
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)

	// Preserved Phase 1 network telemetry.
	mux.HandleFunc("GET /api/v1/network", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, s.poller.getSnapshot()) })
	mux.HandleFunc("GET /api/v1/nodes", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, s.poller.getSnapshot().Nodes) })
	mux.HandleFunc("GET /api/v1/nodes/{id}", s.poller.nodeHandler)
	mux.HandleFunc("GET /api/v1/topology", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, s.poller.getSnapshot().Topology) })
	mux.HandleFunc("GET /api/v1/producer", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, s.poller.getSnapshot().Producer) })
	mux.HandleFunc("GET /api/v1/economics", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, s.poller.getSnapshot().Economics) })

	// Explorer.
	mux.HandleFunc("GET /api/v1/explorer/status", s.handleExplorerStatus)
	mux.HandleFunc("GET /api/v1/explorer/overview", s.handleOverview)
	mux.HandleFunc("GET /api/v1/blocks", s.handleBlocks)
	mux.HandleFunc("GET /api/v1/blocks/{id}", s.handleBlock)
	mux.HandleFunc("GET /api/v1/blocks/{id}/raw", s.handleBlockRaw)
	mux.HandleFunc("GET /api/v1/transactions", s.handleRecentTransactions)
	mux.HandleFunc("GET /api/v1/transactions/{txid}", s.handleTransaction)
	mux.HandleFunc("GET /api/v1/transactions/{txid}/raw", s.handleTransactionRaw)
	mux.HandleFunc("GET /api/v1/addresses/{address}", s.handleAddress)
	mux.HandleFunc("GET /api/v1/addresses/{address}/utxos", s.handleAddressUTXOs)
	mux.HandleFunc("GET /api/v1/assets", s.handleAssets)
	mux.HandleFunc("GET /api/v1/assets/{id}", s.handleAsset)
	mux.HandleFunc("GET /api/v1/assets/{id}/transactions", s.handleAssetTransactions)
	mux.HandleFunc("GET /api/v1/mempool", s.handleMempool)
	mux.HandleFunc("GET /api/v1/search", s.handleSearch)

	mux.Handle("GET /", web)
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleReady reports readiness to serve indexed queries. A catching-up indexer
// is still ready: the UI shows progress rather than failing.
func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	status := s.indexer.Status()
	body := map[string]any{
		"status":        "ready",
		"indexedHeight": status.IndexedHeight,
		"chainHeight":   status.ChainHeight,
		"synchronized":  status.Synchronized,
	}
	if status.ChainHeight < 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		body["status"] = "waiting for an Elements RPC node"
	}
	writeJSON(w, body)
}

type explorerStatusBody struct {
	Indexer IndexerStatus `json:"indexer"`
	Chain   ChainStats    `json:"chain"`
}

func (s *server) handleExplorerStatus(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.chainStats(r.Context(), 500)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, explorerStatusBody{Indexer: s.indexer.Status(), Chain: stats})
}

type overviewBody struct {
	Network      NetworkSummary `json:"network"`
	Economics    Economics      `json:"economics"`
	Producer     ProducerStatus `json:"producer"`
	Indexer      IndexerStatus  `json:"indexer"`
	Chain        ChainStats     `json:"chain"`
	LatestBlocks []BlockSummary `json:"latestBlocks"`
	RecentTxs    []TxSummary    `json:"recentTransactions"`
	MempoolCount int64          `json:"mempoolTransactions"`
	ManagedNodes int            `json:"managedNodes"`
}

func (s *server) handleOverview(w http.ResponseWriter, r *http.Request) {
	snapshot := s.poller.getSnapshot()
	stats, err := s.store.chainStats(r.Context(), 500)
	if err != nil {
		internalError(w, err)
		return
	}
	blocks, _, err := s.store.blockList(r.Context(), 10, nil)
	if err != nil {
		internalError(w, err)
		return
	}
	txs, err := s.store.recentTransactions(r.Context(), 10)
	if err != nil {
		internalError(w, err)
		return
	}
	status := s.indexer.Status()
	writeJSON(w, overviewBody{
		Network:      snapshot.Network,
		Economics:    snapshot.Economics,
		Producer:     snapshot.Producer,
		Indexer:      status,
		Chain:        stats,
		LatestBlocks: blocks,
		RecentTxs:    txs,
		MempoolCount: status.MempoolIndexed,
		ManagedNodes: len(s.inv.Nodes),
	})
}

type blockListBody struct {
	Blocks []BlockSummary `json:"blocks"`
	Page   Page           `json:"page"`
}

func (s *server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	limit, _, err := pageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}
	var before *int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, convErr := strconv.ParseInt(raw, 10, 64)
		if convErr != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "INVALID_CURSOR", "before must be a non-negative block height")
			return
		}
		before = &parsed
	}
	blocks, page, err := s.store.blockList(r.Context(), limit, before)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, blockListBody{Blocks: blocks, Page: page})
}

// resolveBlock accepts either a height or a block hash.
func (s *server) resolveBlock(ctx context.Context, id string) (int64, error) {
	if len(id) == 64 && isHex(id) {
		return s.heightForHashOrRPC(ctx, id)
	}
	height, err := strconv.ParseInt(id, 10, 64)
	if err != nil || height < 0 {
		return 0, errNotFound
	}
	return height, nil
}

func (s *server) heightForHashOrRPC(ctx context.Context, hash string) (int64, error) {
	return s.store.heightForHash(ctx, hash)
}

func (s *server) handleBlock(w http.ResponseWriter, r *http.Request) {
	height, err := s.resolveBlock(r.Context(), r.PathValue("id"))
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "No indexed block matches that height or hash.")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	limit, offset, err := pageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}
	detail, err := s.store.blockDetail(r.Context(), height, limit, offset)
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "No indexed block matches that height or hash.")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, detail)
}

func (s *server) handleBlockRaw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	hash := id
	if !(len(id) == 64 && isHex(id)) {
		height, err := s.resolveBlock(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "No indexed block matches that height or hash.")
			return
		}
		detail, err := s.store.blockDetail(r.Context(), height, 1, 0)
		if err != nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "No indexed block matches that height or hash.")
			return
		}
		hash = detail.Hash
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	raw, err := s.rpc.rawJSON(ctx, "getblock", hash, 2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "RPC_UNAVAILABLE", "The Elements node did not return this block.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (s *server) handleRecentTransactions(w http.ResponseWriter, r *http.Request) {
	limit, _, err := pageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}
	txs, err := s.store.recentTransactions(r.Context(), limit)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"transactions": txs, "page": Page{Limit: limit, Total: int64(len(txs))}})
}

func (s *server) handleTransaction(w http.ResponseWriter, r *http.Request) {
	txid := strings.ToLower(r.PathValue("txid"))
	if !isTxid(txid) {
		writeError(w, http.StatusBadRequest, "INVALID_TXID", "A transaction ID is 64 hexadecimal characters.")
		return
	}
	detail, err := s.store.transactionDetail(r.Context(), txid, s.indexer.Status().ChainHeight)
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "No indexed transaction matches that ID.")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, detail)
}

func (s *server) handleTransactionRaw(w http.ResponseWriter, r *http.Request) {
	txid := strings.ToLower(r.PathValue("txid"))
	if !isTxid(txid) {
		writeError(w, http.StatusBadRequest, "INVALID_TXID", "A transaction ID is 64 hexadecimal characters.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	params := []any{txid, true}
	if detail, err := s.store.transactionDetail(ctx, txid, 0); err == nil {
		params = append(params, detail.BlockHash)
	}
	raw, err := s.rpc.rawJSON(ctx, "getrawtransaction", params...)
	if err != nil {
		writeError(w, http.StatusBadGateway, "RPC_UNAVAILABLE", "The Elements node did not return this transaction.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

type addressBody struct {
	Summary      AddressSummary `json:"summary"`
	Transactions []TxSummary    `json:"transactions"`
	Page         Page           `json:"page"`
}

func validAddress(s string) bool {
	if len(s) < 8 || len(s) > 120 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (s *server) handleAddress(w http.ResponseWriter, r *http.Request) {
	address := r.PathValue("address")
	if !validAddress(address) {
		writeError(w, http.StatusBadRequest, "INVALID_ADDRESS", "That does not look like an Elements address.")
		return
	}
	limit, offset, err := pageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}
	summary, err := s.store.addressSummary(r.Context(), address)
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "No indexed output pays that script address.")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	txs, page, err := s.store.addressTransactions(r.Context(), address, limit, offset)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, addressBody{Summary: *summary, Transactions: txs, Page: page})
}

func (s *server) handleAddressUTXOs(w http.ResponseWriter, r *http.Request) {
	address := r.PathValue("address")
	if !validAddress(address) {
		writeError(w, http.StatusBadRequest, "INVALID_ADDRESS", "That does not look like an Elements address.")
		return
	}
	limit, offset, err := pageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}
	utxos, page, err := s.store.addressUTXOs(r.Context(), address, limit, offset)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"utxos": utxos, "page": page})
}

func (s *server) handleAssets(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}
	assets, page, err := s.store.assetList(r.Context(), limit, offset)
	if err != nil {
		internalError(w, err)
		return
	}
	for i := range assets {
		assets[i].Metadata = s.registry.lookup(assets[i].AssetID)
	}
	writeJSON(w, map[string]any{"assets": assets, "page": page, "policyAsset": s.policyAsset()})
}

func (s *server) handleAsset(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.PathValue("id"))
	if !isTxid(id) {
		writeError(w, http.StatusBadRequest, "INVALID_ASSET_ID", "An Asset ID is 64 hexadecimal characters.")
		return
	}
	record, err := s.store.asset(r.Context(), id)
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "No indexed issuance created that Asset ID.")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	record.Metadata = s.registry.lookup(record.AssetID)
	limit, offset, err := pageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}
	issuances, page, err := s.store.assetIssuances(r.Context(), id, limit, offset)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"asset": record, "issuances": issuances, "page": page})
}

func (s *server) handleAssetTransactions(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.PathValue("id"))
	if !isTxid(id) {
		writeError(w, http.StatusBadRequest, "INVALID_ASSET_ID", "An Asset ID is 64 hexadecimal characters.")
		return
	}
	limit, offset, err := pageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}
	txs, page, err := s.store.assetTransactions(r.Context(), id, limit, offset)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"transactions": txs, "page": page})
}

func (s *server) handleMempool(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}
	entries, page, err := s.store.mempool(r.Context(), limit, offset)
	if err != nil {
		internalError(w, err)
		return
	}
	snapshot := s.poller.getSnapshot()
	writeJSON(w, map[string]any{
		"transactions":  entries,
		"page":          page,
		"reportedBytes": snapshot.Network.MempoolBytes,
		"reportedUsage": snapshot.Network.MempoolUsage,
		"reportedCount": snapshot.Network.MempoolTransactions,
		"indexedAt":     s.indexer.Status().UpdatedAt,
	})
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 128 {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY", "Search for a block height, block hash, transaction ID, Asset ID, or address.")
		return
	}
	var height *int64
	if parsed, err := strconv.ParseInt(query, 10, 64); err == nil && parsed >= 0 {
		height = &parsed
	}
	hits, err := s.store.search(r.Context(), strings.ToLower(query), height)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"query": query, "results": hits})
}

func (s *server) policyAsset() string {
	return s.indexer.Status().PolicyAsset
}

func formatInt(v int64) string { return strconv.FormatInt(v, 10) }
