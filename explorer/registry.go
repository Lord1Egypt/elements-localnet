package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// AssetMetadata is descriptive, operator-supplied labelling. It is never
// consensus data and must always be presented as such.
type AssetMetadata struct {
	AssetID     string `json:"assetId"`
	Name        string `json:"name,omitempty"`
	Ticker      string `json:"ticker,omitempty"`
	Description string `json:"description,omitempty"`
	Decimals    *int   `json:"decimals,omitempty"`
	LogoPath    string `json:"logoPath,omitempty"`
	Website     string `json:"website,omitempty"`
	Status      string `json:"status,omitempty"`
}

type assetRegistryFile struct {
	Version int             `json:"version"`
	Assets  []AssetMetadata `json:"assets"`
}

type assetRegistry struct {
	byID map[string]AssetMetadata
}

const (
	maxRegistryBytes = 1 << 20
	maxRegistryField = 200
	maxRegistryDesc  = 1000
)

func loadAssetRegistry(path string) (*assetRegistry, error) {
	registry := &assetRegistry{byID: map[string]AssetMetadata{}}
	if path == "" {
		return registry, nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return registry, err
	}
	if info.Size() > maxRegistryBytes {
		return registry, fmt.Errorf("asset registry is larger than %d bytes", maxRegistryBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return registry, err
	}
	var file assetRegistryFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return registry, errors.New("asset registry is not valid JSON")
	}
	for _, entry := range file.Assets {
		id := strings.ToLower(strings.TrimSpace(entry.AssetID))
		if len(id) != 64 || !isHex(id) {
			continue
		}
		entry.AssetID = id
		entry.Name = clamp(entry.Name, maxRegistryField)
		entry.Ticker = clamp(entry.Ticker, 16)
		entry.Description = clamp(entry.Description, maxRegistryDesc)
		entry.LogoPath = clamp(entry.LogoPath, maxRegistryField)
		entry.Website = clamp(entry.Website, maxRegistryField)
		entry.Status = clamp(entry.Status, 32)
		registry.byID[id] = entry
	}
	return registry, nil
}

func (r *assetRegistry) lookup(assetID string) *AssetMetadata {
	if r == nil {
		return nil
	}
	if entry, ok := r.byID[strings.ToLower(assetID)]; ok {
		copied := entry
		return &copied
	}
	return nil
}

func clamp(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) > limit {
		return s[:limit]
	}
	return s
}
