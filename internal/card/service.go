package card

import (
	"btc-giftcard/internal/database"
	"btc-giftcard/internal/lnd"
	messages "btc-giftcard/internal/queue"
	"btc-giftcard/pkg/cache"
	"btc-giftcard/pkg/logger"
	streams "btc-giftcard/pkg/queue"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ============================================================================
// Errors
// ============================================================================

var (
	ErrCardNotFound        = errors.New("card not found")
	ErrCardNotActive       = errors.New("card is not active")
	ErrCardAlreadyUsed     = errors.New("card has already been redeemed")
	ErrInsufficientFunds   = errors.New("insufficient funds on card")
	ErrInsufficientBalance = errors.New("insufficient treasury balance")
	ErrTreasuryLockBusy    = errors.New("treasury lock is held by another process")
	ErrInvalidMethod       = errors.New("invalid redeem method")
	ErrLightningInvoice    = errors.New("lightning invoice is required")
	ErrInvalidCurrency     = errors.New("unsupported fiat currency")
	ErrInvalidFiatAmount   = errors.New("fiat amount must be positive")
	ErrInvalidPurchase     = errors.New("purchase price must be positive")
	ErrMissingEmail        = errors.New("purchase email is required")
)

// ============================================================================
// Constants
// ============================================================================

const (
	// Treasury balance cache (Redis)
	treasuryAvailableCacheKey = "treasury:available_sats"
	treasuryAvailableCacheTTL = 10 * time.Second

	// Treasury distributed lock (Redis SETNX)
	treasuryLockKey = "treasury:lock"
	treasuryLockTTL = 5 * time.Second

	// Per-card lock for concurrent redemption protection
	cardLockPrefix = "card:lock:"
	cardLockTTL    = 10 * time.Second
)

// ============================================================================
// Service — core type and constructor
// ============================================================================

// Service handles gift card business logic. It is the single source of truth
// for the card lifecycle and is called by both API handlers and background workers.
//
// API handlers call: CreateCard, RedeemCard, GetCardByCode, GetCardBalance,
// ValidateCardCode, GetTreasuryAvailableBalance.
//
// Workers call: FundCard (fund_card worker).
type Service struct {
	db        *database.DB
	cardRepo  *database.CardRepository
	txRepo    *database.TransactionRepository
	network   string // "testnet" or "mainnet"
	queue     *streams.StreamQueue
	lndClient *lnd.Client
}

// NewService creates a new card service instance.
func NewService(
	db *database.DB,
	cardRepo *database.CardRepository,
	txRepo *database.TransactionRepository,
	network string,
	queue *streams.StreamQueue,
	lndClient *lnd.Client,
) *Service {
	return &Service{
		db:        db,
		cardRepo:  cardRepo,
		txRepo:    txRepo,
		network:   network,
		queue:     queue,
		lndClient: lndClient,
	}
}

// ============================================================================
// API methods — called by HTTP handlers
// ============================================================================

// --- Card lifecycle --------------------------------------------------------

type CreateCardFiatCurrency string

const (
	USD CreateCardFiatCurrency = "USD"
	EUR CreateCardFiatCurrency = "EUR"
)

// IsValid returns true if the currency is a supported fiat currency.
func (c CreateCardFiatCurrency) IsValid() bool {
	switch c {
	case USD, EUR:
		return true
	default:
		return false
	}
}

// CreateCardRequest contains the parameters for creating a new gift card.
// BTCAmountSats is NOT provided at creation — it will be calculated and set
// by the fund_card worker based on the current BTC/fiat exchange rate.
type CreateCardRequest struct {
	FiatAmountCents    int64                  `json:"fiat_amount_cents"`
	FiatCurrency       CreateCardFiatCurrency `json:"fiat_currency"`
	PurchasePriceCents int64                  `json:"purchase_price_cents"`
	UserID             *string                `json:"user_id,omitempty"`
	PurchaseEmail      string                 `json:"purchase_email"`
}

// CreateCardResponse contains the created card details.
type CreateCardResponse struct {
	CardID        string              `json:"card_id"`
	Code          string              `json:"code"`
	BTCAmountSats int64               `json:"btc_amount_sats"`
	Status        database.CardStatus `json:"status"`
	CreatedAt     time.Time           `json:"created_at"`
}

// validateCreateRequest validates the create card request fields.
func (s *Service) validateCreateRequest(req CreateCardRequest) error {
	if !req.FiatCurrency.IsValid() {
		return ErrInvalidCurrency
	}
	if req.FiatAmountCents <= 0 {
		return ErrInvalidFiatAmount
	}
	if req.PurchasePriceCents <= 0 {
		return ErrInvalidPurchase
	}
	if req.PurchaseEmail == "" {
		return ErrMissingEmail
	}
	return nil
}

// CreateCard creates a new gift card as a balance claim on the treasury.
// No wallet or private key is generated — cards are custodial.
//
// After persisting the card, it publishes a FundCardMessage to the "fund_card"
// stream so a worker can fetch the BTC price and activate the card.
func (s *Service) CreateCard(ctx context.Context, req CreateCardRequest) (*CreateCardResponse, error) {
	// 0. Validate request
	if err := s.validateCreateRequest(req); err != nil {
		return nil, err
	}

	// 1. Generate a unique card code
	code, err := s.generateCardCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to generate card code: %w", err)
	}

	// 2. Create Card struct (custodial model — no wallet, no keys)
	// BTCAmountSats is 0 and will be set by the funding worker
	// based on the current exchange rate when the card is funded.
	card := &database.Card{
		ID:                 uuid.New().String(),
		UserID:             req.UserID,
		PurchaseEmail:      req.PurchaseEmail,
		OwnerEmail:         req.PurchaseEmail,
		Code:               code,
		BTCAmountSats:      0, // Will be set by funding worker based on current BTC price
		FiatAmountCents:    req.FiatAmountCents,
		FiatCurrency:       string(req.FiatCurrency),
		PurchasePriceCents: req.PurchasePriceCents,
		Status:             database.Created,
		CreatedAt:          time.Now().UTC(),
	}

	// 3. Save card to database
	err = s.cardRepo.Create(ctx, card)
	if err != nil {
		if errors.Is(err, database.ErrCardCodeExists) {
			return nil, fmt.Errorf("card code collision (unexpected): %w", err)
		}
		return nil, fmt.Errorf("failed to save card: %w", err)
	}

	// 4. Publish FundCardMessage to queue (don't fail card creation if this fails)
	msg := messages.FundCardMessage{
		CardID:          card.ID,
		FiatAmountCents: card.FiatAmountCents,
		FiatCurrency:    card.FiatCurrency,
	}

	msgJSON, err := msg.ToJSON()
	if err != nil {
		logger.Error("Failed to serialize FundCardMessage",
			zap.String("card_id", card.ID),
			zap.Error(err),
		)
	} else {
		_, err = s.queue.Publish(ctx, "fund_card", msgJSON)
		if err != nil {
			logger.Error("Failed to publish FundCardMessage",
				zap.String("card_id", card.ID),
				zap.Error(err),
			)
		} else {
			logger.Info("Published FundCardMessage",
				zap.String("card_id", card.ID),
			)
		}
	}

	// 5. Return response
	return &CreateCardResponse{
		CardID:        card.ID,
		Code:          card.Code,
		BTCAmountSats: card.BTCAmountSats,
		Status:        card.Status,
		CreatedAt:     card.CreatedAt,
	}, nil
}

// RedeemCardMethod identifies the payment rail for a redemption.
type RedeemCardMethod string

const (
	Lightning RedeemCardMethod = "lightning"
)

// RedeemCardRequest contains the parameters for redeeming (spending) a card.
type RedeemCardRequest struct {
	Code             string           `json:"code"`
	Method           RedeemCardMethod `json:"method"`
	AmountSats       int64            `json:"amount_sats"`
	LightningInvoice string           `json:"invoice,omitempty"`
}

// RedeemCardResponse contains the redemption transaction details.
type RedeemCardResponse struct {
	TransactionID    string                     `json:"transaction_id"`
	Method           string                     `json:"method"`
	TxHash           *string                    `json:"tx_hash,omitempty"`
	PaymentHash      *string                    `json:"payment_hash,omitempty"`
	BTCAmountSats    int64                      `json:"btc_amount_sats"`
	RemainingBalance int64                      `json:"remaining_balance_sats"`
	Status           database.TransactionStatus `json:"status"`
}

// RedeemCard processes a card spend (full or partial) via Lightning Network.
// Cards support partial spends — multiple transactions until balance reaches 0.
//
// Concurrency: a per-card Redis lock prevents double-spend from concurrent requests.
func (s *Service) RedeemCard(ctx context.Context, req RedeemCardRequest) (*RedeemCardResponse, error) {
	// Step 1: Validate input
	if err := s.validateRedeemRequest(req); err != nil {
		return nil, err
	}

	// Step 2: Acquire per-card lock (prevent concurrent double-spend)
	lockKey := cardLockPrefix + req.Code
	acquired, err := cache.SetNX(ctx, lockKey, "locked", cardLockTTL)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire card lock: %w", err)
	}
	if !acquired {
		return nil, errors.New("card is being processed by another request")
	}
	defer cache.Delete(ctx, lockKey)

	// Step 3: Retrieve and validate card
	card, err := s.validateCardForRedemption(ctx, req.Code, req.AmountSats)
	if err != nil {
		return nil, err
	}

	// Step 4: Execute payment via LND
	payResult, err := s.executePayment(ctx, req)
	if err != nil {
		return nil, err
	}

	// Steps 5+6: Record transaction and update card balance atomically.
	// These two writes must succeed or fail together. We retry with backoff
	// because the LND payment (Step 4) is irreversible — if the DB write fails
	// we must keep trying rather than silently lose the payment record.
	// Idempotency: UNIQUE constraints on payment_hash / tx_hash prevent duplicate
	// records if a successful commit acknowledgment was lost (network blip).
	now := time.Now().UTC()
	var (
		redeemedTx       *database.Transaction
		remainingBalance int64
	)

	dbErr := retryWithBackoff(ctx, 3, 100*time.Millisecond, func() error {
		return s.db.RunInTx(ctx, func(q database.Querier) error {
			var err error
			// Fresh UUID per attempt avoids PK collision if a previous attempt
			// was rolled back after a partial write.
			redeemedTx, err = s.recordRedemptionTransaction(ctx, s.txRepo.WithTx(q), card.ID, req, payResult, now)
			if err != nil {
				return err
			}
			remainingBalance, err = s.updateCardBalance(ctx, s.cardRepo.WithTx(q), card.ID, card.BTCAmountSats, req.AmountSats)
			return err
		})
	})

	if dbErr != nil {
		logger.Error("CRITICAL: payment sent but DB write failed after retries — manual reconciliation required",
			zap.String("card_id", card.ID),
			zap.String("card_code", req.Code),
			zap.Stringp("payment_hash", payResult.PaymentHash),
			zap.Stringp("tx_hash", payResult.TxHash),
			zap.Int64("amount_sats", req.AmountSats),
			zap.Error(dbErr),
		)
		return nil, fmt.Errorf("payment sent but failed to record — contact support with card code %s", req.Code)
	}

	// Step 7: Invalidate treasury cache (balance changed)
	s.InvalidateTreasuryCache(ctx)

	logger.Info("Card redeemed successfully",
		zap.String("card_id", card.ID),
		zap.String("tx_id", redeemedTx.ID),
		zap.String("method", string(req.Method)),
		zap.Int64("amount_sats", req.AmountSats),
		zap.Int64("remaining_sats", remainingBalance),
	)

	return &RedeemCardResponse{
		TransactionID:    redeemedTx.ID,
		Method:           string(req.Method),
		TxHash:           payResult.TxHash,
		PaymentHash:      payResult.PaymentHash,
		BTCAmountSats:    req.AmountSats,
		RemainingBalance: remainingBalance,
		Status:           redeemedTx.Status,
	}, nil
}

// --- Card queries ----------------------------------------------------------

// GetCardByCode retrieves card details by redemption code.
func (s *Service) GetCardByCode(ctx context.Context, code string) (*database.Card, error) {
	card, err := s.cardRepo.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, database.ErrCardNotFound) {
			return nil, ErrCardNotFound
		}
		return nil, fmt.Errorf("failed to get card: %w", err)
	}
	return card, nil
}

// GetCardBalance returns the remaining balance (in satoshis) for a card.
// In the custodial model, this is simply the btc_amount_sats field in the database.
func (s *Service) GetCardBalance(ctx context.Context, cardID string) (int64, error) {
	card, err := s.cardRepo.GetByID(ctx, cardID)
	if err != nil {
		if errors.Is(err, database.ErrCardNotFound) {
			return 0, ErrCardNotFound
		}
		return 0, fmt.Errorf("failed to get card: %w", err)
	}
	return card.BTCAmountSats, nil
}

// ValidateCardCode checks if a card code is valid and usable.
// Returns the card status without sensitive information.
func (s *Service) ValidateCardCode(ctx context.Context, code string) (database.CardStatus, error) {
	card, err := s.cardRepo.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, database.ErrCardNotFound) {
			return database.Expired, ErrCardNotFound
		}
		return database.Expired, fmt.Errorf("failed to validate card: %w", err)
	}
	return card.Status, nil
}

// --- Treasury queries ------------------------------------------------------

// GetTreasuryAvailableBalance returns the available treasury balance (total LND
// holdings minus reserved card balances). Results are cached in Redis for 10s
// to avoid hitting LND (~50-100ms latency) on every call.
//
// This is the API-safe method — use it for read-only endpoints.
// Write paths (FundCard) bypass the cache via computeTreasuryBalance for
// authoritative reads inside the treasury lock.
func (s *Service) GetTreasuryAvailableBalance(ctx context.Context) (int64, error) {
	// Try cache first
	if cached, err := cache.Get(ctx, treasuryAvailableCacheKey); err == nil && cached != "" {
		if val, parseErr := strconv.ParseInt(cached, 10, 64); parseErr == nil {
			return val, nil
		}
		// Invalid cache value — fall through to recompute
	}

	// Compute from LND + DB
	available, err := s.computeTreasuryBalance(ctx)
	if err != nil {
		return 0, err
	}

	// Cache the result (best-effort, don't fail on cache error)
	if cacheErr := cache.Set(ctx, treasuryAvailableCacheKey, strconv.FormatInt(available, 10), treasuryAvailableCacheTTL); cacheErr != nil {
		logger.Warn("failed to cache treasury balance", zap.Error(cacheErr))
	}

	return available, nil
}

// ============================================================================
// Worker methods — called by background workers (fund_card)
// ============================================================================

// FundCard reserves treasury balance for a card, activating it.
// Called by the fund_card worker after fetching the BTC price and calculating satoshis.
//
// Flow:
//  1. Validate card exists and is in Created status
//  2. Set status to Funding (idempotency guard)
//  3. Acquire treasury lock (prevent concurrent workers from overselling)
//  4. Compute fresh treasury balance (bypasses cache for authoritative read)
//  5. Update card: BTCAmountSats=satoshis, Status=Active, FundedAt=now
//  6. Create Fund transaction record (accounting only — no blockchain tx)
//  7. Invalidate treasury cache
//
// If treasury is insufficient, reverts card to Created for retry.
func (s *Service) FundCard(ctx context.Context, cardID string, satoshis int64) error {
	if satoshis <= 0 {
		return errors.New("satoshis must be positive")
	}

	// Step 1: Fetch card and validate state
	card, err := s.cardRepo.GetByID(ctx, cardID)
	if err != nil {
		return fmt.Errorf("failed to fetch card: %w", err)
	}

	if card.Status != database.Created {
		logger.Warn("Card already processed, skipping",
			zap.String("card_id", card.ID),
			zap.String("status", string(card.Status)),
		)
		return nil // Idempotent: skip already-funded cards
	}

	// Step 2: Set status to Funding (prevents duplicate processing)
	if err := s.cardRepo.Update(ctx, card.ID, database.Funding, nil, nil, nil); err != nil {
		return fmt.Errorf("failed to set funding status: %w", err)
	}

	// Step 3: Acquire treasury lock
	acquired, err := s.AcquireTreasuryLock(ctx)
	if err != nil {
		s.revertCardToCreated(ctx, card.ID)
		return fmt.Errorf("failed to acquire treasury lock: %w", err)
	}
	if !acquired {
		s.revertCardToCreated(ctx, card.ID)
		return ErrTreasuryLockBusy
	}
	defer s.ReleaseTreasuryLock(ctx)

	// Step 4: Fresh treasury balance (cache bypassed — authoritative read inside lock)
	available, err := s.computeTreasuryBalance(ctx)
	if err != nil {
		s.revertCardToCreated(ctx, card.ID)
		return fmt.Errorf("failed to compute treasury balance: %w", err)
	}

	if available < satoshis {
		s.revertCardToCreated(ctx, card.ID)
		logger.Error("Treasury insufficient",
			zap.String("card_id", card.ID),
			zap.Int64("needed", satoshis),
			zap.Int64("available", available),
		)
		return fmt.Errorf("treasury insufficient: need %d sats, have %d available", satoshis, available)
	}

	// Steps 5+6: Activate card and record fund transaction atomically.
	// Both writes must succeed or fail together — a card that is Active but has
	// no Fund transaction record corrupts treasury balance computations.
	now := time.Now().UTC()

	dbErr := retryWithBackoff(ctx, 3, 100*time.Millisecond, func() error {
		return s.db.RunInTx(ctx, func(q database.Querier) error {
			if err := s.cardRepo.WithTx(q).Update(ctx, card.ID, database.Active, &satoshis, &now, nil); err != nil {
				return fmt.Errorf("failed to activate card: %w", err)
			}
			fundTx := &database.Transaction{
				ID:            uuid.New().String(), // fresh UUID per attempt
				CardID:        card.ID,
				Type:          database.Fund,
				BTCAmountSats: satoshis,
				Status:        database.Confirmed,
				Confirmations: 0,
				CreatedAt:     now,
				ConfirmedAt:   &now,
			}
			if err := s.txRepo.WithTx(q).Create(ctx, fundTx); err != nil {
				return fmt.Errorf("failed to create fund transaction: %w", err)
			}
			return nil
		})
	})

	if dbErr != nil {
		s.revertCardToCreated(ctx, card.ID)
		return fmt.Errorf("failed to activate card atomically: %w", dbErr)
	}

	logger.Info("Card funded (balance reserved)",
		zap.String("card_id", card.ID),
		zap.Int64("satoshis", satoshis),
		zap.Int64("treasury_available", available-satoshis),
	)

	// Step 7: Invalidate treasury cache (balance changed)
	s.InvalidateTreasuryCache(ctx)

	return nil
}

// ============================================================================
// Treasury internals — balance computation and distributed locking
// ============================================================================

// computeTreasuryBalance fetches LND balances and DB reserved amounts to
// calculate the available treasury balance. This is the uncached, authoritative
// computation — used inside the treasury lock by write paths (FundCard).
//
// API callers should use GetTreasuryAvailableBalance (cached) instead.
func (s *Service) computeTreasuryBalance(ctx context.Context) (int64, error) {
	channelBal, err := s.lndClient.GetChannelBalance(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get channel balance: %w", err)
	}

	walletBal, err := s.lndClient.GetWalletBalance(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get wallet balance: %w", err)
	}

	totalTreasury := channelBal.LocalSats + walletBal.ConfirmedSats

	totalReserved, err := s.cardRepo.GetTotalReservedBalance(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch total reserved balance: %w", err)
	}

	available := totalTreasury - totalReserved
	if available < 0 {
		logger.Error("treasury oversold: available balance is negative",
			zap.Int64("total_treasury", totalTreasury),
			zap.Int64("total_reserved", totalReserved),
		)
		return 0, ErrInsufficientBalance
	}

	return available, nil
}

// AcquireTreasuryLock acquires a distributed lock for treasury reserve operations.
// Used by FundCard to prevent race conditions when multiple workers try to
// reserve balance simultaneously.
//
// Returns true if the lock was acquired, false if another process holds it.
func (s *Service) AcquireTreasuryLock(ctx context.Context) (bool, error) {
	acquired, err := cache.SetNX(ctx, treasuryLockKey, "locked", treasuryLockTTL)
	if err != nil {
		return false, fmt.Errorf("failed to acquire treasury lock: %w", err)
	}
	if !acquired {
		return false, ErrTreasuryLockBusy
	}
	return true, nil
}

// ReleaseTreasuryLock releases the distributed treasury lock.
func (s *Service) ReleaseTreasuryLock(ctx context.Context) {
	if _, err := cache.Delete(ctx, treasuryLockKey); err != nil {
		logger.Warn("failed to release treasury lock", zap.Error(err))
	}
}

// InvalidateTreasuryCache removes the cached treasury balance.
// Called after card funding or redemption to force a fresh computation
// on the next GetTreasuryAvailableBalance call.
func (s *Service) InvalidateTreasuryCache(ctx context.Context) {
	if _, err := cache.Delete(ctx, treasuryAvailableCacheKey); err != nil {
		logger.Warn("failed to invalidate treasury cache", zap.Error(err))
	}
}

// ============================================================================
// RedeemCard helpers — private methods, each with a single concern
// ============================================================================

// validateRedeemRequest validates the redemption request fields.
func (s *Service) validateRedeemRequest(req RedeemCardRequest) error {
	switch req.Method {
	case Lightning:
		if req.LightningInvoice == "" {
			return ErrLightningInvoice
		}
	default:
		return ErrInvalidMethod
	}

	if req.AmountSats <= 0 {
		return errors.New("amount must be positive")
	}

	return nil
}

// validateCardForRedemption retrieves a card and checks it can be redeemed.
func (s *Service) validateCardForRedemption(ctx context.Context, code string, amountSats int64) (*database.Card, error) {
	card, err := s.GetCardByCode(ctx, code)
	if err != nil {
		return nil, err
	}

	if card.Status != database.Active {
		return nil, ErrCardNotActive
	}

	if amountSats > card.BTCAmountSats {
		return nil, ErrInsufficientFunds
	}

	return card, nil
}

// paymentOutput holds the results of executePayment (unified for both paths).
type paymentOutput struct {
	PaymentHash     *string
	PaymentPreimage *string
	TxHash          *string
	ToAddress       *string
	Invoice         *string
	Status          database.TransactionStatus
	ConfirmedAt     *time.Time
}

// executePayment dispatches to the correct payment path.
func (s *Service) executePayment(ctx context.Context, req RedeemCardRequest) (*paymentOutput, error) {
	switch req.Method {
	case Lightning:
		return s.executeLightningPayment(ctx, req.LightningInvoice, req.AmountSats)
	default:
		return nil, ErrInvalidMethod
	}
}

// executeLightningPayment decodes, validates, and pays a BOLT11 invoice.
func (s *Service) executeLightningPayment(ctx context.Context, invoice string, amountSats int64) (*paymentOutput, error) {
	// Decode and validate
	decoded, err := s.lndClient.DecodeInvoice(ctx, invoice)
	if err != nil {
		return nil, fmt.Errorf("invalid invoice: %w", err)
	}

	if decoded.AmountSats == 0 {
		return nil, errors.New("zero-amount invoices not supported")
	}

	if decoded.IsExpired {
		return nil, errors.New("invoice has expired")
	}

	if decoded.AmountSats != amountSats {
		return nil, fmt.Errorf("invoice amount (%d sats) does not match requested amount (%d sats)", decoded.AmountSats, amountSats)
	}

	// Pay the invoice
	logger.Info("Paying Lightning invoice",
		zap.Int64("amount_sats", amountSats),
		zap.String("destination", decoded.Destination),
	)

	result, err := s.lndClient.PayInvoice(ctx, invoice, s.lndClient.Cfg.MaxPaymentFeeSats)
	if err != nil {
		return nil, fmt.Errorf("lightning payment failed: %w", err)
	}

	// Verify payment actually succeeded (PayInvoice could return non-error with failed status)
	if result.Status != lnd.Succeeded {
		return nil, fmt.Errorf("lightning payment did not succeed: status=%s", result.Status)
	}

	now := time.Now().UTC()
	return &paymentOutput{
		PaymentHash:     &result.PaymentHash,
		PaymentPreimage: &result.PaymentPreimage,
		Invoice:         &invoice,
		Status:          database.Confirmed, // Lightning settles instantly
		ConfirmedAt:     &now,
	}, nil
}

// recordRedemptionTransaction creates a Transaction record for the redemption.
// txRepo is passed explicitly so callers can supply a transaction-scoped repo
// (via TransactionRepository.WithTx) for atomic writes.
func (s *Service) recordRedemptionTransaction(
	ctx context.Context,
	txRepo *database.TransactionRepository,
	cardID string,
	req RedeemCardRequest,
	pay *paymentOutput,
	now time.Time,
) (*database.Transaction, error) {
	method := string(req.Method)
	tx := &database.Transaction{
		ID:               uuid.New().String(),
		CardID:           cardID,
		Type:             database.Redeem,
		RedemptionMethod: &method,
		TxHash:           pay.TxHash,
		PaymentHash:      pay.PaymentHash,
		PaymentPreimage:  pay.PaymentPreimage,
		LightningInvoice: pay.Invoice,
		ToAddress:        pay.ToAddress,
		BTCAmountSats:    req.AmountSats,
		Status:           pay.Status,
		Confirmations:    0,
		CreatedAt:        now,
		BroadcastAt:      &now,
		ConfirmedAt:      pay.ConfirmedAt,
	}

	if err := txRepo.Create(ctx, tx); err != nil {
		return nil, fmt.Errorf("failed to create transaction: %w", err)
	}

	return tx, nil
}

// updateCardBalance deducts the spend amount and marks the card redeemed if balance is zero.
// cardRepo is passed explicitly so callers can supply a transaction-scoped repo
// (via CardRepository.WithTx) for atomic writes.
func (s *Service) updateCardBalance(ctx context.Context, cardRepo *database.CardRepository, cardID string, currentBalance, spendAmount int64) (int64, error) {
	remaining := currentBalance - spendAmount
	status := database.Active
	var redeemedAt *time.Time

	if remaining == 0 {
		status = database.Redeemed
		t := time.Now().UTC()
		redeemedAt = &t
	}

	if err := cardRepo.Update(ctx, cardID, status, &remaining, nil, redeemedAt); err != nil {
		return 0, fmt.Errorf("failed to update card: %w", err)
	}

	return remaining, nil
}

// retryWithBackoff calls fn up to maxAttempts times, doubling the wait after each
// failure. Returns the last error if all attempts fail or ctx is cancelled.
func retryWithBackoff(ctx context.Context, maxAttempts int, initialDelay time.Duration, fn func() error) error {
	var lastErr error
	delay := initialDelay
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}
		if lastErr = fn(); lastErr == nil {
			return nil
		}
	}
	return lastErr
}

// ============================================================================
// FundCard helpers
// ============================================================================

// revertCardToCreated resets a card back to Created status so it can be retried.
func (s *Service) revertCardToCreated(ctx context.Context, cardID string) {
	if err := s.cardRepo.Update(ctx, cardID, database.Created, nil, nil, nil); err != nil {
		logger.Error("Failed to revert card to Created status",
			zap.String("card_id", cardID),
			zap.Error(err),
		)
	}
}

// ============================================================================
// Internal helpers
// ============================================================================

// generateCardCode generates a unique card code.
// Format: GIFT-XXXX-YYYY-ZZZZ (12 alphanumeric characters in 3 groups of 4).
// Uses a character set that excludes visually ambiguous characters (O, 0, I, 1, L).
func (s *Service) generateCardCode(ctx context.Context) (string, error) {
	const charset = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	const codeLength = 16

	for attempt := 0; attempt < 5; attempt++ {
		code := make([]byte, codeLength)
		if _, err := rand.Read(code); err != nil {
			return "", fmt.Errorf("failed to generate random bytes: %w", err)
		}
		for i := range code {
			code[i] = charset[int(code[i])%len(charset)]
		}

		codeStr := string(code)
		formattedCode := fmt.Sprintf("GIFT-%s-%s-%s",
			codeStr[0:4],
			codeStr[4:8],
			codeStr[8:12],
		)

		// Check uniqueness in database
		_, err := s.cardRepo.GetByCode(ctx, formattedCode)
		if err != nil {
			if errors.Is(err, database.ErrCardNotFound) {
				return formattedCode, nil
			}
			return "", fmt.Errorf("failed to check code uniqueness: %w", err)
		}
		// Code exists, retry
	}

	return "", errors.New("failed to generate unique card code after 5 attempts")
}
