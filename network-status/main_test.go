package main

import (
	"testing"
	"time"
)

func online(id string, height int64, hash string) NodeStatus {
	return NodeStatus{ID: id, Online: true, State: "HEALTHY", BlockHeight: height, BestBlockHash: hash, LatestBlockTime: time.Now().Unix()}
}

func TestEqualHeightMismatchIsDiverged(t *testing.T) {
	nodes := []NodeStatus{online("node-01", 10, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), online("node-02", 10, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), online("node-03", 10, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")}
	h, hsh := canonical(nodes)
	if h != 10 || hsh != nodes[0].BestBlockHash {
		t.Fatalf("unexpected canonical chain: %d %s", h, hsh)
	}
	classify(&nodes[2], h, hsh, time.Now(), time.Hour)
	if nodes[2].State != "DIVERGED" {
		t.Fatalf("got %s", nodes[2].State)
	}
}

func TestClassificationPriority(t *testing.T) {
	n := online("node-02", 9, strings64("a"))
	classify(&n, 10, strings64("a"), time.Now(), time.Hour)
	if n.State != "SYNCING" {
		t.Fatalf("got %s", n.State)
	}
	n = online("node-02", 10, strings64("a"))
	n.HasForkTip = true
	classify(&n, 10, strings64("a"), time.Now(), time.Hour)
	if n.State != "FORKED" {
		t.Fatalf("got %s", n.State)
	}
	n = online("node-02", 10, strings64("a"))
	n.LatestBlockTime = time.Now().Add(-2 * time.Hour).Unix()
	classify(&n, 10, strings64("a"), time.Now(), time.Hour)
	if n.State != "STALE" {
		t.Fatalf("got %s", n.State)
	}
}

func TestEconomicsUsesIntegerSatoshis(t *testing.T) {
	e := economics(5_000_000_000, 210_000, 1, 210_000)
	if e.CurrentRewardSats != 2_500_000_000 || e.CurrentRewardEra != 1 || e.NextHalvingHeight != 420_000 || e.BlocksUntilHalving != 210_000 {
		t.Fatalf("unexpected economics: %+v", e)
	}
}

func TestAggregateDoesNotHideOfflineOrDisagreement(t *testing.T) {
	hashA := strings64("a")
	nodes := []NodeStatus{
		online("node-01", 12, hashA),
		online("node-02", 12, hashA),
		online("node-03", 12, strings64("b")),
		{ID: "node-04", State: "OFFLINE", HealthState: "OFFLINE", BlockHeight: -1},
	}
	inv := Inventory{NetworkName: "test", BlockRewardSats: 5_000_000_000, HalvingInterval: 210_000, StaleThresholdSeconds: 3600}
	s := aggregate(inv, nodes, time.Now())
	if s.Network.CanonicalHeight != 12 || s.Network.CanonicalBestHash != hashA {
		t.Fatalf("wrong canonical selection: %+v", s.Network)
	}
	if s.Network.OnlineNodes != 3 || s.Network.OfflineNodes != 1 || s.Network.DivergentNodes != 1 || s.Network.SynchronizedNodes != 2 {
		t.Fatalf("disagreement was hidden: %+v", s.Network)
	}
	if s.Nodes[2].State != "DIVERGED" || s.Nodes[3].State != "OFFLINE" {
		t.Fatalf("wrong states: %+v", s.Nodes)
	}
}

func strings64(s string) string {
	out := ""
	for i := 0; i < 64; i++ {
		out += s
	}
	return out
}
