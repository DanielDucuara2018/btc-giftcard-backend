// Package main implements the fund_card background worker.
//
// # Custodial Funding Model
//
// This worker processes FundCardMessage from the "fund_card" Redis stream.
// Funding is pure accounting — no blockchain transaction happens here.
//
// BTC is pre-purchased via OTC (Crypto.com OTC 2.0) and held in treasury
// (Lightning channels + on-chain hot wallet). Cards are balance claims.
//
// Flow:
//
//  1. API creates card (Status=Created, BTCAmountSats=0)
//  2. API publishes FundCardMessage to "fund_card" Redis stream
//  3. This worker: fetch BTC price → calculate sats → check treasury → activate card
//  4. Card is now active and spendable by the user
//
// No on-chain tx, no wallet generation, no private keys.
// BTC only moves when user redeems (Lightning or on-chain).
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
	"btc-giftcard/internal/exchange"
	"btc-giftcard/internal/lnd"
	messages "btc-giftcard/internal/queue"
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

// run orchestrates the worker lifecycle: init → consume → shutdown.
//
// Each initialization step is a dedicated function for clarity.
// The consumer runs in a goroutine; the main goroutine blocks on OS signal.
func run() error {
	if err := initLogger(); err != nil {
		return err
	}
	defer logger.Sync()

	if err := loadConfig(); err != nil {
		return err
	}
	logger.Info("Starting fund_card worker...")

	if err := initRedis(); err != nil {
		return err
	}
	defer cache.Close()

	db, err := initDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	provider, err := initExchangeProvider("cryptocom", "", nil)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	handler := newMessageHandler(cardService, provider)

	if err := startConsumer(ctx, queue, handler); err != nil {
		return err
	}

	awaitShutdown(cancel)
	return nil
}

// ============================================================================
// Infrastructure Init
// ============================================================================

// initLogger sets up the structured logger for the worker.
func initLogger() error {
	if err := logger.Init("development"); err != nil {
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
		path = string(config.Path(root).Join("..", "..", "..", "config.toml"))
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

// initDatabase opens a PostgreSQL connection pool and verifies connectivity.
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

// initExchangeProvider creates the BTC/fiat price provider.
// Currently uses Coinbase; will switch to Crypto.com OTC once implemented.
func initExchangeProvider(providerName string, baseURL string, httpClient *http.Client) (exchange.PriceProvider, error) {
	// TODO: Switch to "cryptocom_otc" provider once implemented
	provider, err := exchange.NewProvider(providerName, baseURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize exchange provider: %w", err)
	}
	return provider, nil
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
// Consumer Lifecycle
// ============================================================================

// startConsumer declares the Redis stream consumer group and launches the
// blocking consume loop in a goroutine.
func startConsumer(ctx context.Context, queue *streams.StreamQueue, handler *messageHandler) error {
	streamName := "fund_card"
	groupName := "fund_workers"
	consumerName := fmt.Sprintf("fund-worker-%d", time.Now().Unix())

	if err := queue.DeclareStream(ctx, streamName, groupName); err != nil {
		return fmt.Errorf("failed to declare the consumer group: %w", err)
	}

	go func() {
		err := queue.Consume(ctx, streamName, groupName, consumerName,
			func(messageID string, data []byte) error {
				return handler.processMessage(ctx, messageID, data)
			})
		if err != nil && err != context.Canceled {
			logger.Error("Consumer error", zap.Error(err))
		}
	}()

	logger.Info("Fund card worker is running, waiting for messages...",
		zap.String("stream", streamName),
		zap.String("group", groupName),
		zap.String("consumer", consumerName),
	)
	return nil
}

// awaitShutdown blocks until SIGINT or SIGTERM, cancels the context, then
// waits briefly for in-flight messages to finish processing.
func awaitShutdown(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	logger.Info("Received shutdown signal", zap.String("signal", sig.String()))

	cancel()
	time.Sleep(3 * time.Second)
	logger.Info("Fund card worker shut down gracefully")
}

// ============================================================================
// Message Processing
// ============================================================================

// messageHandler holds the dependencies needed by processMessage.
// The worker is a thin adapter: parse message → fetch price → delegate to service.
type messageHandler struct {
	cardService *card.Service
	provider    exchange.PriceProvider
}

func newMessageHandler(
	cardService *card.Service,
	provider exchange.PriceProvider,
) *messageHandler {
	return &messageHandler{
		cardService: cardService,
		provider:    provider,
	}
}

// processMessage handles a single FundCardMessage from the queue.
//
// Worker responsibility: message parsing + price fetching + satoshi calculation.
// Business logic (treasury check, card update, tx creation) is in card.Service.FundCard().
func (h *messageHandler) processMessage(ctx context.Context, messageID string, data []byte) error {
	logger.Info("Processing fund_card message", zap.String("messageID", messageID))

	// Step 1: Deserialize and validate message
	msg, err := messages.FromJSONFundCard(data)
	if err != nil {
		return fmt.Errorf("invalid message: %w", err)
	}
	logger.Info("Received message",
		zap.String("card_id", msg.CardID),
		zap.Int64("fiat_amount_cents", msg.FiatAmountCents),
		zap.String("fiat_currency", msg.FiatCurrency),
	)

	// Step 2: Fetch BTC price from OTC provider
	price, err := h.provider.GetPrice(ctx, msg.FiatCurrency)
	if err != nil {
		return fmt.Errorf("error fetching BTC price: %w", err)
	}
	logger.Info("BTC price fetched",
		zap.Float64("price", price),
		zap.String("currency", msg.FiatCurrency),
	)

	// Step 3: Calculate BTC amount in satoshis
	fiatAmount := float64(msg.FiatAmountCents) / 100.0
	btcAmount := fiatAmount / price
	satoshis := int64(btcAmount * 100_000_000)
	if satoshis <= 0 {
		logger.Error("Calculated 0 sats — price too high or amount too low",
			zap.Float64("fiat_amount", fiatAmount),
			zap.Float64("btc_price", price),
		)
		return nil // Permanent failure, don't retry
	}

	// Step 4: Delegate to service (treasury check + card activation + tx record)
	if err := h.cardService.FundCard(ctx, msg.CardID, satoshis); err != nil {
		return fmt.Errorf("failed to fund card: %w", err)
	}

	logger.Info("Message processed successfully",
		zap.String("messageID", messageID),
		zap.String("card_id", msg.CardID),
		zap.Int64("satoshis", satoshis),
	)
	return nil
}
