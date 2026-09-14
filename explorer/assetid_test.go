package main

import "testing"

// Fixtures captured from this network with `getblock <hash> 2`. Every issuance
// below is a new issuance with an all-zero contract hash, so the entropy must be
// derivable from the spent outpoint alone.
type issuanceFixture struct {
	name         string
	prevTxid     string
	prevVout     uint32
	entropy      string
	assetID      string
	tokenID      string
	confidential bool
}

var knownIssuances = []issuanceFixture{
	{
		name:         "confidential-8071f132",
		prevTxid:     "b540adad579e258cc7926daabdd40432882c00b01733ad51ec2505e1f2751397",
		prevVout:     0,
		entropy:      "2636b11d77c3a80553058b150afea5cbb2e00a938a4e883d160080fee5591f5b",
		assetID:      "8071f132279fd8765b89d9ac8a87a735ca753ea7f0f5956dba6413d9a38ea2a8",
		tokenID:      "739790dd8b6f77911a7d453b04d5ed9823a84ea545017787de4a88258aebd1bd",
		confidential: true,
	},
	{
		name:         "confidential-4b1c0cf7",
		prevTxid:     "e51fab81c32faf8c3e1bafa469864bb1ad48f7501fe494fb99b3a6486066004c",
		prevVout:     0,
		entropy:      "5dcac47d98be27eb0d8f2ca7011f5bb7c1d12fc42c962b497167e468c38ee0aa",
		assetID:      "4b1c0cf75639b61f486b1057d11fd4b8155c5cddf8058badfc7dff36a5502aab",
		tokenID:      "28cdfbb5e3fcfce1a52eb237610ac44b39579a8015d588afb0dc39efccf142da",
		confidential: true,
	},
	{
		name:         "confidential-525cac3b",
		prevTxid:     "16342a649a374fa7b46347b60296d3b3139ae3178ef41472905c7982eea6e5db",
		prevVout:     0,
		entropy:      "d6a569e7e04d8756949e394ccbfa7d28c09ba82fd4361c61807f243f4fb03c2c",
		assetID:      "525cac3bdad6dffaf99d2dec79e9659ecf68b11d618cf27c8bf91d406e235d05",
		tokenID:      "b9e7182ad6b882adc6fa9d217f428ff29fe1d57609531e8534e9c031a8b18c15",
		confidential: true,
	},
	{
		name:         "explicit-6d177c35",
		prevTxid:     "7824934aee2e9197ddf0ee6746a031c94efeedc08ff744c0b055f1e3cefc0bdc",
		prevVout:     0,
		entropy:      "fa21212da00d0deb6e5854f66f3705687740867568445621b7698f78cf395e67",
		assetID:      "6d177c3509ad78f34455de95a6815e7f1f57f512f0bc257559953606382e96b1",
		tokenID:      "c780d81eee3b052a92c8289e694897c41652a8dff984a32edf68f60a53033e38",
		confidential: false,
	},
	{
		name:         "explicit-72f6aa6d",
		prevTxid:     "631d6fb0802330ebb6835f20723e4dee5bb7a88681b0f7ad300a829c3a37ceda",
		prevVout:     0,
		entropy:      "5a7a48e589bb194358aed2c1091a7ece706d6741a2198a47fadf24a7d729ab4c",
		assetID:      "72f6aa6d36504910cac7f8af1fadb2ad2b119a32777cc1e43a4fd1d134d42212",
		tokenID:      "ed8aae7118754e7a38727f0dd2b5fca24b72431a12d924dfd4c612e8f5c1864a",
		confidential: false,
	},
}

func TestAssetEntropyMatchesChain(t *testing.T) {
	var zero [32]byte
	for _, fixture := range knownIssuances {
		got, err := AssetEntropy(fixture.prevTxid, fixture.prevVout, zero)
		if err != nil {
			t.Fatalf("%s: %v", fixture.name, err)
		}
		if got != fixture.entropy {
			t.Errorf("%s entropy = %s, want %s", fixture.name, got, fixture.entropy)
		}
	}
}

func TestAssetIDMatchesChain(t *testing.T) {
	for _, fixture := range knownIssuances {
		got, err := AssetIDFromEntropy(fixture.entropy)
		if err != nil {
			t.Fatalf("%s: %v", fixture.name, err)
		}
		if got != fixture.assetID {
			t.Errorf("%s asset = %s, want %s", fixture.name, got, fixture.assetID)
		}
	}
}

func TestReissuanceTokenMatchesChain(t *testing.T) {
	for _, fixture := range knownIssuances {
		got, err := ReissuanceTokenFromEntropy(fixture.entropy, fixture.confidential)
		if err != nil {
			t.Fatalf("%s: %v", fixture.name, err)
		}
		if got != fixture.tokenID {
			t.Errorf("%s token = %s, want %s", fixture.name, got, fixture.tokenID)
		}
		other, err := ReissuanceTokenFromEntropy(fixture.entropy, !fixture.confidential)
		if err != nil {
			t.Fatalf("%s: %v", fixture.name, err)
		}
		if other == fixture.tokenID {
			t.Errorf("%s: confidential flag did not change the token tag", fixture.name)
		}
	}
}

func TestReverseHexRejectsBadInput(t *testing.T) {
	if _, err := reverseHex("not-hex"); err == nil {
		t.Error("expected an error for non-hexadecimal input")
	}
	if _, err := reverseHex("00ff"); err == nil {
		t.Error("expected an error for a short identifier")
	}
}
