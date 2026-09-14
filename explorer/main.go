package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed static/*
var staticFiles embed.FS

type config struct {
	inventoryFile string
	databasePath  string
	secretsDir    string
	primaryNode   string
	registryFile  string
	listenAddr    string
	batchBlocks   int
	pollInterval  time.Duration
	rpcTimeout    time.Duration
}

func loadConfig() config {
	return config{
		inventoryFile: getenv("INVENTORY_FILE", "/run/localnet/inventory.json"),
		databasePath:  getenv("EXPLORER_DB_PATH", "/var/lib/elements-explorer/explorer.db"),
		secretsDir:    getenv("EXPLORER_SECRETS_DIR", "/run/explorer-secrets"),
		primaryNode:   getenv("EXPLORER_PRIMARY_NODE", "node-02"),
		registryFile:  getenv("ASSET_REGISTRY_FILE", "/run/localnet/assets.json"),
		listenAddr:    getenv("EXPLORER_LISTEN_ADDR", ":8080"),
		batchBlocks:   getenvInt("EXPLORER_BATCH_BLOCKS", 100, 1, 1000),
		pollInterval:  time.Duration(getenvInt("EXPLORER_POLL_SECONDS", 5, 1, 3600)) * time.Second,
		rpcTimeout:    time.Duration(getenvInt("EXPLORER_RPC_TIMEOUT_SECONDS", 60, 5, 600)) * time.Second,
	}
}

func getenvInt(key string, fallback, minimum, maximum int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		log.Printf("configuration: %s must be between %d and %d; using %d", key, minimum, maximum, fallback)
		return fallback
	}
	return value
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "-healthcheck" {
		runHealthcheck()
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "-backup" {
		runBackup(os.Args[2])
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "-remove-backup" {
		runRemoveBackup(os.Args[2])
		return
	}
	cfg := loadConfig()

	data, err := os.ReadFile(cfg.inventoryFile)
	if err != nil {
		log.Fatalf("inventory: %v", err)
	}
	var inv Inventory
	if err := json.Unmarshal(data, &inv); err != nil {
		log.Fatalf("inventory: %v", err)
	}
	if err := validateInventory(inv); err != nil {
		log.Fatalf("inventory: %v", err)
	}

	db, err := openStore(cfg.databasePath)
	if err != nil {
		log.Fatalf("explorer database: %v", err)
	}
	defer db.Close()

	pool, err := newRPCPool(inv, cfg.secretsDir, cfg.primaryNode, cfg.rpcTimeout)
	if err != nil {
		log.Fatalf("explorer RPC: %v", err)
	}

	registry, err := loadAssetRegistry(cfg.registryFile)
	if err != nil {
		log.Printf("asset registry: %v (continuing without metadata)", err)
	}

	chain := chainConfig{SubsidySats: inv.BlockRewardSats, HalvingInterval: inv.HalvingInterval}

	telemetry := &poller{inv: inv, client: &http.Client{Timeout: time.Duration(inv.RPCTimeoutMillis) * time.Millisecond}}
	telemetry.refresh(context.Background())

	ix := newIndexer(db, pool, chain, cfg.primaryNode, cfg.batchBlocks, cfg.pollInterval, cfg.rpcTimeout)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go telemetry.run(ctx)
	go ix.Run(ctx)

	api := &server{poller: telemetry, store: db, indexer: ix, registry: registry, rpc: pool, inv: inv}
	web, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("embedded assets: %v", err)
	}
	mux := http.NewServeMux()
	api.routes(mux, http.FileServer(http.FS(web)))

	server := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           securityHeaders(singlePageRouter(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Printf("explorer listening on %s for %d configured nodes (schema v%d)", cfg.listenAddr, len(inv.Nodes), currentSchemaVersion)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Print("explorer: shutting down")
	cancel()
	shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	_ = server.Shutdown(shutdownCtx)
	db.Close()
}

func runHealthcheck() {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:8080/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
	_ = resp.Body.Close()
}

// backupPath accepts only a backup file directly inside the explorer database
// directory, so this mode can never delete chain data or the index itself.
func backupPath(destination string) (string, error) {
	cleaned := filepath.Clean(destination)
	directory := filepath.Dir(loadConfig().databasePath)
	if filepath.Dir(cleaned) != directory {
		return "", fmt.Errorf("a backup must live directly in %s", directory)
	}
	name := filepath.Base(cleaned)
	if !strings.HasPrefix(name, "backup-") || !strings.HasSuffix(name, ".db") {
		return "", errors.New("a backup file must be named backup-<stamp>.db")
	}
	return cleaned, nil
}

func runRemoveBackup(destination string) {
	path, err := backupPath(destination)
	if err != nil {
		log.Fatalf("backup: %v", err)
	}
	if err := os.Remove(path); err != nil {
		log.Fatalf("backup: could not remove the temporary copy")
	}
}

// runBackup writes a consistent copy of the explorer index with SQLite's
// VACUUM INTO, which is safe while the indexer is writing in WAL mode.
func runBackup(destination string) {
	path, err := backupPath(destination)
	if err != nil {
		log.Fatalf("backup: %v", err)
	}
	cfg := loadConfig()
	db, err := sql.Open("sqlite", "file:"+cfg.databasePath+"?_pragma=busy_timeout(30000)")
	if err != nil {
		log.Fatalf("backup: %v", err)
	}
	defer db.Close()
	if _, err := os.Stat(path); err == nil {
		log.Fatal("backup: the destination already exists")
	}
	if _, err := db.Exec("VACUUM INTO ?", path); err != nil {
		log.Fatalf("backup: %v", err)
	}
	log.Printf("backup: wrote %s", path)
}

// singlePageRouter lets deep links such as /block/<hash> reach the embedded
// application shell instead of the static file server's 404.
func singlePageRouter(next http.Handler) http.Handler {
	shell, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		log.Fatalf("embedded shell: %v", err)
	}
	appRoutes := map[string]bool{
		"block": true, "tx": true, "address": true, "asset": true,
		"blocks": true, "transactions": true, "assets": true, "mempool": true, "nodes": true, "search": true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if len(path) > 1 {
			segment := path[1:]
			if slash := indexByte(segment, '/'); slash >= 0 {
				segment = segment[:slash]
			}
			if appRoutes[segment] {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				_, _ = w.Write(shell)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
