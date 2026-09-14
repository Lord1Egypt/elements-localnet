package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// maxRPCResponseBytes bounds a single RPC response so a hostile or broken node
// cannot exhaust explorer memory.
const maxRPCResponseBytes = 64 << 20

type rpcTarget struct {
	id    string
	url   string
	cred  credentials
	rank  int
	calls int64
}

// rpcPool issues read-only RPC calls, preferring the archival/txindex node and
// failing over to any other configured node.
type rpcPool struct {
	client  *http.Client
	mu      sync.Mutex
	targets []*rpcTarget
}

func newRPCPool(inv Inventory, secretsDir string, preferred string, timeout time.Duration) (*rpcPool, error) {
	pool := &rpcPool{client: &http.Client{Timeout: timeout}}
	for _, node := range inv.Nodes {
		cred, err := readCredentials(secretsDir + "/" + node.ID + ".rpc")
		if err != nil {
			continue
		}
		rank := 2
		if node.ID == preferred {
			rank = 0
		} else if hasCapability(node, "txindex") {
			rank = 1
		}
		pool.targets = append(pool.targets, &rpcTarget{
			id:   node.ID,
			url:  fmt.Sprintf("http://%s:%d/", node.RPCHost, node.RPCPort),
			cred: cred,
			rank: rank,
		})
	}
	if len(pool.targets) == 0 {
		return nil, errors.New("no explorer RPC credentials are available")
	}
	sort.SliceStable(pool.targets, func(i, j int) bool { return pool.targets[i].rank < pool.targets[j].rank })
	return pool, nil
}

func hasCapability(node NodeSpec, want string) bool {
	for _, capability := range node.Capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

type rpcRequest struct {
	Method string
	Params []any
}

// call runs one or more RPC methods as a single batch, trying each node in
// preference order. It returns results positionally.
func (p *rpcPool) call(ctx context.Context, requests ...rpcRequest) ([]json.RawMessage, error) {
	var lastErr error
	for _, target := range p.snapshotTargets() {
		results, err := p.callTarget(ctx, target, requests)
		if err == nil {
			return results, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no RPC target responded")
	}
	return nil, lastErr
}

func (p *rpcPool) snapshotTargets() []*rpcTarget {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*rpcTarget, len(p.targets))
	copy(out, p.targets)
	return out
}

func (p *rpcPool) callTarget(ctx context.Context, target *rpcTarget, requests []rpcRequest) ([]json.RawMessage, error) {
	calls := make([]rpcCall, len(requests))
	for i, request := range requests {
		params := request.Params
		if params == nil {
			params = []any{}
		}
		calls[i] = rpcCall{JSONRPC: "1.0", ID: strconv.Itoa(i), Method: request.Method, Params: params}
	}
	body, err := json.Marshal(calls)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(target.cred.user, target.cred.password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: RPC transport unavailable", target.id)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxRPCResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%s: RPC response truncated", target.id)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: RPC HTTP status %d", target.id, resp.StatusCode)
	}
	var responses []rpcResponse
	if err := json.Unmarshal(payload, &responses); err != nil {
		return nil, fmt.Errorf("%s: invalid RPC JSON", target.id)
	}
	results := make([]json.RawMessage, len(requests))
	seen := 0
	for _, item := range responses {
		index, convErr := strconv.Atoi(item.ID)
		if convErr != nil || index < 0 || index >= len(results) {
			return nil, fmt.Errorf("%s: unexpected RPC response identifier", target.id)
		}
		if item.Error != nil {
			return nil, &rpcMethodError{node: target.id, method: requests[index].Method, code: item.Error.Code, message: item.Error.Message}
		}
		results[index] = item.Result
		seen++
	}
	if seen != len(requests) {
		return nil, fmt.Errorf("%s: incomplete RPC batch response", target.id)
	}
	return results, nil
}

type rpcMethodError struct {
	node    string
	method  string
	code    int
	message string
}

func (e *rpcMethodError) Error() string {
	return fmt.Sprintf("%s: %s failed with RPC code %d", e.node, e.method, e.code)
}

func (p *rpcPool) callOne(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	results, err := p.call(ctx, rpcRequest{Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

func (p *rpcPool) chainInfo(ctx context.Context) (rpcChainInfo, error) {
	var info rpcChainInfo
	raw, err := p.callOne(ctx, "getblockchaininfo")
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return info, errors.New("invalid getblockchaininfo response")
	}
	if info.Blocks < 0 || len(info.BestBlockHash) != 64 {
		return info, errors.New("implausible getblockchaininfo response")
	}
	return info, nil
}

func (p *rpcPool) blockHash(ctx context.Context, height int64) (string, error) {
	raw, err := p.callOne(ctx, "getblockhash", height)
	if err != nil {
		return "", err
	}
	var hash string
	if err := json.Unmarshal(raw, &hash); err != nil || len(hash) != 64 {
		return "", errors.New("invalid getblockhash response")
	}
	return hash, nil
}

// blocks fetches a contiguous range with one batch per call site, bounding both
// the number of blocks and the resulting memory footprint.
func (p *rpcPool) blocks(ctx context.Context, hashes []string) ([]rpcBlock, error) {
	requests := make([]rpcRequest, len(hashes))
	for i, hash := range hashes {
		requests[i] = rpcRequest{Method: "getblock", Params: []any{hash, 2}}
	}
	results, err := p.call(ctx, requests...)
	if err != nil {
		return nil, err
	}
	blocks := make([]rpcBlock, len(results))
	for i, raw := range results {
		if err := json.Unmarshal(raw, &blocks[i]); err != nil {
			return nil, errors.New("invalid getblock response")
		}
		if len(blocks[i].Hash) != 64 {
			return nil, errors.New("implausible getblock response")
		}
	}
	return blocks, nil
}

func (p *rpcPool) rawJSON(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	return p.callOne(ctx, method, params...)
}

// backoff produces a bounded exponential delay with a fixed ceiling.
func backoff(attempt int, base, ceiling time.Duration) time.Duration {
	delay := base
	for i := 0; i < attempt && delay < ceiling; i++ {
		delay *= 2
	}
	if delay > ceiling {
		delay = ceiling
	}
	return delay
}
