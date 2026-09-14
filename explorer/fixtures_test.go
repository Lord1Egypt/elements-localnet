package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// syntheticChain builds a deterministic, self-consistent block sequence so
// reorg handling can be exercised without touching the live network.
type syntheticChain struct {
	branch string
	blocks []rpcBlock
}

func hashFor(parts ...string) string {
	sum := sha256.Sum256([]byte(fmt.Sprint(parts)))
	return hex.EncodeToString(sum[:])
}

func number(v string) *json.Number {
	n := json.Number(v)
	return &n
}

const (
	testPolicyAsset = "6dd9e5bee5f86e1857389d1c8a0be8eeba4e463b902ec07224d6bf519b7321ee"
	testAddress     = "ert1qexploreraddressfixture00000000000000000"
	testSpendAddr   = "ert1qexplorerspendfixture0000000000000000000"
)

func newSyntheticChain(branch string) *syntheticChain {
	return &syntheticChain{branch: branch}
}

func (c *syntheticChain) tipHash() string {
	if len(c.blocks) == 0 {
		return ""
	}
	return c.blocks[len(c.blocks)-1].Hash
}

func (c *syntheticChain) height() int64 { return int64(len(c.blocks)) - 1 }

// extend appends one block whose coinbase pays testAddress. Extra transactions
// let a test add spends or issuances at a chosen height.
func (c *syntheticChain) extend(extra ...rpcTx) rpcBlock {
	height := int64(len(c.blocks))
	prev := c.tipHash()
	hash := hashFor("block", c.branch, fmt.Sprint(height), prev)
	coinbaseTxid := hashFor("coinbase", c.branch, fmt.Sprint(height))
	coinbase := rpcTx{
		Txid: coinbaseTxid, Wtxid: coinbaseTxid, Version: 2, Size: 245, Vsize: 215, Weight: 857,
		Vin: []rpcVin{{Coinbase: "0101", Sequence: 4294967295}},
		Vout: []rpcVout{{
			Value: number("50.00000000"), Asset: testPolicyAsset, N: 0,
			ScriptPubKey: rpcScriptOut{Hex: "0014aa", Asm: "0 aa", Address: testAddress, Type: "witness_v0_keyhash"},
		}},
	}
	block := rpcBlock{
		Hash: hash, Height: height, Version: 536870912, MerkleRoot: coinbaseTxid,
		Time: 1789000000 + height*60, MedianTime: 1789000000 + height*60,
		PreviousBlockHash: prev, StrippedSize: 284, Size: 325, Weight: 1177,
		Tx: append([]rpcTx{coinbase}, extra...),
	}
	block.NTx = int64(len(block.Tx))
	c.blocks = append(c.blocks, block)
	return block
}

func (c *syntheticChain) coinbaseTxid(height int64) string {
	return c.blocks[height].Tx[0].Txid
}

// spendTx consumes one earlier coinbase output and pays an explicit fee.
func spendTx(id string, prevTxid string, prevVout int64) rpcTx {
	vout := prevVout
	return rpcTx{
		Txid: id, Wtxid: id, Version: 2, Size: 300, Vsize: 200, Weight: 800,
		Vin: []rpcVin{{Txid: prevTxid, Vout: &vout, Sequence: 4294967293, ScriptSig: &rpcScriptIn{}}},
		Vout: []rpcVout{
			{Value: number("49.99900000"), Asset: testPolicyAsset, N: 0,
				ScriptPubKey: rpcScriptOut{Hex: "0014bb", Asm: "0 bb", Address: testSpendAddr, Type: "witness_v0_keyhash"}},
			{Value: number("0.00100000"), Asset: testPolicyAsset, N: 1,
				ScriptPubKey: rpcScriptOut{Type: "fee"}},
		},
		Fee: map[string]json.Number{testPolicyAsset: json.Number("0.00100000")},
	}
}

// issuanceTx creates a confidential issuance with a real entropy/asset/token
// triple so the stored identifiers are internally consistent.
func issuanceTx(id string, prevTxid string, prevVout int64) rpcTx {
	vout := prevVout
	entropy, err := AssetEntropy(prevTxid, uint32(prevVout), [32]byte{})
	if err != nil {
		panic(err)
	}
	assetID, err := AssetIDFromEntropy(entropy)
	if err != nil {
		panic(err)
	}
	token, err := ReissuanceTokenFromEntropy(entropy, true)
	if err != nil {
		panic(err)
	}
	return rpcTx{
		Txid: id, Wtxid: id, Version: 2, Size: 13272, Vsize: 3647, Weight: 14586,
		Vin: []rpcVin{{
			Txid: prevTxid, Vout: &vout, Sequence: 4294967293, ScriptSig: &rpcScriptIn{},
			Issuance: &rpcIssuance{
				AssetBlindingNonce:    "0000000000000000000000000000000000000000000000000000000000000000",
				AssetEntropy:          entropy,
				IsReissuance:          false,
				Token:                 token,
				Asset:                 assetID,
				AssetAmountCommitment: "08d1cfcc56e028d864120ebfeb3ecbd1172d1e456625ce418c1bbd69b0b9e8b877",
			},
		}},
		Vout: []rpcVout{
			{AssetCommitment: "0bfc182fe5105edb2cadac16e7c7c7764778f91cd84ab8c7f771d0b2c2796bd52b",
				ValueCommitment: "082b2309c1eec6e2e830b57f1370de9bbfdccf3b0b9718883e533a49401413602d",
				CommitmentNonce: "0227e2a956d15fedf91e3759c62145d02788dfa2b87df9dacd6860210f959ef140",
				N:               0,
				ScriptPubKey:    rpcScriptOut{Hex: "0014cc", Asm: "0 cc", Address: testSpendAddr, Type: "witness_v0_keyhash"}},
			{Value: number("0.00003647"), Asset: testPolicyAsset, N: 1, ScriptPubKey: rpcScriptOut{Type: "fee"}},
		},
		Fee: map[string]json.Number{testPolicyAsset: json.Number("0.00003647")},
	}
}

func testChainConfig() chainConfig {
	return chainConfig{SubsidySats: 5000000000, HalvingInterval: 210000, PolicyAsset: testPolicyAsset}
}

// fork returns a chain that shares every block up to and including upTo but
// produces different hashes for anything appended afterwards.
func (c *syntheticChain) fork(branch string, upTo int) *syntheticChain {
	forked := &syntheticChain{branch: branch}
	forked.blocks = append(forked.blocks, c.blocks[:upTo+1]...)
	return forked
}
