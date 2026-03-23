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
	"errors"
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

const (
	// targetConfirmations is the number of block confirmations required before
	// a transaction is considered fully settled (~1 hour on Bitcoin mainnet).
	targetConfirmations = 6

	// monitorTimeout is how long we wait for a transaction to confirm before
	// marking it as failed. Transactions stuck in mempool beyond 24h are
	// typically evicted by nodes and will never confirm.
	monitorTimeout = 24 * time.Hour
)

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
// It queries LND for the on-chain confirmation status of the broadcast
// transaction and either:
//   - Marks it confirmed (>= 6 confirmations)
//   - Updates the confirmation count and re-publishes for another check cycle
//   - Marks it failed after 24 h with no confirmations
func (h *messageHandler) processMessage(ctx context.Context, messageID string, data []byte) error {
	// Step 1: Deserialize and validate the message.
	msg, err := messages.FromJSONMonitorTx(data)
	if err != nil {
		// Malformed messages cannot be retried — ACK them so they don't
		// block the consumer group.
		logger.Error("Failed to deserialize MonitorTransactionMessage — ACKing",
			zap.String("messageID", messageID),
			zap.Error(err),
		)
		return nil
	}

	// Step 2: Look up the transaction in the database.
	tx, err := h.txRepo.GetByTxHash(ctx, msg.TxHash)
	if err != nil {
		if errors.Is(err, database.ErrTransactionNotFound) {
			logger.Warn("Transaction not found in DB — ACKing",
				zap.String("tx_hash", msg.TxHash),
				zap.String("card_id", msg.CardID),
			)
			return nil // ACK: can't monitor what we don't know about
		}
		// Transient DB error — NACK so the consumer retries.
		return fmt.Errorf("failed to fetch transaction %s: %w", msg.TxHash, err)
	}

	// Step 3: Idempotency — skip already-terminal transactions.
	if tx.Status == database.Confirmed || tx.Status == database.Failed {
		logger.Info("Transaction already in terminal state — skipping",
			zap.String("tx_hash", msg.TxHash),
			zap.String("status", string(tx.Status)),
		)
		return nil
	}

	// Step 4: Check for timeout (24 h since broadcast).
	if tx.BroadcastAt != nil && time.Since(*tx.BroadcastAt) > monitorTimeout {
		if err := h.txRepo.Update(ctx, tx.ID, database.Failed, 0, nil, nil); err != nil {
			return fmt.Errorf("failed to mark timed-out transaction as failed: %w", err)
		}
		logger.Error("Transaction timed out — marked failed",
			zap.String("tx_hash", msg.TxHash),
			zap.String("tx_id", tx.ID),
			zap.Time("broadcast_at", *tx.BroadcastAt),
		)
		return nil
	}

	// Step 5: Query LND for the current on-chain status.
	onchainStatus, err := h.lndClient.GetTransaction(ctx, msg.TxHash)
	if err != nil {
		// LND is temporarily unavailable — re-publish and ACK so the
		// consumer group isn't blocked on a single failing message.
		logger.Warn("LND query failed — re-publishing for retry",
			zap.String("tx_hash", msg.TxHash),
			zap.Error(err),
		)
		h.republish(ctx, msg)
		return nil
	}

	// Step 6: Transaction not yet visible in LND's wallet.
	if !onchainStatus.Found {
		logger.Info("Transaction not yet visible in LND wallet — re-publishing",
			zap.String("tx_hash", msg.TxHash),
		)
		h.republish(ctx, msg)
		return nil
	}

	confirmations := int(onchainStatus.NumConfirmations)
	logger.Info("Transaction confirmation update",
		zap.String("tx_hash", msg.TxHash),
		zap.Int("confirmations", confirmations),
		zap.Int("required", targetConfirmations),
	)

	// Step 7a: Fully confirmed.
	if confirmations >= targetConfirmations {
		now := time.Now().UTC()
		if err := h.txRepo.Update(ctx, tx.ID, database.Confirmed, confirmations, nil, &now); err != nil {
			return fmt.Errorf("failed to mark transaction as confirmed: %w", err)
		}
		logger.Info("Transaction confirmed",
			zap.String("tx_hash", msg.TxHash),
			zap.String("tx_id", tx.ID),
			zap.Int("confirmations", confirmations),
		)
		return nil
	}

	// Step 7b: Partially confirmed or still in mempool — update progress and
	// re-publish for another poll cycle.
	if err := h.txRepo.Update(ctx, tx.ID, database.Pending, confirmations, nil, nil); err != nil {
		// Non-fatal: log and continue to re-publish.
		logger.Warn("Failed to update confirmation count",
			zap.String("tx_hash", msg.TxHash),
			zap.Int("confirmations", confirmations),
			zap.Error(err),
		)
	}

	h.republish(ctx, msg)
	logger.Info("Transaction not yet confirmed — re-queued",
		zap.String("tx_hash", msg.TxHash),
		zap.Int("confirmations", confirmations),
	)
	return nil
}

// republish re-publishes a MonitorTransactionMessage to the monitor_tx stream
// for the next poll cycle. Errors are logged but not returned — the original
// message is ACKed regardless to prevent consumer group stalls.
func (h *messageHandler) republish(ctx context.Context, msg *messages.MonitorTransactionMessage) {
	data, err := msg.ToJSON()
	if err != nil {
		logger.Error("Failed to serialize message for republish",
			zap.String("tx_hash", msg.TxHash),
			zap.Error(err),
		)
		return
	}
	if _, err := h.queue.Publish(ctx, "monitor_tx", data); err != nil {
		logger.Error("Failed to re-publish monitor_tx message",
			zap.String("tx_hash", msg.TxHash),
			zap.Error(err),
		)
	}
}
