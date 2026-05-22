package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"btc-giftcard/internal/card"
	"btc-giftcard/internal/database"
	"btc-giftcard/internal/payment"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Mock card service
// ============================================================================

type mockCardService struct {
	createCard                  func(context.Context, card.CreateCardRequest) (*card.CreateCardResponse, error)
	redeemCard                  func(context.Context, card.RedeemCardRequest) (*card.RedeemCardResponse, error)
	getCardByCode               func(context.Context, string) (*database.Card, error)
	getCardsBySessionID         func(context.Context, string) (*card.SessionCardsResponse, error)
	getCardBalance              func(context.Context, string) (int64, error)
	validateCardCode            func(context.Context, string) (database.CardStatus, error)
	getTreasuryAvailableBalance func(context.Context) (int64, error)
	handleCheckoutEvent         func(context.Context, *payment.Event) error
}

func (m *mockCardService) CreateCard(ctx context.Context, req card.CreateCardRequest) (*card.CreateCardResponse, error) {
	return m.createCard(ctx, req)
}
func (m *mockCardService) RedeemCard(ctx context.Context, req card.RedeemCardRequest) (*card.RedeemCardResponse, error) {
	return m.redeemCard(ctx, req)
}
func (m *mockCardService) GetCardByCode(ctx context.Context, code string) (*database.Card, error) {
	return m.getCardByCode(ctx, code)
}
func (m *mockCardService) GetCardsBySessionID(ctx context.Context, sessionID string) (*card.SessionCardsResponse, error) {
	return m.getCardsBySessionID(ctx, sessionID)
}
func (m *mockCardService) GetCardBalance(ctx context.Context, cardID string) (int64, error) {
	return m.getCardBalance(ctx, cardID)
}
func (m *mockCardService) ValidateCardCode(ctx context.Context, code string) (database.CardStatus, error) {
	return m.validateCardCode(ctx, code)
}
func (m *mockCardService) GetTreasuryAvailableBalance(ctx context.Context) (int64, error) {
	return m.getTreasuryAvailableBalance(ctx)
}
func (m *mockCardService) HandleCheckoutEvent(ctx context.Context, event *payment.Event) error {
	return m.handleCheckoutEvent(ctx, event)
}

// newTestHandler builds a handler with the given mock service.
func newTestHandler(svc cardServicer) *handler {
	return &handler{cardService: svc}
}

// ============================================================================
// createCard tests
// ============================================================================

func TestCreateCard_Success(t *testing.T) {
	cardID := "card-uuid-1234"
	code := "GIFT-TEST-ABCD-1234"
	expiresAt := time.Now().Add(24 * time.Hour).UTC()

	svc := &mockCardService{
		createCard: func(_ context.Context, req card.CreateCardRequest) (*card.CreateCardResponse, error) {
			assert.Equal(t, 1, len(req.Items))
			assert.Equal(t, int64(10000), req.Items[0].FiatAmountCents)
			assert.Equal(t, 1, req.Items[0].Quantity)
			assert.Equal(t, database.FiatCurrency("EUR"), req.FiatCurrency)
			return &card.CreateCardResponse{
				Cards:       []card.CreatedCard{{CardID: cardID, Code: code}},
				CheckoutURL: "https://checkout.stripe.com/test",
				SessionID:   "cs_test_123",
				ExpiresAt:   expiresAt,
			}, nil
		},
	}

	body := `{"items":[{"fiat_amount_cents":10000,"quantity":1}],"fiat_currency":"EUR","purchase_email":"buyer@test.com"}`
	r := httptest.NewRequest("POST", "/api/cards", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	newTestHandler(svc).createCard(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp card.CreateCardResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, len(resp.Cards))
	assert.Equal(t, cardID, resp.Cards[0].CardID)
	assert.Equal(t, code, resp.Cards[0].Code)
	assert.Equal(t, "https://checkout.stripe.com/test", resp.CheckoutURL)
}

func TestCreateCard_BadJSON(t *testing.T) {
	svc := &mockCardService{}
	r := httptest.NewRequest("POST", "/api/cards", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()

	newTestHandler(svc).createCard(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body APIError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "invalid JSON", body.Message)
}

func TestCreateCard_ServiceValidationError(t *testing.T) {
	svc := &mockCardService{
		createCard: func(_ context.Context, req card.CreateCardRequest) (*card.CreateCardResponse, error) {
			// Simulate the service's enum validation: "XYZ" is not a known currency.
			if !req.FiatCurrency.IsValid() {
				return nil, card.ErrInvalidCurrency
			}
			return nil, nil
		},
	}

	body := `{"fiat_amount_cents":10000,"fiat_currency":"XYZ","purchase_email":"a@b.com"}`
	r := httptest.NewRequest("POST", "/api/cards", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	newTestHandler(svc).createCard(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateCard_ServiceUnknownError(t *testing.T) {
	svc := &mockCardService{
		createCard: func(_ context.Context, _ card.CreateCardRequest) (*card.CreateCardResponse, error) {
			return nil, fmt.Errorf("database unavailable")
		},
	}

	body := `{"fiat_amount_cents":10000,"fiat_currency":"USD","purchase_email":"a@b.com"}`
	r := httptest.NewRequest("POST", "/api/cards", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	newTestHandler(svc).createCard(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ============================================================================
// redeemCard tests
// ============================================================================

func TestRedeemCard_Success(t *testing.T) {
	txID := "tx-uuid-5678"
	method := "lightning"
	payHash := "abc123"

	svc := &mockCardService{
		redeemCard: func(_ context.Context, req card.RedeemCardRequest) (*card.RedeemCardResponse, error) {
			assert.Equal(t, "GIFT-TEST-ABCD-1234", req.Code)
			assert.Equal(t, card.Lightning, req.Method)
			return &card.RedeemCardResponse{
				TransactionID:    txID,
				Method:           method,
				PaymentHash:      &payHash,
				BTCAmountSats:    50000,
				RemainingBalance: 50000,
				Status:           database.Confirmed,
			}, nil
		},
	}

	body := `{"method":"lightning","amount_sats":50000,"lightning_invoice":"lnbc500u1ptest"}`
	r := httptest.NewRequest("POST", "/api/cards/GIFT-TEST-ABCD-1234/redeem", bytes.NewBufferString(body))
	r.SetPathValue("code", "GIFT-TEST-ABCD-1234")
	w := httptest.NewRecorder()

	newTestHandler(svc).redeemCard(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp card.RedeemCardResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, txID, resp.TransactionID)
}

func TestRedeemCard_BadJSON(t *testing.T) {
	svc := &mockCardService{}
	r := httptest.NewRequest("POST", "/api/cards/GIFT-TEST/redeem", bytes.NewBufferString("!!!"))
	r.SetPathValue("code", "GIFT-TEST")
	w := httptest.NewRecorder()

	newTestHandler(svc).redeemCard(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRedeemCard_CardNotFound(t *testing.T) {
	svc := &mockCardService{
		redeemCard: func(_ context.Context, _ card.RedeemCardRequest) (*card.RedeemCardResponse, error) {
			return nil, card.ErrCardNotFound
		},
	}

	body := `{"method":"lightning","amount_sats":50000,"lightning_invoice":"lnbc500u1p"}`
	r := httptest.NewRequest("POST", "/api/cards/GIFT-XXXX/redeem", bytes.NewBufferString(body))
	r.SetPathValue("code", "GIFT-XXXX")
	w := httptest.NewRecorder()

	newTestHandler(svc).redeemCard(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRedeemCard_CardNotActive(t *testing.T) {
	svc := &mockCardService{
		redeemCard: func(_ context.Context, _ card.RedeemCardRequest) (*card.RedeemCardResponse, error) {
			return nil, card.ErrCardNotActive
		},
	}

	body := `{"method":"lightning","amount_sats":50000,"lightning_invoice":"lnbc500u1p"}`
	r := httptest.NewRequest("POST", "/api/cards/GIFT-XXXX/redeem", bytes.NewBufferString(body))
	r.SetPathValue("code", "GIFT-XXXX")
	w := httptest.NewRecorder()

	newTestHandler(svc).redeemCard(w, r)

	assert.Equal(t, http.StatusConflict, w.Code)
}

// ============================================================================
// getCard tests
// ============================================================================

func TestGetCard_Success(t *testing.T) {
	cardID := "card-uuid"
	code := "GIFT-TEST-1234-ABCD"
	svc := &mockCardService{
		getCardByCode: func(_ context.Context, c string) (*database.Card, error) {
			assert.Equal(t, code, c)
			return &database.Card{
				ID:              cardID,
				Code:            code,
				BTCAmountSats:   149254,
				FiatAmountCents: 10000,
				FiatCurrency:    "USD",
				Status:          database.Active,
				CreatedAt:       time.Now().UTC(),
			}, nil
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/"+code, nil)
	r.SetPathValue("code", code)
	w := httptest.NewRecorder()

	newTestHandler(svc).getCard(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp database.Card
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, cardID, resp.ID)
}

func TestGetCard_NotFound(t *testing.T) {
	svc := &mockCardService{
		getCardByCode: func(_ context.Context, _ string) (*database.Card, error) {
			return nil, card.ErrCardNotFound
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/GIFT-NONE", nil)
	r.SetPathValue("code", "GIFT-NONE")
	w := httptest.NewRecorder()

	newTestHandler(svc).getCard(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ============================================================================
// getCardsBySession tests
// ============================================================================

func TestGetCardsBySession_Paid(t *testing.T) {
	sessionID := "cs_test_paid_001"
	svc := &mockCardService{
		getCardsBySessionID: func(_ context.Context, id string) (*card.SessionCardsResponse, error) {
			assert.Equal(t, sessionID, id)
			return &card.SessionCardsResponse{
				PaymentStatus: database.PaymentPaid,
				Cards: []card.CreatedCard{
					{CardID: "card-uuid-1", Code: "GIFT-TEST-AAAA-0001"},
					{CardID: "card-uuid-2", Code: "GIFT-TEST-BBBB-0002"},
				},
			}, nil
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/session/"+sessionID, nil)
	r.SetPathValue("session_id", sessionID)
	w := httptest.NewRecorder()

	newTestHandler(svc).getCardsBySession(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp card.SessionCardsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, database.PaymentPaid, resp.PaymentStatus)
	assert.Len(t, resp.Cards, 2)
	assert.Equal(t, "GIFT-TEST-AAAA-0001", resp.Cards[0].Code)
}

func TestGetCardsBySession_Pending(t *testing.T) {
	sessionID := "cs_test_pending_001"
	svc := &mockCardService{
		getCardsBySessionID: func(_ context.Context, _ string) (*card.SessionCardsResponse, error) {
			return &card.SessionCardsResponse{
				PaymentStatus: database.PaymentPending,
				Cards:         nil,
			}, nil
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/session/"+sessionID, nil)
	r.SetPathValue("session_id", sessionID)
	w := httptest.NewRecorder()

	newTestHandler(svc).getCardsBySession(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp card.SessionCardsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, database.PaymentPending, resp.PaymentStatus)
	assert.Empty(t, resp.Cards)
}

func TestGetCardsBySession_NotFound(t *testing.T) {
	svc := &mockCardService{
		getCardsBySessionID: func(_ context.Context, _ string) (*card.SessionCardsResponse, error) {
			return nil, card.ErrCardNotFound
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/session/cs_unknown", nil)
	r.SetPathValue("session_id", "cs_unknown")
	w := httptest.NewRecorder()

	newTestHandler(svc).getCardsBySession(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetCardsBySession_ServiceError(t *testing.T) {
	svc := &mockCardService{
		getCardsBySessionID: func(_ context.Context, _ string) (*card.SessionCardsResponse, error) {
			return nil, fmt.Errorf("db connection error")
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/session/cs_error", nil)
	r.SetPathValue("session_id", "cs_error")
	w := httptest.NewRecorder()

	newTestHandler(svc).getCardsBySession(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ============================================================================
// getCardBalance tests
// ============================================================================

func TestGetCardBalance_Success(t *testing.T) {
	cardID := "card-uuid-bal"
	code := "GIFT-BAL-TEST-5678"

	svc := &mockCardService{
		getCardByCode: func(_ context.Context, _ string) (*database.Card, error) {
			return &database.Card{ID: cardID, Code: code}, nil
		},
		getCardBalance: func(_ context.Context, id string) (int64, error) {
			assert.Equal(t, cardID, id)
			return 149254, nil
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/"+code+"/balance", nil)
	r.SetPathValue("code", code)
	w := httptest.NewRecorder()

	newTestHandler(svc).getCardBalance(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(149254), resp["btc_amount_sats"])
	assert.Equal(t, "0.00149254", resp["btc_amount"])
}

func TestGetCardBalance_CardNotFound(t *testing.T) {
	svc := &mockCardService{
		getCardByCode: func(_ context.Context, _ string) (*database.Card, error) {
			return nil, card.ErrCardNotFound
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/GIFT-NONE/balance", nil)
	r.SetPathValue("code", "GIFT-NONE")
	w := httptest.NewRecorder()

	newTestHandler(svc).getCardBalance(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ============================================================================
// validateCard tests
// ============================================================================

func TestValidateCard_ActiveCard(t *testing.T) {
	svc := &mockCardService{
		validateCardCode: func(_ context.Context, _ string) (database.CardStatus, error) {
			return database.Active, nil
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/GIFT-VALID/validate", nil)
	r.SetPathValue("code", "GIFT-VALID")
	w := httptest.NewRecorder()

	newTestHandler(svc).validateCard(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["valid"].(bool))
	assert.Equal(t, string(database.Active), resp["status"])
}

func TestValidateCard_RedeemedCard(t *testing.T) {
	svc := &mockCardService{
		validateCardCode: func(_ context.Context, _ string) (database.CardStatus, error) {
			return database.Redeemed, nil
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/GIFT-USED/validate", nil)
	r.SetPathValue("code", "GIFT-USED")
	w := httptest.NewRecorder()

	newTestHandler(svc).validateCard(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["valid"].(bool), "redeemed card should not be valid")
}

func TestValidateCard_NotFound_ReturnsValidFalse(t *testing.T) {
	// ErrCardNotFound is special: we return 200 with valid=false instead of 404.
	svc := &mockCardService{
		validateCardCode: func(_ context.Context, _ string) (database.CardStatus, error) {
			return database.Expired, card.ErrCardNotFound
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/GIFT-NONE/validate", nil)
	r.SetPathValue("code", "GIFT-NONE")
	w := httptest.NewRecorder()

	newTestHandler(svc).validateCard(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["valid"].(bool))
	assert.Equal(t, "", resp["status"])
}

func TestValidateCard_ServiceError(t *testing.T) {
	svc := &mockCardService{
		validateCardCode: func(_ context.Context, _ string) (database.CardStatus, error) {
			return "", fmt.Errorf("db connection error")
		},
	}

	r := httptest.NewRequest("GET", "/api/cards/GIFT-ERR/validate", nil)
	r.SetPathValue("code", "GIFT-ERR")
	w := httptest.NewRecorder()

	newTestHandler(svc).validateCard(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ============================================================================
// getTreasuryBalance tests
// ============================================================================

func TestGetTreasuryBalance_Success(t *testing.T) {
	svc := &mockCardService{
		getTreasuryAvailableBalance: func(_ context.Context) (int64, error) {
			return 500_000_000, nil
		},
	}

	r := httptest.NewRequest("GET", "/api/treasury/balance", nil)
	w := httptest.NewRecorder()

	newTestHandler(svc).getTreasuryBalance(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(500_000_000), resp["available_sats"])
	assert.Equal(t, "5.00000000", resp["available_btc"])
}

func TestGetTreasuryBalance_ServiceError(t *testing.T) {
	svc := &mockCardService{
		getTreasuryAvailableBalance: func(_ context.Context) (int64, error) {
			return 0, fmt.Errorf("LND unavailable")
		},
	}

	r := httptest.NewRequest("GET", "/api/treasury/balance", nil)
	w := httptest.NewRecorder()

	newTestHandler(svc).getTreasuryBalance(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ============================================================================
// healthCheck tests
// ============================================================================

func TestHealthCheck_ReturnsOK(t *testing.T) {
	svc := &mockCardService{}
	r := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	newTestHandler(svc).healthCheck(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
}

// ============================================================================
// Mock stripe client
// ============================================================================

type mockStripeClient struct {
	constructEvent        func(rawBody []byte, sigHeader string) (*payment.Event, error)
	createCheckoutSession func(ctx context.Context, req payment.CreateCheckoutRequest) (*payment.CheckoutSession, error)
}

func (m *mockStripeClient) ConstructEvent(rawBody []byte, sigHeader string) (*payment.Event, error) {
	if m.constructEvent != nil {
		return m.constructEvent(rawBody, sigHeader)
	}
	return nil, nil
}

func (m *mockStripeClient) CreateCheckoutSession(ctx context.Context, req payment.CreateCheckoutRequest) (*payment.CheckoutSession, error) {
	if m.createCheckoutSession != nil {
		return m.createCheckoutSession(ctx, req)
	}
	return nil, nil
}

// ============================================================================
// cardPayment webhook handler tests
// ============================================================================

// TestCardPayment_ConstructEventError verifies that a signature verification
// failure stops processing and writes a 500 response (Stripe errors are not in
// the known errorStatusMap so they fall through to the generic 500 handler).
func TestCardPayment_ConstructEventError(t *testing.T) {
	sc := &mockStripeClient{
		constructEvent: func(_ []byte, _ string) (*payment.Event, error) {
			return nil, fmt.Errorf("invalid webhook signature")
		},
	}
	h := &handler{cardService: &mockCardService{}, stripeClient: sc}
	r := httptest.NewRequest("POST", "/webhook/stripe", bytes.NewBufferString(`{}`))
	r.Header.Set("Stripe-Signature", "t=1,v1=badhash")
	w := httptest.NewRecorder()

	h.cardPayment(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestCardPayment_ServiceError_StillReturns200 verifies the always-200 contract:
// even when HandleCheckoutEvent fails, the handler returns 200 to prevent Stripe
// from retrying a permanently-broken event.
func TestCardPayment_ServiceError_StillReturns200(t *testing.T) {
	sc := &mockStripeClient{
		constructEvent: func(_ []byte, _ string) (*payment.Event, error) {
			return &payment.Event{
				Type:            payment.EventCheckoutCompleted,
				CheckoutSession: &payment.CheckoutSessionPayload{ID: "cs_test_abc"},
			}, nil
		},
	}
	svc := &mockCardService{
		handleCheckoutEvent: func(_ context.Context, _ *payment.Event) error {
			return fmt.Errorf("database unavailable")
		},
	}
	h := &handler{cardService: svc, stripeClient: sc}
	r := httptest.NewRequest("POST", "/webhook/stripe", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()

	h.cardPayment(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String(), "response body must be empty on success path")
}

// TestCardPayment_CompletedEvent_Success verifies the happy path for a completed
// checkout event: 200 with no response body.
func TestCardPayment_CompletedEvent_Success(t *testing.T) {
	sessionID := "cs_test_success_001"
	sc := &mockStripeClient{
		constructEvent: func(_ []byte, _ string) (*payment.Event, error) {
			return &payment.Event{
				Type:            payment.EventCheckoutCompleted,
				CheckoutSession: &payment.CheckoutSessionPayload{ID: sessionID},
			}, nil
		},
	}
	svc := &mockCardService{
		handleCheckoutEvent: func(_ context.Context, e *payment.Event) error {
			assert.Equal(t, sessionID, e.CheckoutSession.ID)
			return nil
		},
	}
	h := &handler{cardService: svc, stripeClient: sc}
	r := httptest.NewRequest("POST", "/webhook/stripe", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()

	h.cardPayment(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
}

// TestCardPayment_UnknownEventType_Returns200 verifies that events with no
// CheckoutSession (e.g. payment_intent.created) are forwarded to the service
// and still return 200.
func TestCardPayment_UnknownEventType_Returns200(t *testing.T) {
	sc := &mockStripeClient{
		constructEvent: func(_ []byte, _ string) (*payment.Event, error) {
			return &payment.Event{
				Type:            "payment_intent.created",
				CheckoutSession: nil,
			}, nil
		},
	}
	svc := &mockCardService{
		handleCheckoutEvent: func(_ context.Context, e *payment.Event) error {
			assert.Nil(t, e.CheckoutSession)
			return nil
		},
	}
	h := &handler{cardService: svc, stripeClient: sc}
	r := httptest.NewRequest("POST", "/webhook/stripe", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()

	h.cardPayment(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}
