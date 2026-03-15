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
	getCardBalance              func(context.Context, string) (int64, error)
	validateCardCode            func(context.Context, string) (database.CardStatus, error)
	getTreasuryAvailableBalance func(context.Context) (int64, error)
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
func (m *mockCardService) GetCardBalance(ctx context.Context, cardID string) (int64, error) {
	return m.getCardBalance(ctx, cardID)
}
func (m *mockCardService) ValidateCardCode(ctx context.Context, code string) (database.CardStatus, error) {
	return m.validateCardCode(ctx, code)
}
func (m *mockCardService) GetTreasuryAvailableBalance(ctx context.Context) (int64, error) {
	return m.getTreasuryAvailableBalance(ctx)
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
	createdAt := time.Now().UTC()

	svc := &mockCardService{
		createCard: func(_ context.Context, req card.CreateCardRequest) (*card.CreateCardResponse, error) {
			assert.Equal(t, int64(10000), req.FiatAmountCents)
			assert.Equal(t, card.CreateCardFiatCurrency("USD"), req.FiatCurrency)
			return &card.CreateCardResponse{
				CardID:        cardID,
				Code:          code,
				BTCAmountSats: 0,
				Status:        database.Created,
				CreatedAt:     createdAt,
			}, nil
		},
	}

	body := `{"FiatAmountCents":10000,"FiatCurrency":"USD","PurchasePriceCents":10500,"PurchaseEmail":"buyer@test.com"}`
	r := httptest.NewRequest("POST", "/api/cards", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	newTestHandler(svc).createCard(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp card.CreateCardResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, cardID, resp.CardID)
	assert.Equal(t, code, resp.Code)
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

	body := `{"FiatAmountCents":10000,"FiatCurrency":"XYZ","PurchasePriceCents":10500,"PurchaseEmail":"a@b.com"}`
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

	body := `{"FiatAmountCents":10000,"FiatCurrency":"USD","PurchasePriceCents":10500,"PurchaseEmail":"a@b.com"}`
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
