package main

import (
	"encoding/json"
	"errors"
	"math/big"
)

// Elements RPC reports amounts as decimal JSON numbers and sometimes in
// scientific notation (3.647e-05). Parsing through float64 would be lossy for
// large explicit supplies, so amounts are converted exactly with big.Rat.
var satsPerUnit = big.NewRat(100000000, 1)

func parseSats(n json.Number) (int64, error) {
	rat, ok := new(big.Rat).SetString(n.String())
	if !ok {
		return 0, errors.New("amount is not a decimal number")
	}
	rat.Mul(rat, satsPerUnit)
	if !rat.IsInt() {
		return 0, errors.New("amount has sub-satoshi precision")
	}
	value := rat.Num()
	if !value.IsInt64() {
		return 0, errors.New("amount does not fit in an int64")
	}
	return value.Int64(), nil
}

type rpcChainInfo struct {
	Chain         string `json:"chain"`
	Blocks        int64  `json:"blocks"`
	Headers       int64  `json:"headers"`
	BestBlockHash string `json:"bestblockhash"`
}

type rpcBlock struct {
	Hash              string  `json:"hash"`
	Height            int64   `json:"height"`
	Version           int64   `json:"version"`
	MerkleRoot        string  `json:"merkleroot"`
	Time              int64   `json:"time"`
	MedianTime        int64   `json:"mediantime"`
	NTx               int64   `json:"nTx"`
	PreviousBlockHash string  `json:"previousblockhash"`
	NextBlockHash     string  `json:"nextblockhash"`
	StrippedSize      int64   `json:"strippedsize"`
	Size              int64   `json:"size"`
	Weight            int64   `json:"weight"`
	SignblockChalleng string  `json:"signblock_challenge"`
	Tx                []rpcTx `json:"tx"`
}

type rpcTx struct {
	Txid     string                 `json:"txid"`
	Hash     string                 `json:"hash"`
	Wtxid    string                 `json:"wtxid"`
	WitHash  string                 `json:"withash"`
	Version  int64                  `json:"version"`
	Size     int64                  `json:"size"`
	Vsize    int64                  `json:"vsize"`
	Weight   int64                  `json:"weight"`
	Locktime int64                  `json:"locktime"`
	Vin      []rpcVin               `json:"vin"`
	Vout     []rpcVout              `json:"vout"`
	Fee      map[string]json.Number `json:"fee"`
}

type rpcVin struct {
	Txid         string       `json:"txid"`
	Vout         *int64       `json:"vout"`
	Coinbase     string       `json:"coinbase"`
	IsPegin      bool         `json:"is_pegin"`
	Sequence     int64        `json:"sequence"`
	ScriptSig    *rpcScriptIn `json:"scriptSig"`
	Issuance     *rpcIssuance `json:"issuance"`
	PeginWitness []string     `json:"pegin_witness"`
}

type rpcScriptIn struct {
	Hex string `json:"hex"`
}

type rpcIssuance struct {
	AssetBlindingNonce    string       `json:"assetBlindingNonce"`
	AssetEntropy          string       `json:"assetEntropy"`
	IsReissuance          bool         `json:"isreissuance"`
	Token                 string       `json:"token"`
	Asset                 string       `json:"asset"`
	AssetAmount           *json.Number `json:"assetamount"`
	AssetAmountCommitment string       `json:"assetamountcommitment"`
	TokenAmount           *json.Number `json:"tokenamount"`
	TokenAmountCommitment string       `json:"tokenamountcommitment"`
}

type rpcVout struct {
	Value           *json.Number `json:"value"`
	ValueCommitment string       `json:"valuecommitment"`
	Asset           string       `json:"asset"`
	AssetCommitment string       `json:"assetcommitment"`
	CommitmentNonce string       `json:"commitmentnonce"`
	N               int64        `json:"n"`
	ScriptPubKey    rpcScriptOut `json:"scriptPubKey"`
}

type rpcScriptOut struct {
	Asm           string   `json:"asm"`
	Hex           string   `json:"hex"`
	Address       string   `json:"address"`
	Addresses     []string `json:"addresses"`
	Type          string   `json:"type"`
	PegoutChain   string   `json:"pegout_chain"`
	PegoutAddress string   `json:"pegout_address"`
	PegoutType    string   `json:"pegout_type"`
	PegoutAsm     string   `json:"pegout_asm"`
}

func (s rpcScriptOut) address() string {
	if s.Address != "" {
		return s.Address
	}
	if len(s.Addresses) == 1 {
		return s.Addresses[0]
	}
	return ""
}

func (v rpcVout) isFee() bool { return v.ScriptPubKey.Type == "fee" }

type rpcMempoolEntry struct {
	Vsize           int64                  `json:"vsize"`
	Weight          int64                  `json:"weight"`
	Time            int64                  `json:"time"`
	Height          int64                  `json:"height"`
	DescendantCount int64                  `json:"descendantcount"`
	Fees            map[string]json.Number `json:"fees"`
}

// blockSubsidy mirrors the generated con_blocksubsidy / halving configuration.
func blockSubsidy(initial, halvingInterval uint64, height int64) int64 {
	if height <= 0 || halvingInterval == 0 {
		return 0
	}
	era := uint64(height) / halvingInterval
	if era >= 64 {
		return 0
	}
	return int64(initial >> era)
}
