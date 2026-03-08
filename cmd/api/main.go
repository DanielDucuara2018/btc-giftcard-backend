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

var Cfg config.ApiConfig

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Initialize logger
	if err := logger.Init(logger.GetEnv()); err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer logger.Sync()

	// Load configuration
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filename)
	configPath := config.Path(root).Join("config.toml", "..", "..")

	if err := config.Load(configPath, &Cfg); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize Redis cache
	var redisCfg cache.Config
	if err := copier.Copy(&redisCfg, &Cfg.Redis); err != nil {
		return fmt.Errorf("failed to copy cache config: %w", err)
	}
	if err := cache.Init(redisCfg); err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	defer cache.Close()

	// Initialize database
	var dbCfg database.Config
	if err := copier.Copy(&dbCfg, &Cfg.Database); err != nil {
		return fmt.Errorf("failed to copy database config: %w", err)
	}
	db, err := database.NewDB(dbCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize database connection: %w", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Verify database connection
	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	// Run migrations
	if err := db.RunMigrations(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Initialize LND client
	var lndCfg lnd.Config
	if err := copier.Copy(&lndCfg, &Cfg.LND); err != nil {
		return fmt.Errorf("failed to copy LND config: %w", err)
	}
	lndClient, err := lnd.NewClient(lndCfg)
	if err != nil {
		return fmt.Errorf("failed to connect to LND: %w", err)
	}
	defer lndClient.Close()

	// Verify LND connection
	info, err := lndClient.GetInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get LND info: %w", err)
	}
	logger.Info("Connected to LND",
		zap.String("alias", info.Alias),
		zap.Bool("synced", info.SyncedToChain),
		zap.Uint32("block_height", info.BlockHeight),
	)

	// Create repositories and services
	cardRepo := database.NewCardRepository(db)
	txRepo := database.NewTransactionRepository(db)
	queue := streams.NewStreamQueue(cache.Client)
	cardService := card.NewService(cardRepo, txRepo, lndCfg.Network, queue, lndClient)

	// Build HTTP handler
	handler := newHandler(cardService)

	// Create HTTP server
	// TODO: Make port configurable via config.toml [api] section
	srv := &http.Server{
		Addr:         ":8080",
		Handler:      handler.routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		logger.Info("API server starting", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed", zap.Error(err))
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	logger.Info("Received shutdown signal", zap.String("signal", sig.String()))

	// Graceful shutdown with 10s deadline
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown failed: %w", err)
	}

	logger.Info("API server shut down gracefully")
	return nil
}
