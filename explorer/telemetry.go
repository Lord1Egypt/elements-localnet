package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Inventory struct {
	NetworkName           string     `json:"networkName"`
	PollIntervalSeconds   int        `json:"pollIntervalSeconds"`
	RPCTimeoutMillis      int        `json:"rpcTimeoutMillis"`
	StaleThresholdSeconds int        `json:"staleThresholdSeconds"`
	BlockRewardSats       uint64     `json:"blockRewardSats"`
	HalvingInterval       uint64     `json:"halvingInterval"`
	BlockInterval         int        `json:"blockInterval"`
	AutoMine              bool       `json:"autoMine"`
	ProducerStatusFile    string     `json:"producerStatusFile"`
	Nodes                 []NodeSpec `json:"nodes"`
}

type NodeSpec struct {
	ID              string   `json:"id"`
	Role            string   `json:"role"`
	RPCHost         string   `json:"rpcHost"`
	RPCPort         int      `json:"rpcPort"`
	NetworkIP       string   `json:"networkIp"`
	CredentialsFile string   `json:"credentialsFile"`
	Capabilities    []string `json:"capabilities,omitempty"`
}

type NodeStatus struct {
	ID                   string    `json:"id"`
	Role                 string    `json:"role"`
	Capabilities         []string  `json:"capabilities,omitempty"`
	State                string    `json:"state"`
	Online               bool      `json:"online"`
	HealthState          string    `json:"healthState"`
	BlockHeight          int64     `json:"blockHeight"`
	BestBlockHash        string    `json:"bestBlockHash"`
	HeaderHeight         int64     `json:"headerHeight"`
	VerificationProgress float64   `json:"verificationProgress"`
	InitialBlockDownload bool      `json:"initialBlockDownload"`
	PeerCount            int       `json:"peerCount"`
	InboundPeers         int       `json:"inboundPeers"`
	OutboundPeers        int       `json:"outboundPeers"`
	MempoolTransactions  int64     `json:"mempoolTransactions"`
	MempoolBytes         int64     `json:"mempoolBytes"`
	MempoolUsage         int64     `json:"mempoolUsage"`
	ExternalPeers        int       `json:"externalPeerConnections"`
	Chainwork            string    `json:"chainwork"`
	Version              int       `json:"version"`
	Subversion           string    `json:"subversion"`
	Chain                string    `json:"chain"`
	BlockchainSize       int64     `json:"blockchainSize"`
	LatestBlockTime      int64     `json:"latestBlockTime"`
	RPCLatencyMillis     int64     `json:"rpcLatencyMillis"`
	LastSuccessfulPoll   time.Time `json:"lastSuccessfulPoll,omitempty"`
	LastError            string    `json:"lastError,omitempty"`
	PeerAddresses        []string  `json:"-"`
	HasForkTip           bool      `json:"-"`
}

type NetworkSummary struct {
	NetworkName                  string    `json:"networkName"`
	Chain                        string    `json:"chain"`
	TotalNodes                   int       `json:"totalNodes"`
	OnlineNodes                  int       `json:"onlineNodes"`
	OfflineNodes                 int       `json:"offlineNodes"`
	SynchronizedNodes            int       `json:"synchronizedNodes"`
	CatchingUpNodes              int       `json:"catchingUpNodes"`
	DivergentNodes               int       `json:"divergentNodes"`
	CanonicalHeight              int64     `json:"canonicalHeight"`
	CanonicalBestHash            string    `json:"canonicalBestBlockHash"`
	LatestBlockTimestamp         int64     `json:"latestBlockTimestamp"`
	LatestBlockAgeSeconds        int64     `json:"latestBlockAgeSeconds"`
	TotalPeerConnections         int       `json:"totalPeerConnections"`
	ExternalPeerConnections      int       `json:"externalPeerConnections"`
	ManagedNodeLinks             int       `json:"managedNodeLinks"`
	MempoolTransactions          int64     `json:"mempoolTransactions"`
	MempoolBytes                 int64     `json:"mempoolBytes"`
	MempoolUsage                 int64     `json:"mempoolUsage"`
	AggregateChainBytes          int64     `json:"aggregateBlockchainSize"`
	UpdatedAt                    time.Time `json:"updatedAt"`
	BlockProductionRatePerMinute float64   `json:"blockProductionRatePerMinute"`
}

type Economics struct {
	InitialRewardSats            uint64 `json:"initialRewardSats,string"`
	CurrentRewardSats            uint64 `json:"currentRewardSats,string"`
	HalvingInterval              uint64 `json:"halvingInterval"`
	CurrentRewardEra             uint64 `json:"currentRewardEra"`
	NextHalvingHeight            uint64 `json:"nextHalvingHeight"`
	BlocksUntilHalving           uint64 `json:"blocksUntilNextHalving"`
	EstimatedSecondsUntilHalving uint64 `json:"estimatedSecondsUntilHalving"`
}

type ProducerStatus struct {
	Enabled               bool   `json:"enabled"`
	Running               bool   `json:"running"`
	PayoutAddressMasked   string `json:"payoutAddressMasked"`
	BlockInterval         int    `json:"blockInterval"`
	LastAttempt           string `json:"lastAttempt,omitempty"`
	LastSuccess           string `json:"lastSuccess,omitempty"`
	LastProducedTimestamp string `json:"lastProducedTimestamp,omitempty"`
	LastProducedHeight    int64  `json:"lastProducedHeight,omitempty"`
	LastProducedBlockHash string `json:"lastProducedBlockHash,omitempty"`
	NextScheduledAttempt  string `json:"nextScheduledAttempt,omitempty"`
	LastError             string `json:"lastError,omitempty"`
	ConsecutiveFailures   int    `json:"consecutiveFailures"`
	GenerationWarning     string `json:"generationWarning,omitempty"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type Topology struct {
	Nodes []NodeStatus `json:"nodes"`
	Edges []Edge       `json:"edges"`
}
type Snapshot struct {
	Network   NetworkSummary `json:"network"`
	Nodes     []NodeStatus   `json:"nodes"`
	Topology  Topology       `json:"topology"`
	Producer  ProducerStatus `json:"producer"`
	Economics Economics      `json:"economics"`
}

type rpcCall struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}
type rpcResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type credentials struct{ user, password string }

type poller struct {
	inv      Inventory
	client   *http.Client
	mu       sync.RWMutex
	snapshot Snapshot
	samples  []heightSample
}
type heightSample struct {
	at     time.Time
	height int64
}

func validateInventory(inv Inventory) error {
	if inv.NetworkName == "" || len(inv.Nodes) == 0 {
		return errors.New("networkName and nodes are required")
	}
	if inv.PollIntervalSeconds < 1 || inv.RPCTimeoutMillis < 100 || inv.StaleThresholdSeconds < 1 || inv.HalvingInterval == 0 {
		return errors.New("invalid timing or halving configuration")
	}
	seen := map[string]bool{}
	for _, n := range inv.Nodes {
		if n.ID == "" || n.RPCHost == "" || n.RPCPort < 1 || n.CredentialsFile == "" || seen[n.ID] {
			return fmt.Errorf("invalid or duplicate node %q", n.ID)
		}
		seen[n.ID] = true
	}
	return nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (p *poller) run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(p.inv.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.refresh(ctx)
		}
	}
}

func (p *poller) refresh(ctx context.Context) {
	nodes := make([]NodeStatus, len(p.inv.Nodes))
	var wg sync.WaitGroup
	for i, spec := range p.inv.Nodes {
		wg.Add(1)
		go func(i int, spec NodeSpec) { defer wg.Done(); nodes[i] = p.pollNode(ctx, spec) }(i, spec)
	}
	wg.Wait()
	now := time.Now().UTC()
	snapshot := aggregate(p.inv, nodes, now)
	snapshot.Producer = readProducer(p.inv)
	p.mu.Lock()
	if p.snapshot.Network.CanonicalHeight >= 0 && snapshot.Network.CanonicalHeight > p.snapshot.Network.CanonicalHeight && !snapshot.Producer.Running {
		if snapshot.Producer.Enabled {
			snapshot.Producer.GenerationWarning = "ORPHANED_PRODUCTION"
		} else {
			snapshot.Producer.GenerationWarning = "EXTERNAL_GENERATION"
		}
	}
	p.samples = append(p.samples, heightSample{at: now, height: snapshot.Network.CanonicalHeight})
	cutoff := now.Add(-60 * time.Second)
	first := 0
	for first < len(p.samples)-1 && p.samples[first].at.Before(cutoff) {
		first++
	}
	p.samples = p.samples[first:]
	if len(p.samples) > 1 {
		a, b := p.samples[0], p.samples[len(p.samples)-1]
		seconds := b.at.Sub(a.at).Seconds()
		if seconds > 0 && b.height >= a.height {
			snapshot.Network.BlockProductionRatePerMinute = float64(b.height-a.height) * 60 / seconds
		}
	}
	p.snapshot = snapshot
	p.mu.Unlock()
}

func (p *poller) pollNode(parent context.Context, spec NodeSpec) NodeStatus {
	start := time.Now()
	n := NodeStatus{ID: spec.ID, Role: spec.Role, Capabilities: spec.Capabilities, State: "OFFLINE", HealthState: "OFFLINE", BlockHeight: -1, HeaderHeight: -1}
	cred, err := readCredentials(spec.CredentialsFile)
	if err != nil {
		n.State = "ERROR"
		n.HealthState = "ERROR"
		n.LastError = "MONITOR_CREDENTIALS_UNAVAILABLE"
		return n
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(p.inv.RPCTimeoutMillis)*time.Millisecond)
	defer cancel()
	methods := []string{"getblockchaininfo", "getnetworkinfo", "getmempoolinfo", "getpeerinfo", "getconnectioncount", "getchaintips", "getmininginfo"}
	responses, reached, err := p.rpcBatch(ctx, spec, cred, methods)
	n.RPCLatencyMillis = time.Since(start).Milliseconds()
	if err != nil {
		if reached {
			n.State = "ERROR"
			n.HealthState = "ERROR"
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			n.LastError = "RPC_TIMEOUT"
		} else if reached {
			n.LastError = "RPC_RESPONSE_INVALID"
		} else {
			n.LastError = "RPC_UNAVAILABLE"
		}
		return n
	}
	if err := decodeNode(&n, responses); err != nil {
		n.Online = true
		n.State = "ERROR"
		n.HealthState = "ERROR"
		n.LastError = "RPC_RESPONSE_INVALID"
		return n
	}
	headerResult, _, err := p.rpcBatch(ctx, spec, cred, []string{"getblockheader:" + n.BestBlockHash})
	if err != nil {
		n.Online = true
		n.State = "ERROR"
		n.HealthState = "ERROR"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			n.LastError = "RPC_TIMEOUT"
		} else {
			n.LastError = "RPC_RESPONSE_INVALID"
		}
		return n
	}
	var header struct {
		Time int64 `json:"time"`
	}
	if err := json.Unmarshal(headerResult["getblockheader"], &header); err != nil || header.Time <= 0 {
		n.Online = true
		n.State = "ERROR"
		n.HealthState = "ERROR"
		n.LastError = "invalid getblockheader response"
		return n
	}
	n.LatestBlockTime = header.Time
	n.Online = true
	n.LastSuccessfulPoll = time.Now().UTC()
	n.State = "HEALTHY"
	n.HealthState = "HEALTHY"
	return n
}

func readCredentials(path string) (credentials, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, err
	}
	c := credentials{}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key == "RPC_USER" {
			c.user = value
		}
		if key == "RPC_PASSWORD" {
			c.password = value
		}
	}
	if c.user == "" || c.password == "" {
		return credentials{}, errors.New("monitor credential file is malformed")
	}
	return c, nil
}

func (p *poller) rpcBatch(ctx context.Context, spec NodeSpec, cred credentials, methods []string) (map[string]json.RawMessage, bool, error) {
	calls := make([]rpcCall, 0, len(methods))
	aliases := map[string]string{}
	for i, methodSpec := range methods {
		method, arg, hasArg := strings.Cut(methodSpec, ":")
		params := []any{}
		if hasArg {
			params = []any{arg}
		}
		id := strconv.Itoa(i)
		calls = append(calls, rpcCall{JSONRPC: "1.0", ID: id, Method: method, Params: params})
		aliases[id] = method
	}
	body, _ := json.Marshal(calls)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s:%d/", spec.RPCHost, spec.RPCPort), strings.NewReader(string(body)))
	if err != nil {
		return nil, false, err
	}
	req.SetBasicAuth(cred.user, cred.password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	reached := true
	limited := io.LimitReader(resp.Body, 4<<20)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, reached, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, reached, fmt.Errorf("RPC HTTP status %d", resp.StatusCode)
	}
	var responses []rpcResponse
	if err := json.Unmarshal(responseBody, &responses); err != nil {
		return nil, reached, errors.New("invalid RPC JSON")
	}
	results := map[string]json.RawMessage{}
	for _, item := range responses {
		name := aliases[item.ID]
		if item.Error != nil {
			return nil, reached, fmt.Errorf("%s RPC error %d: %s", name, item.Error.Code, item.Error.Message)
		}
		results[name] = item.Result
	}
	if len(results) != len(methods) {
		return nil, reached, errors.New("incomplete RPC batch response")
	}
	return results, reached, nil
}

func decodeNode(n *NodeStatus, r map[string]json.RawMessage) error {
	var chain struct {
		Chain     string  `json:"chain"`
		Blocks    int64   `json:"blocks"`
		Headers   int64   `json:"headers"`
		Best      string  `json:"bestblockhash"`
		Progress  float64 `json:"verificationprogress"`
		IBD       bool    `json:"initialblockdownload"`
		Chainwork string  `json:"chainwork"`
		Size      int64   `json:"size_on_disk"`
	}
	var network struct {
		Version     int    `json:"version"`
		Subversion  string `json:"subversion"`
		Connections int    `json:"connections"`
	}
	var mempool struct {
		Size  int64 `json:"size"`
		Bytes int64 `json:"bytes"`
		Usage int64 `json:"usage"`
	}
	var peers []struct {
		Inbound bool   `json:"inbound"`
		Addr    string `json:"addr"`
	}
	var tips []struct {
		BranchLen int64  `json:"branchlen"`
		Status    string `json:"status"`
	}
	for raw, target := range map[string]any{"getblockchaininfo": &chain, "getnetworkinfo": &network, "getmempoolinfo": &mempool, "getpeerinfo": &peers, "getchaintips": &tips} {
		if err := json.Unmarshal(r[raw], target); err != nil {
			return fmt.Errorf("invalid %s response", raw)
		}
	}
	if chain.Blocks < 0 || chain.Headers < 0 || len(chain.Best) != 64 || chain.Chain == "" {
		return errors.New("invalid blockchain fields")
	}
	n.BlockHeight = chain.Blocks
	n.HeaderHeight = chain.Headers
	n.BestBlockHash = chain.Best
	n.VerificationProgress = chain.Progress
	n.InitialBlockDownload = chain.IBD
	n.Chainwork = chain.Chainwork
	n.BlockchainSize = chain.Size
	n.Chain = chain.Chain
	n.Version = network.Version
	n.Subversion = network.Subversion
	n.PeerCount = network.Connections
	n.MempoolTransactions = mempool.Size
	n.MempoolBytes = mempool.Bytes
	n.MempoolUsage = mempool.Usage
	for _, peer := range peers {
		n.PeerAddresses = append(n.PeerAddresses, peer.Addr)
		if peer.Inbound {
			n.InboundPeers++
		} else {
			n.OutboundPeers++
		}
	}
	for _, tip := range tips {
		if tip.BranchLen > 0 && (tip.Status == "valid-fork" || tip.Status == "active") {
			n.HasForkTip = true
		}
	}
	return nil
}

func aggregate(inv Inventory, nodes []NodeStatus, now time.Time) Snapshot {
	height, hash := canonical(nodes)
	// Peers that are not one of the managed nodes are counted as external
	// connections. The number of distinct external nodes behind them cannot be
	// determined reliably, so only the connection count is reported.
	managedHosts := map[string]bool{}
	for _, spec := range inv.Nodes {
		managedHosts[spec.NetworkIP] = true
		managedHosts[spec.ID] = true
		managedHosts[spec.RPCHost] = true
	}
	for i := range nodes {
		for _, addr := range nodes[i].PeerAddresses {
			host := strings.Trim(strings.Split(addr, ":")[0], "[]")
			if !managedHosts[host] {
				nodes[i].ExternalPeers++
			}
		}
	}
	summary := NetworkSummary{NetworkName: inv.NetworkName, TotalNodes: len(nodes), CanonicalHeight: height, CanonicalBestHash: hash, UpdatedAt: now}
	for i := range nodes {
		classify(&nodes[i], height, hash, now, time.Duration(inv.StaleThresholdSeconds)*time.Second)
		n := nodes[i]
		if n.Online {
			summary.OnlineNodes++
			if summary.Chain == "" {
				summary.Chain = n.Chain
			}
			summary.TotalPeerConnections += n.PeerCount
			summary.ExternalPeerConnections += n.ExternalPeers
			summary.MempoolTransactions += n.MempoolTransactions
			summary.MempoolBytes += n.MempoolBytes
			summary.MempoolUsage += n.MempoolUsage
			summary.AggregateChainBytes += n.BlockchainSize
		} else {
			summary.OfflineNodes++
		}
		if n.BestBlockHash == hash && n.BlockHeight == height && n.Online {
			summary.SynchronizedNodes++
		}
		if n.State == "SYNCING" {
			summary.CatchingUpNodes++
		}
		if n.State == "DIVERGED" || n.State == "FORKED" {
			summary.DivergentNodes++
		}
		if n.BestBlockHash == hash && n.LatestBlockTime > 0 {
			summary.LatestBlockTimestamp = n.LatestBlockTime
		}
	}
	if summary.LatestBlockTimestamp > 0 {
		summary.LatestBlockAgeSeconds = now.Unix() - summary.LatestBlockTimestamp
		if summary.LatestBlockAgeSeconds < 0 {
			summary.LatestBlockAgeSeconds = 0
		}
	}
	topology := buildTopology(inv, nodes)
	summary.ManagedNodeLinks = len(topology.Edges)
	economics := economics(inv.BlockRewardSats, inv.HalvingInterval, uint64(inv.BlockInterval), height)
	return Snapshot{Network: summary, Nodes: nodes, Topology: topology, Economics: economics}
}

func canonical(nodes []NodeStatus) (int64, string) {
	type key struct {
		h    int64
		hash string
	}
	counts := map[key]int{}
	for _, n := range nodes {
		if n.Online && n.State != "ERROR" {
			counts[key{n.BlockHeight, n.BestBlockHash}]++
		}
	}
	best := key{h: -1}
	votes := -1
	for k, c := range counts {
		if c > votes || (c == votes && k.h > best.h) || (c == votes && k.h == best.h && k.hash < best.hash) {
			best = k
			votes = c
		}
	}
	return best.h, best.hash
}

func classify(n *NodeStatus, height int64, hash string, now time.Time, stale time.Duration) {
	if !n.Online {
		return
	}
	if n.State == "ERROR" {
		return
	}
	switch {
	case n.HasForkTip:
		n.State = "FORKED"
	case n.BlockHeight == height && n.BestBlockHash != hash:
		n.State = "DIVERGED"
	case n.BlockHeight < height:
		n.State = "SYNCING"
	case n.BlockHeight > height:
		n.State = "DIVERGED"
	case n.BestBlockHash == hash && n.LatestBlockTime > 0 && now.Sub(time.Unix(n.LatestBlockTime, 0)) > stale:
		n.State = "STALE"
	default:
		n.State = "HEALTHY"
	}
	n.HealthState = n.State
}

func economics(initial, interval, blockSeconds uint64, height int64) Economics {
	h := uint64(0)
	if height > 0 {
		h = uint64(height)
	}
	era := h / interval
	reward := uint64(0)
	if era < 64 {
		reward = initial >> era
	}
	next := (era + 1) * interval
	remaining := next - h
	return Economics{InitialRewardSats: initial, CurrentRewardSats: reward, HalvingInterval: interval, CurrentRewardEra: era, NextHalvingHeight: next, BlocksUntilHalving: remaining, EstimatedSecondsUntilHalving: remaining * blockSeconds}
}

func buildTopology(inv Inventory, nodes []NodeStatus) Topology {
	ipToID := map[string]string{}
	for _, s := range inv.Nodes {
		ipToID[s.NetworkIP] = s.ID
	}
	edges := map[string]Edge{}
	for _, n := range nodes {
		for _, addr := range n.PeerAddresses {
			host := strings.Trim(strings.Split(addr, ":")[0], "[]")
			if to := ipToID[host]; to != "" && to != n.ID {
				a, b := n.ID, to
				if a > b {
					a, b = b, a
				}
				edges[a+"|"+b] = Edge{a, b}
			}
		}
	}
	out := make([]Edge, 0, len(edges))
	for _, e := range edges {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From+out[i].To < out[j].From+out[j].To })
	return Topology{Nodes: nodes, Edges: out}
}

func readProducer(inv Inventory) ProducerStatus {
	p := ProducerStatus{Enabled: inv.AutoMine, BlockInterval: inv.BlockInterval}
	b, err := os.ReadFile(inv.ProducerStatusFile)
	if err == nil {
		_ = json.Unmarshal(b, &p)
		p.Enabled = inv.AutoMine
		p.BlockInterval = inv.BlockInterval
		if p.Running && p.NextScheduledAttempt != "" {
			if next, parseErr := time.Parse(time.RFC3339, p.NextScheduledAttempt); parseErr == nil && time.Now().After(next.Add(time.Duration(inv.BlockInterval)*time.Second)) {
				p.Running = false
				p.LastError = "producer status heartbeat expired"
			}
		}
	}
	return p
}
func sanitize(message string, c credentials) string {
	out := message
	for _, secret := range []string{c.user, c.password} {
		if secret != "" {
			out = strings.ReplaceAll(out, secret, "[redacted]")
		}
	}
	if len(out) > 240 {
		out = out[:240]
	}
	return out
}
func (p *poller) getSnapshot() Snapshot { p.mu.RLock(); defer p.mu.RUnlock(); return p.snapshot }
func (p *poller) nodeHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, n := range p.getSnapshot().Nodes {
		if n.ID == id {
			writeJSON(w, n)
			return
		}
	}
	http.Error(w, "unknown node ID", http.StatusNotFound)
}
