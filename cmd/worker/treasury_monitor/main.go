// Package main implements the treasury_monitor background worker.
//
// # Treasury Monitor Worker
//
// This worker runs automated treasury management on a configurable poll interval.
// It is the only component that calls TreasuryManager automatically; every other
// call to TreasuryManager originates from an admin API request.
//
// Automatic flow (ThresholdTrigger):
//  1. Poll Qonto for the current fiat balance every pollInterval (e.g. 5 min)
//  2. Evaluate the ThresholdTrigger rule
//  3. If the balance exceeds the floor → initiate the full purchase pipeline:
//     a. TransferFiatToCryptoCom
//     b. BuyBTC (market order)
//     c. WithdrawBTCToLND (after order fills)
//  4. Log result, sleep until next tick
//
// Manual purchases bypass this worker entirely — they hit the admin API directly.
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
	"btc-giftcard/internal/lnd"
	"btc-giftcard/internal/otc"
	"btc-giftcard/internal/qonto"
	"btc-giftcard/internal/treasury"
	"btc-giftcard/pkg/cache"
	"btc-giftcard/pkg/logger"

	"github.com/google/uuid"
	"github.com/jinzhu/copier"
	"go.uber.org/zap"
)

// Cfg holds the application configuration loaded from config.toml.
var Cfg config.ApiConfig

// pollInterval controls how often the worker checks the Qonto balance.
const pollInterval = 5 * time.Minute

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if err := logger.Init("development"); err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer logger.Sync()

	if err := loadConfig(); err != nil {
		return err
	}
	logger.Info("Starting treasury_monitor worker...")

	// Redis is used by other workers but not strictly required here.
	// Initialise it anyway so the config block is consistent.
	if err := initRedis(); err != nil {
		return err
	}
	defer cache.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lndClient, err := initLND(ctx)
	if err != nil {
		return err
	}
	defer lndClient.Close()

	// Wire external service clients
	qontoClient := qonto.NewClient(qonto.Config{
		BaseURL:      Cfg.Qonto.BaseURL,
		Login:        Cfg.Qonto.Login,
		SecretKey:    Cfg.Qonto.SecretKey,
		IBAN:         Cfg.Qonto.IBAN,
		StagingToken: Cfg.Qonto.StagingToken,
	})

	cryptoClient := otc.NewClient(otc.Config{
		BaseURL:     Cfg.CryptoCom.BaseURL,
		APIKey:      Cfg.CryptoCom.APIKey,
		SecretKey:   Cfg.CryptoCom.SecretKey,
		HTTPTimeout: time.Duration(Cfg.CryptoCom.HTTPTimeout) * time.Second,
	})

	mgr := treasury.NewTreasuryManager(
		qontoClient,
		cryptoClient,
		lndClient,
		treasury.Config{
			CryptoComIBAN:            Cfg.Treasury.CryptoComIBAN,
			CryptoComBeneficiaryName: Cfg.Treasury.CryptoComBeneficiaryName,
			CryptoComBeneficiaryID:   Cfg.Treasury.CryptoComBeneficiaryID,
			LNDChannelPeerPubKey:     Cfg.Treasury.LNDChannelPeerPubKey,
			LNDChannelPeerHost:       Cfg.Treasury.LNDChannelPeerHost,
			LNDChannelThresholdSats:  Cfg.Treasury.LNDChannelThresholdSats,
			LNDChannelTargetSats:     Cfg.Treasury.LNDChannelTargetSats,
		},
	)

	trigger := &treasury.ThresholdTrigger{
		FloorCents:  Cfg.Treasury.PurchaseFloorCents,
		TargetCents: Cfg.Treasury.PurchaseTargetCents,
	}

	runLoop(ctx, mgr, trigger)
	return nil
}

// runLoop polls on a fixed interval until the context is cancelled.
func runLoop(ctx context.Context, mgr *treasury.TreasuryManager, trigger treasury.PurchaseTrigger) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Run one check immediately on start, then on each tick.
	checkAndPurchase(ctx, mgr, trigger)
	checkAndFundLNDChannel(ctx, mgr)

	for {
		select {
		case <-ticker.C:
			checkAndPurchase(ctx, mgr, trigger)
			checkAndFundLNDChannel(ctx, mgr)
		case sig := <-sigChan:
			logger.Info("Received shutdown signal", zap.String("signal", sig.String()))
			return
		case <-ctx.Done():
			return
		}
	}
}

// checkAndPurchase runs a single poll cycle: read balance → evaluate trigger →
// optionally start the full Fiat→BTC→LND purchase pipeline.
func checkAndPurchase(ctx context.Context, mgr *treasury.TreasuryManager, trigger treasury.PurchaseTrigger) {
	balance, err := mgr.GetFiatBalance(ctx)
	if err != nil {
		logger.Error("Failed to fetch fiat balance", zap.Error(err))
		return
	}
	logger.Info("Qonto fiat balance fetched", zap.Int64("balance_cents", balance))

	should, amountCents, err := trigger.ShouldPurchase(ctx, balance)
	if err != nil {
		logger.Error("Purchase trigger evaluation failed", zap.Error(err))
		return
	}
	if !should {
		logger.Info("No purchase needed", zap.Int64("balance_cents", balance))
		return
	}

	logger.Info("Purchase trigger fired",
		zap.Int64("balance_cents", balance),
		zap.Int64("spend_cents", amountCents),
	)
	if err := executePurchasePipeline(ctx, mgr, amountCents); err != nil {
		logger.Error("Purchase pipeline failed", zap.Error(err))
	}
}

// executePurchasePipeline runs the full Fiat → BTC → LND pipeline for the
// given fiat amount (in cents).
//
// Steps:
//  1. Transfer fiat from Qonto to Crypto.com via SEPA.
//  2. Request a live OTC quote via WebSocket.
//  3. Execute the deal against the quote.
//  4. Withdraw purchased BTC to the LND on-chain wallet.
//  5. Poll until the withdrawal is confirmed on-chain.
//  6. Fund a Lightning channel with all confirmed sats.
//
// Note: step 1 (SEPA transfer) currently returns an error because SendTransfer
// in the Qonto client is not yet implemented with SCA proof tokens. The pipeline
// will log the error and abort gracefully without panicking.
func executePurchasePipeline(ctx context.Context, mgr *treasury.TreasuryManager, amountCents int64) error {
	// Step 1: Transfer fiat to Crypto.com
	transferKey := uuid.New().String()
	transfer, err := mgr.TransferFiatToCryptoCom(ctx, amountCents, transferKey)
	if err != nil {
		return fmt.Errorf("step 1 TransferFiatToCryptoCom: %w", err)
	}
	logger.Info("Fiat transfer initiated",
		zap.String("transfer_id", transfer.Transfer.ID),
		zap.String("status", transfer.Transfer.Status),
	)

	// Step 2: Request live OTC quote via WebSocket
	// Derive instrument name from amount currency (always EUR → BTC_EUR)
	clQuoteReqID := uuid.New().String()
	quoteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	quote, err := mgr.RequestOTCQuote(quoteCtx, clQuoteReqID, amountCents)
	if err != nil {
		return fmt.Errorf("step 2 RequestOTCQuote: %w", err)
	}
	logger.Info("OTC quote received",
		zap.String("quote_id", quote.QuoteID),
		zap.String("cl_quote_req_id", clQuoteReqID),
	)

	// Step 3: Execute OTC deal against the quote
	dealLegs := make([]otc.DealLegRequest, len(quote.LegList))
	for i, leg := range quote.LegList {
		dealLegs[i] = otc.DealLegRequest{
			InstrumentName: leg.InstrumentName,
			Side:           leg.Side,
			Price:          leg.Ask, // buy side uses ask price
			Notional:       leg.Notional,
			Quantity:       leg.Quantity,
		}
	}
	dealParams := otc.RequestDealParams{
		DealType:   "QUOTE_REQUEST",
		ClDealID:   uuid.New().String(),
		QuoteID:    quote.QuoteID,
		QuoteReqID: quote.QuoteReqID,
		LegList:    dealLegs,
	}
	deal, err := mgr.RequestOTCDeal(ctx, dealParams)
	if err != nil {
		return fmt.Errorf("step 3 RequestOTCDeal: %w", err)
	}
	logger.Info("OTC deal executed",
		zap.String("deal_id", deal.DealID),
		zap.String("status", string(deal.DealStatus)),
	)

	// Extract BTC quantity from deal legs
	btcAmount := ""
	for _, leg := range deal.LegList {
		if leg.ExecutedQty != "" {
			btcAmount = leg.ExecutedQty
			break
		}
		if leg.Quantity != "" {
			btcAmount = leg.Quantity
		}
	}
	if btcAmount == "" {
		return fmt.Errorf("step 3: could not determine BTC amount from deal legs")
	}

	// Step 4: Withdraw BTC to LND
	clientWdID := uuid.New().String()
	withdrawal, err := mgr.WithdrawBTCToLND(ctx, btcAmount, clientWdID)
	if err != nil {
		return fmt.Errorf("step 4 WithdrawBTCToLND: %w", err)
	}
	logger.Info("BTC withdrawal initiated",
		zap.String("withdrawal_id", withdrawal.ID),
		zap.String("status", string(withdrawal.Status)),
	)

	// Step 5: Poll until withdrawal confirmed
	const maxPolls = 60
	const pollDelay = 30 * time.Second
	var confirmed *otc.Withdrawal
	for i := 0; i < maxPolls; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("step 5: context cancelled while polling withdrawal")
		case <-time.After(pollDelay):
		}
		w, err := mgr.GetWithdrawalStatus(ctx, withdrawal.ID)
		if err != nil {
			logger.Warn("Polling withdrawal status failed", zap.Error(err))
			continue
		}
		if w.IsConfirmed() {
			confirmed = w
			break
		}
		logger.Info("Withdrawal pending", zap.String("status", string(w.Status)))
	}
	if confirmed == nil {
		return fmt.Errorf("step 5: withdrawal %s not confirmed after %d polls", withdrawal.ID, maxPolls)
	}
	logger.Info("BTC withdrawal confirmed",
		zap.String("txid", confirmed.TxID),
		zap.Float64("amount_btc", confirmed.Amount),
	)

	// Step 6: Fund a Lightning channel with all confirmed BTC.
	// All sats go into a channel — there is no on-chain / Lightning split.
	sats := int64(confirmed.Amount * 1e8)
	logger.Info("BTC confirmed on-chain, opening Lightning channel",
		zap.Int64("sats", sats),
	)
	chanResult, err := mgr.FundLNDChannel(ctx, sats)
	if err != nil {
		return fmt.Errorf("step 6 FundLNDChannel: %w", err)
	}
	if chanResult == nil {
		// No existing channels yet — first channel must be opened manually via lncli.
		// BTC is safe in the LND on-chain wallet; checkAndFundLNDChannel will retry.
		logger.Info("BTC in LND on-chain wallet — open first Lightning channel manually via lncli",
			zap.Int64("sats", sats),
		)
		return nil
	}
	logger.Info("Lightning channel opening initiated",
		zap.String("funding_txid", chanResult.FundingTxID),
		zap.String("channel_point", chanResult.ChannelPoint),
		zap.Int64("sats", sats),
	)
	return nil
}

// checkAndFundLNDChannel is the fourth automatic action in the monitor loop.
// When the LND on-chain confirmed balance exceeds LNDChannelThresholdSats, it
// opens a new Lightning channel to Config.LNDChannelPeerPubKey using the
// excess sats, converting on-chain BTC into outbound Lightning liquidity.
func checkAndFundLNDChannel(ctx context.Context, mgr *treasury.TreasuryManager) {
	if Cfg.Treasury.LNDChannelThresholdSats == 0 {
		return // channel-funding not configured
	}

	balance, err := mgr.GetLNDBalance(ctx)
	if err != nil {
		logger.Error("Failed to fetch LND balance for channel check", zap.Error(err))
		return
	}

	if balance.OnChainConfirmedSats <= Cfg.Treasury.LNDChannelThresholdSats {
		logger.Info("LND on-chain balance below channel-open threshold",
			zap.Int64("confirmed_sats", balance.OnChainConfirmedSats),
			zap.Int64("threshold_sats", Cfg.Treasury.LNDChannelThresholdSats),
		)
		return
	}

	channelSats := balance.OnChainConfirmedSats - Cfg.Treasury.LNDChannelTargetSats
	if channelSats <= 0 {
		return
	}

	logger.Info("Opening Lightning channel with excess on-chain balance",
		zap.Int64("channel_sats", channelSats),
		zap.String("peer", Cfg.Treasury.LNDChannelPeerPubKey),
	)
	result, err := mgr.FundLNDChannel(ctx, channelSats)
	if err != nil {
		logger.Error("Lightning channel open failed", zap.Error(err))
		return
	}
	if result == nil {
		logger.Info("No existing channels yet — open the first channel manually via lncli, then this will run automatically")
		return
	}
	logger.Info("Lightning channel opening initiated",
		zap.String("funding_txid", result.FundingTxID),
		zap.String("channel_point", result.ChannelPoint),
	)
}

// ============================================================================
// Infrastructure init (mirrors other workers)
// ============================================================================

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
	)
	return lndClient, nil
}
