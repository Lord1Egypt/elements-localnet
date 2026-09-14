package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeNode serves the read-only subset of the Elements RPC surface the indexer
// uses, backed by a synthetic chain the test can swap at will.
type fakeNode struct {
	mu     sync.Mutex
	chain  *syntheticChain
	server *httptest.Server
	calls  int
}

func newFakeNode(t *testing.T, chain *syntheticChain) *fakeNode {
	node := &fakeNode{chain: chain}
	node.server = httptest.NewServer(http.HandlerFunc(node.serve))
	t.Cleanup(node.server.Close)
	return node
}

func (n *fakeNode) setChain(chain *syntheticChain) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.chain = chain
}

func (n *fakeNode) serve(w http.ResponseWriter, r *http.Request) {
	var calls []rpcCall
	if err := json.NewDecoder(r.Body).Decode(&calls); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	n.mu.Lock()
	chain := n.chain
	n.calls++
	n.mu.Unlock()

	responses := make([]rpcResponse, 0, len(calls))
	for _, call := range calls {
		response := rpcResponse{ID: call.ID}
		switch call.Method {
		case "getsidechaininfo":
			response.Result = mustJSON(map[string]string{"pegged_asset": testPolicyAsset})
		case "getblockchaininfo":
			response.Result = mustJSON(rpcChainInfo{Chain: "elements", Blocks: chain.height(), Headers: chain.height(), BestBlockHash: chain.tipHash()})
		case "getblockhash":
			height := int64(call.Params[0].(float64))
			if height < 0 || height > chain.height() {
				response.Error = &rpcError{Code: -8, Message: "block height out of range"}
			} else {
				response.Result = mustJSON(chain.blocks[height].Hash)
			}
		case "getblock":
			hash, _ := call.Params[0].(string)
			found := false
			for _, block := range chain.blocks {
				if block.Hash == hash {
					response.Result = mustJSON(block)
					found = true
					break
				}
			}
			if !found {
				response.Error = &rpcError{Code: -5, Message: "block not found"}
			}
		case "getrawmempool":
			response.Result = mustJSON(map[string]rpcMempoolEntry{})
		default:
			response.Error = &rpcError{Code: -32601, Message: "method not permitted"}
		}
		responses = append(responses, response)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(responses)
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func newTestIndexer(t *testing.T, s *store, node *fakeNode, batch int) *indexer {
	t.Helper()
	pool := &rpcPool{
		client:  &http.Client{Timeout: 5 * time.Second},
		targets: []*rpcTarget{{id: "fixture", url: node.server.URL + "/", cred: credentials{user: "u", password: "p"}}},
	}
	cfg := chainConfig{SubsidySats: 5000000000, HalvingInterval: 210000}
	return newIndexer(s, pool, cfg, "fixture", batch, time.Millisecond, 5*time.Second)
}

func runUntilIdle(t *testing.T, ix *indexer, limit int) {
	t.Helper()
	for i := 0; i < limit; i++ {
		progressed, err := ix.step(context.Background())
		if err != nil {
			t.Fatalf("step: %v", err)
		}
		if !progressed {
			return
		}
	}
	t.Fatalf("indexer did not settle within %d steps", limit)
}

func TestIndexerFollowsChainFromGenesis(t *testing.T) {
	s := newTestStore(t)
	chain := newSyntheticChain("a")
	for i := 0; i < 6; i++ {
		chain.extend()
	}
	node := newFakeNode(t, chain)
	ix := newTestIndexer(t, s, node, 3)

	runUntilIdle(t, ix, 20)
	status := ix.Status()
	if status.IndexedHeight != 5 || status.IndexedBlockHash != chain.tipHash() {
		t.Fatalf("status = %+v, want height 5 at %s", status, chain.tipHash())
	}
	if !status.Synchronized || status.Lag != 0 {
		t.Fatalf("indexer did not report synchronized: %+v", status)
	}
	if status.PolicyAsset != testPolicyAsset {
		t.Errorf("policy asset = %q", status.PolicyAsset)
	}
}

func TestIndexerDetectsReorgAndFollowsTheNewBranch(t *testing.T) {
	s := newTestStore(t)
	original := newSyntheticChain("a")
	for i := 0; i < 6; i++ {
		original.extend()
	}
	node := newFakeNode(t, original)
	ix := newTestIndexer(t, s, node, 3)
	runUntilIdle(t, ix, 20)

	replacement := original.fork("b", 2)
	for i := 0; i < 5; i++ {
		replacement.extend()
	}
	if replacement.blocks[3].Hash == original.blocks[3].Hash {
		t.Fatal("fixture error: the replacement branch is identical")
	}
	node.setChain(replacement)

	runUntilIdle(t, ix, 40)
	status := ix.Status()
	if status.IndexedHeight != replacement.height() || status.IndexedBlockHash != replacement.tipHash() {
		t.Fatalf("status = %+v, want height %d at %s", status, replacement.height(), replacement.tipHash())
	}
	if status.ReorgsHandled < 3 {
		t.Errorf("reorgsHandled = %d, want at least 3 rolled-back blocks", status.ReorgsHandled)
	}

	detail, err := s.blockDetail(context.Background(), 3, 10, 0)
	if err != nil {
		t.Fatalf("blockDetail: %v", err)
	}
	if detail.Hash != replacement.blocks[3].Hash {
		t.Errorf("height 3 still holds the orphaned hash %s", detail.Hash)
	}
	for _, orphan := range original.blocks[3:] {
		var count int
		if err := s.read.QueryRow(`SELECT COUNT(*) FROM blocks WHERE hash = ?`, orphan.Hash).Scan(&count); err != nil {
			t.Fatalf("count orphan: %v", err)
		}
		if count != 0 {
			t.Errorf("orphaned block %s is still indexed", orphan.Hash)
		}
	}
}

func TestIndexerResumesFromItsStoredCursor(t *testing.T) {
	s := newTestStore(t)
	chain := newSyntheticChain("a")
	for i := 0; i < 8; i++ {
		chain.extend()
	}
	node := newFakeNode(t, chain)
	first := newTestIndexer(t, s, node, 4)
	runUntilIdle(t, first, 20)

	// A second indexer over the same database stands in for a container
	// restart: it must resume at the stored cursor, not rescan from genesis.
	resumed := newTestIndexer(t, s, node, 4)
	if status := resumed.Status(); status.IndexedHeight != 7 {
		t.Fatalf("resumed indexer starts at height %d", status.IndexedHeight)
	}
	before := node.calls
	runUntilIdle(t, resumed, 5)
	if status := resumed.Status(); status.BlocksIndexed != 0 {
		t.Errorf("resumed indexer re-indexed %d blocks", status.BlocksIndexed)
	}
	if node.calls-before > 2 {
		t.Errorf("resumed indexer issued %d RPC round trips to confirm the tip", node.calls-before)
	}
}
