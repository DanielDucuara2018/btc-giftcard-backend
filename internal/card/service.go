package card

import (
	"btc-giftcard/internal/database"
	"btc-giftcard/internal/fees"
	"btc-giftcard/internal/lnd"
	"btc-giftcard/internal/payment"
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
	ErrCardNotFound         = errors.New("card not found")
	ErrCardNotActive        = errors.New("card is not active")
	ErrCardAlreadyUsed      = errors.New("card has already been redeemed")
	ErrInsufficientFunds    = errors.New("insufficient funds on card")
	ErrInsufficientBalance  = errors.New("insufficient treasury balance")
	ErrTreasuryLockBusy     = errors.New("treasury lock is held by another process")
	ErrInvalidMethod        = errors.New("invalid redeem method")
	ErrLightningInvoice     = errors.New("lightning invoice is required")
	ErrInvalidCurrency      = errors.New("unsupported fiat currency")
	ErrInvalidFiatAmount    = errors.New("fiat amount must be positive")
	ErrMissingEmail         = errors.New("purchase email is required")
	ErrEmptyItems           = errors.New("at least one item is required")
	ErrInvalidQuantity      = errors.New("item quantity must be positive")
	ErrInvalidPaymentMethod = errors.New("Invalid payment method")
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
	db              *database.DB
	cardRepo        *database.CardRepository
	txRepo          *database.TransactionRepository
	queue           *streams.StreamQueue
	lndClient       *lnd.Client
	paymentProvider payment.Provider
	fees            *fees.Config
}

// NewService creates a new card service instance.
func NewService(
	db *database.DB,
	cardRepo *database.CardRepository,
	txRepo *database.TransactionRepository,
	queue *streams.StreamQueue,
	lndClient *lnd.Client,
	paymentProvider payment.Provider,
	fees *fees.Config,
) *Service {
	return &Service{
		db:              db,
		cardRepo:        cardRepo,
		txRepo:          txRepo,
		queue:           queue,
		lndClient:       lndClient,
		paymentProvider: paymentProvider,
		fees:            fees,
	}
}

// ============================================================================
// API methods — called by HTTP handlers
// ============================================================================

// --- Card lifecycle --------------------------------------------------------

// CreateCardItem is one denomination + quantity in an order.
type CreateCardItem struct {
	FiatAmountCents int64 `json:"fiat_amount_cents"`
	Quantity        int   `json:"quantity"`
}

// CreateCardRequest contains the parameters for a bulk card order.
// A single request creates N cards under one Stripe checkout session.
type CreateCardRequest struct {
	Items         []CreateCardItem       `json:"items"`
	FiatCurrency  database.FiatCurrency  `json:"fiat_currency"`
	UserID        *string                `json:"user_id,omitempty"`
	PurchaseEmail string                 `json:"purchase_email"`
	PaymentMethod database.PaymentMethod `json:"payment_method"`
}

// CreatedCard identifies one card within a bulk order response.
type CreatedCard struct {
	CardID string `json:"card_id"`
	Code   string `json:"code"`
}

// CreateCardResponse is returned after a successful bulk card order.
// All cards share one Stripe checkout session; payment is confirmed via webhook.
type CreateCardResponse struct {
	Cards       []CreatedCard `json:"cards"`
	CheckoutURL string        `json:"checkout_url"`
	SessionID   string        `json:"session_id"`
	ExpiresAt   time.Time     `json:"expires_at"`
}

// validateCreateRequest validates the bulk card order request.
func (s *Service) validateCreateRequest(req CreateCardRequest) error {
	if !req.FiatCurrency.IsValid() {
		return ErrInvalidCurrency
	}
	if req.PurchaseEmail == "" {
		return ErrMissingEmail
	}
	if !req.PaymentMethod.IsValid() {
		return ErrInvalidPaymentMethod
	}
	if len(req.Items) == 0 {
		return ErrEmptyItems
	}
	for _, item := range req.Items {
		if item.FiatAmountCents <= 0 {
			return ErrInvalidFiatAmount
		}
		if item.Quantity <= 0 {
			return ErrInvalidQuantity
		}
	}
	return nil
}

// CreateCard creates N gift cards (potentially of different denominations) under
// a single payment session. All cards share the same payment_reference and are
// persisted with payment_status=pending.
//
// Payment method routing:
//   - "card"          → Stripe hosted checkout. This method creates a Stripe
//     session and returns a checkout_url for the frontend to
//     redirect the buyer. Cards are activated by the
//     POST /webhook/stripe handler on checkout.session.completed.
//   - "bank_transfer" → SEPA via Qonto. No external session is created here;
//     a SEPA reference is generated and returned so the buyer
//     can initiate a bank transfer. Cards are activated by the
//     POST /webhook/qonto handler when the matching inbound
//     transfer is confirmed.
//
// In both cases, FundCardMessages are published by the respective webhook
// handler — NOT here. Cards remain payment_status=pending until the webhook
// fires.
//
// TODO: implement the "bank_transfer" branch (generate SEPA ref, skip Stripe).
func (s *Service) CreateCard(ctx context.Context, req CreateCardRequest) (*CreateCardResponse, error) {
	// 1. Validate request
	if err := s.validateCreateRequest(req); err != nil {
		return nil, err
	}

	// 2. Build one Stripe line item per denomination
	checkoutItems := make([]payment.LineItem, len(req.Items))
	for i, item := range req.Items {
		checkoutItems[i] = payment.LineItem{
			FaceValueCents: item.FiatAmountCents,
			Quantity:       int64(item.Quantity),
			Description:    fmt.Sprintf("Bitcoin Gift Card — %s %.2f", string(req.FiatCurrency), float64(item.FiatAmountCents)/100),
		}
	}

	// 3. Create a single Stripe checkout session covering the entire order
	session, err := s.paymentProvider.CreateCheckoutSession(ctx, payment.CreateCheckoutRequest{
		Items:         checkoutItems,
		PurchaseEmail: req.PurchaseEmail,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create checkout session: %w", err)
	}

	// 4. Generate card codes and build DB rows — all cards share the session ID
	sessionID := session.ID
	checkoutURL := session.URL
	expiresAt := session.ExpiresAt

	var dbCards []*database.Card
	var created []CreatedCard
	for _, item := range req.Items {
		for i := 0; i < item.Quantity; i++ {
			code, err := s.generateCardCode(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to generate card code: %w", err)
			}
			breakdown, err := fees.Calculate(item.FiatAmountCents, req.PaymentMethod, s.fees)
			if err != nil {
				return nil, fmt.Errorf("failed to compute the fees for card code: %w", err)
			}
			id := uuid.New().String()
			dbCards = append(dbCards, &database.Card{
				ID:                    id,
				UserID:                req.UserID,
				PurchaseEmail:         req.PurchaseEmail,
				OwnerEmail:            req.PurchaseEmail,
				Code:                  code,
				BTCAmountSats:         0,
				FiatAmountCents:       item.FiatAmountCents,
				FiatCurrency:          req.FiatCurrency,
				PaymentMethod:         req.PaymentMethod,
				PaymentReference:      &sessionID,
				PaymentStatus:         database.PaymentPending,
				PaymentExpiresAt:      &expiresAt,
				StripeCheckoutURL:     &checkoutURL,
				ServiceFeeCents:       breakdown.ServiceFeeCents,
				ProcessorFeeCents:     breakdown.ProcessorFeeCents,
				ProcessorFeeFlatCents: breakdown.ProcessorFeeFlatCents,
				CryptoSpreadCents:     breakdown.CryptoSpreadCents,
				SEPAFeeCents:          breakdown.SEPAFeeCents,
				TotalFeeCents:         breakdown.TotalFeeCents,
				Status:                database.Created,
				CreatedAt:             time.Now().UTC(),
			})
			created = append(created, CreatedCard{CardID: id, Code: code})
		}
	}

	// 5. Persist all cards atomically in a single transaction
	if err := s.db.RunInTx(ctx, func(q database.Querier) error {
		return s.cardRepo.WithTx(q).BulkCreate(ctx, dbCards)
	}); err != nil {
		return nil, fmt.Errorf("failed to save cards: %w", err)
	}

	// FundCardMessages are published by the webhook handler after Stripe confirms
	// payment — NOT here. Cards are pending payment at this point.
	logger.Info("Created pending card order",
		zap.Int("card_count", len(created)),
		zap.String("session_id", sessionID),
	)

	return &CreateCardResponse{
		Cards:       created,
		CheckoutURL: checkoutURL,
		SessionID:   sessionID,
		ExpiresAt:   expiresAt,
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

// SessionCardsResponse is returned by GetCardsBySessionID.
type SessionCardsResponse struct {
	PaymentStatus database.CardPaymentStatus `json:"payment_status"`
	Cards         []CreatedCard              `json:"cards"`
}

// GetCardsBySessionID returns the cards associated with a Stripe checkout session.
// Card codes are only included when payment_status is "paid".
func (s *Service) GetCardsBySessionID(ctx context.Context, sessionID string) (*SessionCardsResponse, error) {
	cards, err := s.cardRepo.GetByStripeSessionID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cards for session: %w", err)
	}
	if len(cards) == 0 {
		return nil, ErrCardNotFound
	}

	status := cards[0].PaymentStatus
	resp := &SessionCardsResponse{PaymentStatus: status}
	if status == database.PaymentPaid {
		for _, c := range cards {
			resp.Cards = append(resp.Cards, CreatedCard{CardID: c.ID, Code: c.Code})
		}
	}
	return resp, nil
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

func (s *Service) HandleCheckoutEvent(ctx context.Context, event *payment.Event) error {
	if event.CheckoutSession == nil {
		return nil
	}
	sessionID := event.CheckoutSession.ID
	switch event.Type {
	case payment.EventCheckoutCompleted:
		cards, err := s.cardRepo.GetByStripeSessionID(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("failed to get cards for stripe session %s: %w", sessionID, err)
		}

		if len(cards) == 0 {
			return nil
		}

		if cards[0].PaymentStatus != database.PaymentPending {
			return nil
		}

		if err := s.cardRepo.UpdatePaymentStatus(ctx, sessionID, database.PaymentPaid); err != nil {
			return fmt.Errorf("failed to update payment status for session %s: %w", sessionID, err)
		}

		for _, card := range cards {
			msg := messages.FundCardMessage{CardID: card.ID, NetFiatAmountCents: card.FiatAmountCents - card.TotalFeeCents, FiatCurrency: card.FiatCurrency}
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
		}
	case payment.EventCheckoutExpired:
		cards, err := s.cardRepo.GetByStripeSessionID(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("failed to get cards for stripe session %s: %w", sessionID, err)
		}

		if len(cards) == 0 {
			return nil
		}

		if err := s.cardRepo.UpdatePaymentStatus(ctx, sessionID, database.PaymentExpired); err != nil {
			return fmt.Errorf("failed to update payment status for session %s: %w", sessionID, err)
		}
	}
	return nil
}
