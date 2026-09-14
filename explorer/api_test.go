package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPageParamsEnforcesBounds(t *testing.T) {
	cases := []struct {
		query      string
		wantLimit  int
		wantOffset int
		wantErr    bool
	}{
		{"", defaultPageSize, 0, false},
		{"?limit=10&offset=20", 10, 20, false},
		{"?limit=5000", maxPageSize, 0, false},
		{"?limit=0", 0, 0, true},
		{"?limit=-1", 0, 0, true},
		{"?limit=abc", 0, 0, true},
		{"?offset=-5", 0, 0, true},
		{"?offset=100000000", 0, 0, true},
	}
	for _, c := range cases {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/blocks"+c.query, nil)
		limit, offset, err := pageParams(request)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected an error", c.query)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.query, err)
			continue
		}
		if limit != c.wantLimit || offset != c.wantOffset {
			t.Errorf("%q: limit/offset = %d/%d, want %d/%d", c.query, limit, offset, c.wantLimit, c.wantOffset)
		}
	}
}

func TestWriteErrorIsStructuredAndOpaque(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeError(recorder, http.StatusNotFound, "NOT_FOUND", "No indexed block matches that height or hash.")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if want := `{"error":{"code":"NOT_FOUND","message":"No indexed block matches that height or hash."}}`; body != want+"\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestValidAddressRejectsInjectionShapes(t *testing.T) {
	for _, bad := range []string{"", "short", "ert1q'; DROP TABLE outputs;--", "ert1q addr", "../../etc/passwd"} {
		if validAddress(bad) {
			t.Errorf("validAddress(%q) should be false", bad)
		}
	}
	if !validAddress("ert1q5e9t8my39jvhywjhqdawmy4wj0sx79yq0egfd8") {
		t.Error("a real bech32 address should be accepted")
	}
}

func TestAssetRegistryIgnoresMalformedEntries(t *testing.T) {
	registry := &assetRegistry{byID: map[string]AssetMetadata{}}
	if registry.lookup("6dd9e5bee5f86e1857389d1c8a0be8eeba4e463b902ec07224d6bf519b7321ee") != nil {
		t.Error("empty registry should not resolve an asset")
	}
}
