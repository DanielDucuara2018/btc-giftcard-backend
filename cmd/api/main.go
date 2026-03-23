// Package main implements the HTTP API server for btc-giftcard.
//
// The server exposes card CRUD, redemption, treasury balance, and health
// check endpoints. Infrastructure (Redis, PostgreSQL, LND) is initialised
// at startup; the server shuts down gracefully on SIGINT/SIGTERM.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"btc-giftcard/config"
	"btc-giftcard/internal/card"
	"btc-giftcard/internal/database"
	"btc-giftcard/internal/lnd"
	"btc-giftcard/pkg/cache"
	"btc-giftcard/pkg/logger"
	streams "btc-giftcard/pkg/queue"

	"github.com/jinzhu/copier"
	"go.uber.org/zap"
)

// Cfg holds the application configuration loaded from config.toml.
var Cfg config.ApiConfig

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// run orchestrates the server lifecycle: init → serve → shutdown.
//
// Each infrastructure dependency is initialised by a dedicated function.
// The HTTP server runs in a goroutine; the main goroutine blocks on OS signal.
func run() error {
	if err := initLogger(); err != nil {
		return err
	}
	defer logger.Sync()

	if err := loadConfig(); err != nil {
		return err
	}

	if err := initRedis(); err != nil {
		return err
	}
	defer cache.Close()

	db, err := initDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()

	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}
	if err := db.RunMigrations(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	lndClient, err := initLND(ctx)
	if err != nil {
		return err
	}
	defer lndClient.Close()

	// Wire dependencies
	cardRepo := database.NewCardRepository(db)
	txRepo := database.NewTransactionRepository(db)
	queue := streams.NewStreamQueue(cache.Client)
	cardService := card.NewService(db, cardRepo, txRepo, Cfg.LND.Network, queue, lndClient)

	srv := newServer(cardService, lndClient)

	go func() {
		logger.Info("API server starting", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed", zap.Error(err))
		}
	}()

	awaitShutdown(srv)
	return nil
}

// ============================================================================
// Infrastructure Init
// ============================================================================

// initLogger sets up the structured logger.
func initLogger() error {
	if err := logger.Init(logger.GetEnv()); err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	return nil
}

// loadConfig reads config.toml from the repo root.
//
// Resolution order:
//  1. CONFIG_FILE env var — used by Docker containers (e.g. /app/config.toml)
//  2. Path relative to this source file — used by `go run` in local development
func loadConfig() error {
	path := os.Getenv("CONFIG_FILE")
	if path == "" {
		_, filename, _, _ := runtime.Caller(0)
		root := filepath.Dir(filename)
		path = string(config.Path(root).Join("..", "..", "config.toml"))
	}
	if err := config.Load(config.Path(path), &Cfg); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return nil
}

// initRedis initializes the global Redis client used by cache and queue.
func initRedis() error {
	var redisCfg cache.Config
	if err := copier.Copy(&redisCfg, &Cfg.Redis); err != nil {
		return fmt.Errorf("failed to copy cache config: %w", err)
	}
	if err := cache.Init(redisCfg); err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	return nil
}

// initDatabase opens a PostgreSQL connection pool.
func initDatabase() (*database.DB, error) {
	var dbCfg database.Config
	if err := copier.Copy(&dbCfg, &Cfg.Database); err != nil {
		return nil, fmt.Errorf("failed to copy database config: %w", err)
	}
	db, err := database.NewDB(dbCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database connection: %w", err)
	}
	return db, nil
}

// initLND connects to the LND node and verifies it is synced to chain.
func initLND(ctx context.Context) (*lnd.Client, error) {
	var lndCfg lnd.Config
	if err := copier.Copy(&lndCfg, &Cfg.LND); err != nil {
		return nil, fmt.Errorf("failed to copy LND config: %w", err)
	}
	lndClient, err := lnd.NewClient(lndCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to LND: %w", err)
	}

	info, err := lndClient.GetInfo(ctx)
	if err != nil {
		lndClient.Close()
		return nil, fmt.Errorf("failed to get LND info: %w", err)
	}
	logger.Info("Connected to LND",
		zap.String("alias", info.Alias),
		zap.Bool("synced", info.SyncedToChain),
		zap.Uint32("block_height", info.BlockHeight),
	)
	return lndClient, nil
}

// ============================================================================
// Server Lifecycle
// ============================================================================

// newServer creates the HTTP server with sensible timeouts.
// TODO: Make port configurable via config.toml [api] section.
func newServer(cardService *card.Service, lndClient *lnd.Client) *http.Server {
	h := newHandler(cardService, lndClient)
	return &http.Server{
		Addr:         ":3202",
		Handler:      h.routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// awaitShutdown blocks until SIGINT or SIGTERM, then gracefully drains
// in-flight requests with a 10 s deadline.
func awaitShutdown(srv *http.Server) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	logger.Info("Received shutdown signal", zap.String("signal", sig.String()))

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shutdown failed", zap.Error(err))
	}
	logger.Info("API server shut down gracefully")
}
