// Package main implements the monitor_tx background worker.
//
// # On-Chain Transaction Monitor
//
// This worker consumes MonitorTransactionMessage from the "monitor_tx" Redis
// stream. Messages are published by card.Service.RedeemCard() after an on-chain
// redemption broadcasts a transaction.
//
// Purpose:
//   - Track on-chain Bitcoin transactions until they reach sufficient confirmations
//   - Update the transaction status in the database (pending → confirmed)
//   - Log confirmation progress for observability
//
// Flow:
//
//  1. RedeemCard (on-chain path) broadcasts tx via LND SendOnChain
//  2. RedeemCard publishes MonitorTransactionMessage {CardID, TxHash, ExpectedAmountSats, DestinationAddr}
//  3. This worker:
//     - Query LND for the transaction status
//     - If confirmed (>= 6 confirmations): update status to "confirmed"
//     - If still pending: re-publish for another poll cycle
//     - If unconfirmed after 24h: mark as "failed"
//
// Lightning payments do NOT go through this worker — they confirm instantly
// and are marked as confirmed in RedeemCard itself.
//
// Configuration:
//   - Target confirmations: 6 (~1 hour)
//   - Poll interval: re-publish with delay (~60s between checks)
//   - Timeout: 24 hours — unconfirmed after this is marked failed
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"btc-giftcard/config"
	"btc-giftcard/internal/database"
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
	logger.Info("Starting monitor_tx worker...")

	if err := initRedis(); err != nil {
		return err
	}
	defer cache.Close()

	db, err := initDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lndClient, err := initLND(ctx)
	if err != nil {
		return err
	}
	defer lndClient.Close()

	// Wire dependencies
	txRepo := database.NewTransactionRepository(db)
	queue := streams.NewStreamQueue(cache.Client)
	handler := newMessageHandler(txRepo, lndClient, queue)

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

// loadConfig reads config.toml relative to this source file.
func loadConfig() error {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filename)
	configPath := config.Path(root).Join("config.toml", "..", "..", "..")
	if err := config.Load(configPath, &Cfg); err != nil {
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
	streamName := "monitor_tx"
	groupName := "monitors"
	consumerName := fmt.Sprintf("monitor-worker-%d", time.Now().Unix())

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

	logger.Info("Monitor tx worker is running, waiting for messages...",
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
	logger.Info("Monitor tx worker shut down gracefully")
}

// ============================================================================
// Message Processing
// ============================================================================

// messageHandler holds the dependencies for processing monitor_tx messages.
type messageHandler struct {
	txRepo    *database.TransactionRepository
	lndClient *lnd.Client
	queue     *streams.StreamQueue
}

func newMessageHandler(
	txRepo *database.TransactionRepository,
	lndClient *lnd.Client,
	queue *streams.StreamQueue,
) *messageHandler {
	return &messageHandler{
		txRepo:    txRepo,
		lndClient: lndClient,
		queue:     queue,
	}
}

// processMessage handles a single MonitorTransactionMessage from the queue.
//
// Implementation steps:
//
//  1. Deserialize and validate the message using messages.FromJSONMonitorTx(data)
//
//  2. Look up the transaction in the database by tx_hash:
//     - Call h.txRepo.GetByTxHash(ctx, msg.TxHash)
//     - If not found, log warning and return nil (ACK, don't retry)
//     - If already "confirmed" or "failed", return nil (idempotent)
//
//  3. Query the transaction status from LND or a blockchain API:
//     - Option A: Use LND's GetTransactions RPC to find our tx in the list
//     - Option B: Query a public API (mempool.space, blockstream.info) as fallback
//     - Extract: confirmation count, block hash, block height
//
//  4. Evaluate confirmation status:
//     a) If confirmations >= targetConfirmations (6):
//     - Update transaction: status="confirmed", confirmations=N, confirmed_at=now()
//     - Log success with card_id, tx_hash, confirmations
//     - Return nil (ACK)
//     b) If confirmations > 0 but < target:
//     - Update transaction: confirmations=N (partial progress)
//     - Re-publish the message to "monitor_tx" stream for another check later
//     - Log progress: "tx has X/6 confirmations"
//     - Return nil (ACK this message — a new one was published)
//     c) If confirmations == 0 (still in mempool):
//     - Check how long since broadcast_at (from the transaction record)
//     - If < 24 hours: re-publish for later check
//     - If > 24 hours: mark as "failed", log alert
//     - Return nil
//     d) If transaction not found on-chain at all:
//     - If broadcast_at < 1 hour ago: re-publish (may still be propagating)
//     - If broadcast_at > 24 hours ago: mark as "failed", log alert
//     - Return nil
//
//  5. For re-publishing: use a simple delay mechanism
//     - Publish a new MonitorTransactionMessage to "monitor_tx"
//     - The consumer will pick it up on the next cycle
//     - Consider adding a "check_count" or "first_seen" field to prevent infinite loops
func (h *messageHandler) processMessage(ctx context.Context, messageID string, data []byte) error {
	// TODO: Implement — see steps above.
	//
	// Placeholder to keep imports valid:
	_ = messages.FromJSONMonitorTx
	_ = h.txRepo
	_ = h.lndClient
	_ = h.queue

	logger.Info("Processing monitor_tx message", zap.String("messageID", messageID))

	panic("processMessage not implemented")
}
