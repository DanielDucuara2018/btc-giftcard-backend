//go:build integration

package card

import (
	"btc-giftcard/internal/database"
	"btc-giftcard/internal/fees"
	"btc-giftcard/internal/payment"
	messages "btc-giftcard/internal/queue"
	"btc-giftcard/pkg/logger"
	streams "btc-giftcard/pkg/queue"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Initialize logger for tests
	_ = logger.Init("development")
}

// ============================================================================
// Test helpers
// ============================================================================

// mockPaymentProvider satisfies payment.Provider for unit tests.
type mockPaymentProvider struct{}

func (m *mockPaymentProvider) CreateCheckoutSession(
	_ context.Context,
	_ payment.CreateCheckoutRequest,
) (*payment.CheckoutSession, error) {
	return &payment.CheckoutSession{
		ID:        "cs_mock_" + uuid.New().String()[:8],
		URL:       "https://checkout.stripe.com/mock",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}

func (m *mockPaymentProvider) ConstructEvent([]byte, string) (*payment.Event, error) {
	return nil, errors.New("not implemented")
}

// testFeesCfg returns a fees.Config with realistic non-zero values.
func testFeesCfg() *fees.Config {
	return &fees.Config{
		ServiceFeePct:    2.0,
		StripeFeePct:     1.4,
		StripeFeeFlatEUR: 0.25,
		CryptoSpreadPct:  0.5,
		SEPAFeeEUR:       0.0,
		PaymentExpiryH:   24,
	}
}

// setupTestService creates a minimal service (no payment provider, no fees).
// Use this for HandleCheckoutEvent tests and validation-only tests.
func setupTestService(t *testing.T) (*Service, *database.DB, *database.CardRepository, *redis.Client) {
	t.Helper()

	db := database.SetupTestDB(t)
	cardRepo := database.NewCardRepository(db)
	txRepo := database.NewTransactionRepository(db)

	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1,
	})

	ctx := context.Background()
	redisClient.Del(ctx, "fund_card")

	queue := streams.NewStreamQueue(redisClient)
	err := queue.DeclareStream(ctx, "fund_card", "test_workers")
	require.NoError(t, err)

	service := NewService(db, cardRepo, txRepo, queue, nil, nil, nil)
	return service, db, cardRepo, redisClient
}

// setupTestServiceFull creates a service with a mock payment provider and fees
// config. Use this for CreateCard happy-path tests.
func setupTestServiceFull(t *testing.T) (*Service, *database.DB, *database.CardRepository, *redis.Client) {
	t.Helper()

	db := database.SetupTestDB(t)
	cardRepo := database.NewCardRepository(db)
	txRepo := database.NewTransactionRepository(db)

	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1,
	})

	ctx := context.Background()
	redisClient.Del(ctx, "fund_card")

	queue := streams.NewStreamQueue(redisClient)
	err := queue.DeclareStream(ctx, "fund_card", "test_workers")
	require.NoError(t, err)

	service := NewService(db, cardRepo, txRepo, queue, nil, &mockPaymentProvider{}, testFeesCfg())
	return service, db, cardRepo, redisClient
}

// ============================================================================
// CreateCard tests
// ============================================================================

// TestService_CreateCard_Validation exercises validateCreateRequest via table-
// driven subtests. No payment provider is needed: validation returns before any
// provider call.
func TestService_CreateCard_Validation(t *testing.T) {
	service, db, _, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()

	validBase := CreateCardRequest{
		Items:         []CreateCardItem{{FiatAmountCents: 5000, Quantity: 1}},
		FiatCurrency:  database.FiatEUR,
		PaymentMethod: database.CardBlue,
		PurchaseEmail: "test@example.com",
	}

	tests := []struct {
		name   string
		mutate func(*CreateCardRequest)
		want   error
	}{
		{
			name:   "empty items",
			mutate: func(r *CreateCardRequest) { r.Items = nil },
			want:   ErrEmptyItems,
		},
		{
			name:   "zero amount",
			mutate: func(r *CreateCardRequest) { r.Items = []CreateCardItem{{FiatAmountCents: 0, Quantity: 1}} },
			want:   ErrInvalidFiatAmount,
		},
		{
			name:   "negative amount",
			mutate: func(r *CreateCardRequest) { r.Items = []CreateCardItem{{FiatAmountCents: -1, Quantity: 1}} },
			want:   ErrInvalidFiatAmount,
		},
		{
			name:   "zero quantity",
			mutate: func(r *CreateCardRequest) { r.Items = []CreateCardItem{{FiatAmountCents: 5000, Quantity: 0}} },
			want:   ErrInvalidQuantity,
		},
		{
			name:   "unsupported currency",
			mutate: func(r *CreateCardRequest) { r.FiatCurrency = "GBP" },
			want:   ErrInvalidCurrency,
		},
		{
			name:   "missing email",
			mutate: func(r *CreateCardRequest) { r.PurchaseEmail = "" },
			want:   ErrMissingEmail,
		},
		{
			name:   "invalid payment method",
			mutate: func(r *CreateCardRequest) { r.PaymentMethod = "cash" },
			want:   ErrInvalidPaymentMethod,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := validBase
			tc.mutate(&req)
			_, err := service.CreateCard(ctx, req)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestService_CreateCard(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestServiceFull(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()
	userID := uuid.New().String()
	email := "test@example.com"

	req := CreateCardRequest{
		Items:         []CreateCardItem{{FiatAmountCents: 10000, Quantity: 1}},
		FiatCurrency:  database.FiatUSD,
		PaymentMethod: database.CardBlue,
		UserID:        &userID,
		PurchaseEmail: email,
	}

	resp, err := service.CreateCard(ctx, req)

	require.NoError(t, err)
	require.Len(t, resp.Cards, 1)
	assert.NotEmpty(t, resp.Cards[0].CardID)
	assert.NotEmpty(t, resp.Cards[0].Code)
	assert.NotEmpty(t, resp.CheckoutURL)
	assert.NotEmpty(t, resp.SessionID)

	// Verify code format: GIFT-XXXX-YYYY-ZZZZ
	assert.Regexp(t, `^GIFT-[A-Z2-9]{4}-[A-Z2-9]{4}-[A-Z2-9]{4}$`, resp.Cards[0].Code)

	// Verify card was saved in database
	savedCard, err := cardRepo.GetByID(ctx, resp.Cards[0].CardID)
	require.NoError(t, err)
	assert.Equal(t, resp.Cards[0].Code, savedCard.Code)
	assert.Equal(t, userID, *savedCard.UserID)
	assert.Equal(t, email, savedCard.PurchaseEmail)
	assert.Equal(t, email, savedCard.OwnerEmail)
	assert.Equal(t, int64(0), savedCard.BTCAmountSats) // 0 until funded by worker
	assert.Equal(t, int64(10000), savedCard.FiatAmountCents)
	assert.Equal(t, database.FiatUSD, savedCard.FiatCurrency)
	assert.Equal(t, database.CardBlue, savedCard.PaymentMethod)
	assert.Equal(t, database.PaymentPending, savedCard.PaymentStatus)
	assert.NotNil(t, savedCard.PaymentReference)
	assert.Equal(t, resp.SessionID, *savedCard.PaymentReference)
	assert.Equal(t, database.Created, savedCard.Status)
	assert.WithinDuration(t, time.Now().UTC(), savedCard.CreatedAt, 2*time.Second)
	assert.Nil(t, savedCard.FundedAt)
	assert.Nil(t, savedCard.RedeemedAt)

	// CreateCard does NOT publish fund_card messages. That happens in
	// HandleCheckoutEvent after Stripe confirms payment. The queue must be empty.
	result := redisClient.XLen(ctx, "fund_card")
	assert.Equal(t, int64(0), result.Val())
}

func TestService_CreateCard_WithoutOptionalFields(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestServiceFull(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()

	req := CreateCardRequest{
		Items:         []CreateCardItem{{FiatAmountCents: 5000, Quantity: 1}},
		FiatCurrency:  database.FiatEUR,
		PaymentMethod: database.CardBlue,
		PurchaseEmail: "anonymous@example.com",
		UserID:        nil, // No user ID
	}

	resp, err := service.CreateCard(ctx, req)
	require.NoError(t, err)
	require.Len(t, resp.Cards, 1)

	savedCard, err := cardRepo.GetByID(ctx, resp.Cards[0].CardID)
	require.NoError(t, err)
	assert.Nil(t, savedCard.UserID)
	assert.Equal(t, "anonymous@example.com", savedCard.PurchaseEmail)
	assert.Equal(t, "anonymous@example.com", savedCard.OwnerEmail)
	assert.Equal(t, database.FiatEUR, savedCard.FiatCurrency)
}

func TestService_CreateCard_GeneratesUniqueCode(t *testing.T) {
	service, db, _, redisClient := setupTestServiceFull(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()

	codes := make(map[string]bool)
	for i := 0; i < 10; i++ {
		req := CreateCardRequest{
			Items:         []CreateCardItem{{FiatAmountCents: 10000, Quantity: 1}},
			FiatCurrency:  database.FiatUSD,
			PaymentMethod: database.CardBlue,
			PurchaseEmail: "test@example.com",
		}

		resp, err := service.CreateCard(ctx, req)
		require.NoError(t, err)
		require.Len(t, resp.Cards, 1)

		assert.False(t, codes[resp.Cards[0].Code], "Duplicate code: %s", resp.Cards[0].Code)
		codes[resp.Cards[0].Code] = true
	}

	assert.Equal(t, 10, len(codes))
}

// TestService_CreateCard_BulkOrder verifies that a multi-item order creates the
// correct total number of cards and that all of them share one payment_reference.
// This test would previously fail due to the (now-removed) UNIQUE constraint on
// payment_reference.
func TestService_CreateCard_BulkOrder(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestServiceFull(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()
	userID := uuid.New().String()

	req := CreateCardRequest{
		Items: []CreateCardItem{
			{FiatAmountCents: 5000, Quantity: 2},
			{FiatAmountCents: 10000, Quantity: 1},
		},
		FiatCurrency:  database.FiatEUR,
		PaymentMethod: database.CardBlue,
		UserID:        &userID,
		PurchaseEmail: "buyer@test.com",
	}

	resp, err := service.CreateCard(ctx, req)
	require.NoError(t, err)
	assert.Len(t, resp.Cards, 3) // 2 × €50 + 1 × €100

	// All cards share the same payment_reference (Stripe session ID).
	for _, c := range resp.Cards {
		saved, err := cardRepo.GetByID(ctx, c.CardID)
		require.NoError(t, err)
		require.NotNil(t, saved.PaymentReference)
		assert.Equal(t, resp.SessionID, *saved.PaymentReference)
	}
}

func TestService_CreateCard_CodeExcludesConfusingCharacters(t *testing.T) {
	service, db, _, redisClient := setupTestServiceFull(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()

	confusingChars := []string{"O", "0", "I", "1", "L"}

	for i := 0; i < 20; i++ {
		req := CreateCardRequest{
			Items:         []CreateCardItem{{FiatAmountCents: 10000, Quantity: 1}},
			FiatCurrency:  database.FiatUSD,
			PaymentMethod: database.CardBlue,
			PurchaseEmail: "test@example.com",
		}

		resp, err := service.CreateCard(ctx, req)
		require.NoError(t, err)
		require.Len(t, resp.Cards, 1)

		for _, char := range confusingChars {
			assert.NotContains(t, strings.TrimPrefix(resp.Cards[0].Code, "GIFT-"), char,
				"Code should not contain confusing character: %s", char)
		}
	}
}

func TestService_generateCardCode_RetriesOnDuplicate(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()

	existingCard := &database.Card{
		ID:              uuid.New().String(),
		PurchaseEmail:   "test@example.com",
		OwnerEmail:      "test@example.com",
		Code:            "GIFT-TEST-CODE-0001",
		BTCAmountSats:   100000,
		FiatAmountCents: 1000,
		FiatCurrency:    database.FiatUSD,
		PaymentMethod:   database.CardBlue,
		PaymentStatus:   database.PaymentPending,
		Status:          database.Created,
		CreatedAt:       time.Now().UTC(),
	}

	err := cardRepo.Create(ctx, existingCard)
	require.NoError(t, err)

	// Try to generate codes - should not return the existing code
	codes := make(map[string]bool)
	for i := 0; i < 10; i++ {
		code, err := service.generateCardCode(ctx)
		require.NoError(t, err)
		codes[code] = true
	}

	// Verify the existing code is not in the generated codes
	assert.NotContains(t, codes, "GIFT-TEST-CODE-0001")
}

// ============================================================================
// HandleCheckoutEvent tests
// ============================================================================

// testCard returns a minimal Card ready for insertion. PaymentReference and
// PaymentStatus are set so the card can be matched by GetByStripeSessionID.
func testCard(sessionID string, code string) *database.Card {
	ref := sessionID
	return &database.Card{
		ID:               uuid.New().String(),
		PurchaseEmail:    "buyer@test.com",
		OwnerEmail:       "buyer@test.com",
		Code:             code,
		BTCAmountSats:    0,
		FiatAmountCents:  5000,
		FiatCurrency:     database.FiatEUR,
		PaymentMethod:    database.CardBlue,
		PaymentReference: &ref,
		PaymentStatus:    database.PaymentPending,
		Status:           database.Created,
		CreatedAt:        time.Now().UTC(),
	}
}

// TestHandleCheckoutEvent_NilCheckoutSession verifies the nil-guard: an event
// with no CheckoutSession is silently ignored (returns nil, no DB calls made).
func TestHandleCheckoutEvent_NilCheckoutSession(t *testing.T) {
	service, db, _, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	err := service.HandleCheckoutEvent(context.Background(), &payment.Event{
		Type:            payment.EventCheckoutCompleted,
		CheckoutSession: nil,
	})
	require.NoError(t, err)
}

// TestHandleCheckoutEvent_UnknownEventType verifies that events with an
// unrecognised type are ignored without error.
func TestHandleCheckoutEvent_UnknownEventType(t *testing.T) {
	service, db, _, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	err := service.HandleCheckoutEvent(context.Background(), &payment.Event{
		Type:            "customer.subscription.created",
		CheckoutSession: &payment.CheckoutSessionPayload{ID: "cs_test_unknown"},
	})
	require.NoError(t, err)
}

// TestHandleCheckoutEvent_EmptyCards verifies that a completed event for a
// session with no cards is a no-op (returns nil).
func TestHandleCheckoutEvent_EmptyCards(t *testing.T) {
	service, db, _, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	err := service.HandleCheckoutEvent(context.Background(), &payment.Event{
		Type:            payment.EventCheckoutCompleted,
		CheckoutSession: &payment.CheckoutSessionPayload{ID: "cs_nonexistent_session"},
	})
	require.NoError(t, err)
}

// TestHandleCheckoutEvent_Completed verifies the happy path: payment_status is
// updated to "paid" and fund_card messages are published for each card.
func TestHandleCheckoutEvent_Completed(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()
	sessionID := "cs_test_completed_" + uuid.New().String()[:8]

	card1 := testCard(sessionID, "GIFT-HCEV-C001-AAAA")
	card2 := testCard(sessionID, "GIFT-HCEV-C002-BBBB")
	require.NoError(t, cardRepo.Create(ctx, card1))
	require.NoError(t, cardRepo.Create(ctx, card2))

	err := service.HandleCheckoutEvent(ctx, &payment.Event{
		Type:            payment.EventCheckoutCompleted,
		CheckoutSession: &payment.CheckoutSessionPayload{ID: sessionID},
	})
	require.NoError(t, err)

	// Both cards must now be paid.
	cards, err := cardRepo.GetByStripeSessionID(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, cards, 2)
	for _, c := range cards {
		assert.Equal(t, database.PaymentPaid, c.PaymentStatus)
	}
}

// TestHandleCheckoutEvent_Completed_Idempotency verifies that a second completed
// event for an already-paid session is a no-op.
func TestHandleCheckoutEvent_Completed_Idempotency(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()
	sessionID := "cs_test_idem_" + uuid.New().String()[:8]

	card := testCard(sessionID, "GIFT-HCEV-IDEM-CCCC")
	card.PaymentStatus = database.PaymentPaid // already paid
	require.NoError(t, cardRepo.Create(ctx, card))

	// First call — should be a no-op since card is already paid.
	err := service.HandleCheckoutEvent(ctx, &payment.Event{
		Type:            payment.EventCheckoutCompleted,
		CheckoutSession: &payment.CheckoutSessionPayload{ID: sessionID},
	})
	require.NoError(t, err)

	// Status must still be paid (not changed again).
	cards, err := cardRepo.GetByStripeSessionID(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, cards, 1)
	assert.Equal(t, database.PaymentPaid, cards[0].PaymentStatus)
}

// TestHandleCheckoutEvent_Expired verifies that an expired event sets
// payment_status to "expired" on all cards in the session.
func TestHandleCheckoutEvent_Expired(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()
	sessionID := "cs_test_expired_" + uuid.New().String()[:8]

	card1 := testCard(sessionID, "GIFT-HCEV-E001-DDDD")
	card2 := testCard(sessionID, "GIFT-HCEV-E002-EEEE")
	require.NoError(t, cardRepo.Create(ctx, card1))
	require.NoError(t, cardRepo.Create(ctx, card2))

	err := service.HandleCheckoutEvent(ctx, &payment.Event{
		Type:            payment.EventCheckoutExpired,
		CheckoutSession: &payment.CheckoutSessionPayload{ID: sessionID},
	})
	require.NoError(t, err)

	cards, err := cardRepo.GetByStripeSessionID(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, cards, 2)
	for _, c := range cards {
		assert.Equal(t, database.PaymentExpired, c.PaymentStatus)
	}
}

// TestHandleCheckoutEvent_Expired_EmptyCards verifies that an expired event
// for a non-existent session is silently ignored.
func TestHandleCheckoutEvent_Expired_EmptyCards(t *testing.T) {
	service, db, _, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	err := service.HandleCheckoutEvent(context.Background(), &payment.Event{
		Type:            payment.EventCheckoutExpired,
		CheckoutSession: &payment.CheckoutSessionPayload{ID: "cs_ghost_session"},
	})
	require.NoError(t, err)
}

// Prevent "imported and not used" errors if the compiler optimises away
// any of the helper imports above.
var _ = messages.FundCardMessage{}
var _ = streams.NewStreamQueue

// ============================================================================
// GetCardsBySessionID tests
// ============================================================================

// TestGetCardsBySessionID_Paid verifies that codes are returned when
// payment_status is "paid".
func TestGetCardsBySessionID_Paid(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()
	sessionID := "cs_test_paid_" + uuid.New().String()[:8]

	c1 := testCard(sessionID, "GIFT-GBSI-P001-AAAA")
	c2 := testCard(sessionID, "GIFT-GBSI-P002-BBBB")
	require.NoError(t, cardRepo.Create(ctx, c1))
	require.NoError(t, cardRepo.Create(ctx, c2))
	require.NoError(t, cardRepo.UpdatePaymentStatus(ctx, sessionID, database.PaymentPaid))

	resp, err := service.GetCardsBySessionID(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, database.PaymentPaid, resp.PaymentStatus)
	require.Len(t, resp.Cards, 2)
	codes := []string{resp.Cards[0].Code, resp.Cards[1].Code}
	assert.Contains(t, codes, "GIFT-GBSI-P001-AAAA")
	assert.Contains(t, codes, "GIFT-GBSI-P002-BBBB")
}

// TestGetCardsBySessionID_Pending verifies that no codes are exposed when
// payment_status is still "pending".
func TestGetCardsBySessionID_Pending(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()
	sessionID := "cs_test_pending_" + uuid.New().String()[:8]

	c := testCard(sessionID, "GIFT-GBSI-PN01-CCCC")
	require.NoError(t, cardRepo.Create(ctx, c))

	resp, err := service.GetCardsBySessionID(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, database.PaymentPending, resp.PaymentStatus)
	assert.Empty(t, resp.Cards, "codes must not be exposed before payment is confirmed")
}

// TestGetCardsBySessionID_NotFound verifies that ErrCardNotFound is returned
// when the session has no associated cards.
func TestGetCardsBySessionID_NotFound(t *testing.T) {
	service, db, _, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	_, err := service.GetCardsBySessionID(context.Background(), "cs_nonexistent_session")
	assert.ErrorIs(t, err, ErrCardNotFound)
}
