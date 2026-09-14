package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

// Elements derives asset and reissuance-token identifiers with the "fast" merkle
// hash: one raw SHA-256 compression of the 64-byte concatenation of two 32-byte
// leaves, without length padding. Upstream calls it ComputeFastMerkleRoot; it is
// not the same as SHA256(a||b), so the compression function is implemented here.
var sha256IV = [8]uint32{
	0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
	0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
}

var sha256K = [64]uint32{
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}

func rotr(x uint32, n uint) uint32 { return x>>n | x<<(32-n) }

// fastMerkleHash returns the single-compression SHA-256 midstate over left||right.
func fastMerkleHash(left, right [32]byte) [32]byte {
	var block [64]byte
	copy(block[:32], left[:])
	copy(block[32:], right[:])
	var w [64]uint32
	for i := 0; i < 16; i++ {
		w[i] = binary.BigEndian.Uint32(block[i*4 : i*4+4])
	}
	for i := 16; i < 64; i++ {
		s0 := rotr(w[i-15], 7) ^ rotr(w[i-15], 18) ^ (w[i-15] >> 3)
		s1 := rotr(w[i-2], 17) ^ rotr(w[i-2], 19) ^ (w[i-2] >> 10)
		w[i] = w[i-16] + s0 + w[i-7] + s1
	}
	a, b, c, d, e, f, g, h := sha256IV[0], sha256IV[1], sha256IV[2], sha256IV[3], sha256IV[4], sha256IV[5], sha256IV[6], sha256IV[7]
	for i := 0; i < 64; i++ {
		s1 := rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)
		ch := (e & f) ^ (^e & g)
		t1 := h + s1 + ch + sha256K[i] + w[i]
		s0 := rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)
		maj := (a & b) ^ (a & c) ^ (b & c)
		t2 := s0 + maj
		h, g, f, e, d, c, b, a = g, f, e, d+t1, c, b, a, t1+t2
	}
	state := [8]uint32{sha256IV[0] + a, sha256IV[1] + b, sha256IV[2] + c, sha256IV[3] + d,
		sha256IV[4] + e, sha256IV[5] + f, sha256IV[6] + g, sha256IV[7] + h}
	var out [32]byte
	for i, v := range state {
		binary.BigEndian.PutUint32(out[i*4:i*4+4], v)
	}
	return out
}

func doubleSHA256(b []byte) [32]byte {
	first := sha256.Sum256(b)
	return sha256.Sum256(first[:])
}

// reverseHex converts between a displayed uint256 hex string and its internal
// little-endian byte order. Elements prints every identifier reversed.
func reverseHex(s string) ([32]byte, error) {
	var out [32]byte
	raw, err := hex.DecodeString(s)
	if err != nil {
		return out, err
	}
	if len(raw) != 32 {
		return out, errors.New("identifier must be 32 bytes")
	}
	for i := 0; i < 32; i++ {
		out[i] = raw[31-i]
	}
	return out, nil
}

func displayHex(b [32]byte) string {
	var flipped [32]byte
	for i := 0; i < 32; i++ {
		flipped[i] = b[31-i]
	}
	return hex.EncodeToString(flipped[:])
}

// AssetEntropy reproduces GenerateAssetEntropy(prevout, contractHash).
func AssetEntropy(prevTxid string, prevVout uint32, contractHash [32]byte) (string, error) {
	txid, err := reverseHex(prevTxid)
	if err != nil {
		return "", err
	}
	serialized := make([]byte, 36)
	copy(serialized[:32], txid[:])
	binary.LittleEndian.PutUint32(serialized[32:], prevVout)
	return displayHex(fastMerkleHash(doubleSHA256(serialized), contractHash)), nil
}

// AssetIDFromEntropy reproduces CalculateAsset(entropy).
func AssetIDFromEntropy(entropy string) (string, error) {
	e, err := reverseHex(entropy)
	if err != nil {
		return "", err
	}
	var zero [32]byte
	return displayHex(fastMerkleHash(e, zero)), nil
}

// ReissuanceTokenFromEntropy reproduces CalculateReissuanceToken(entropy, confidential).
func ReissuanceTokenFromEntropy(entropy string, confidential bool) (string, error) {
	e, err := reverseHex(entropy)
	if err != nil {
		return "", err
	}
	var tag [32]byte
	if confidential {
		tag[0] = 2
	} else {
		tag[0] = 1
	}
	return displayHex(fastMerkleHash(e, tag)), nil
}
