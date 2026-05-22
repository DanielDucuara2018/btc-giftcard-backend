package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"btc-giftcard/internal/card"
	"btc-giftcard/internal/database"
	"btc-giftcard/internal/payment"
)

// cardServicer is the interface satisfied by *card.Service.
// Using an interface here decouples HTTP handlers from the concrete service,
// enabling unit testing with mock implementations.
type cardServicer interface {
	CreateCard(ctx context.Context, req card.CreateCardRequest) (*card.CreateCardResponse, error)
	RedeemCard(ctx context.Context, req card.RedeemCardRequest) (*card.RedeemCardResponse, error)
	GetCardByCode(ctx context.Context, code string) (*database.Card, error)
	GetCardsBySessionID(ctx context.Context, sessionID string) (*card.SessionCardsResponse, error)
	GetCardBalance(ctx context.Context, cardID string) (int64, error)
	ValidateCardCode(ctx context.Context, code string) (database.CardStatus, error)
	GetTreasuryAvailableBalance(ctx context.Context) (int64, error)
	HandleCheckoutEvent(ctx context.Context, event *payment.Event) error
}

type stripeClient interface {
	CreateCheckoutSession(ctx context.Context, req payment.CreateCheckoutRequest) (*payment.CheckoutSession, error)
	ConstructEvent(rawBody []byte, sigHeader string) (*payment.Event, error)
}

// ============================================================================
// Card handlers
// ============================================================================

// createCard handles POST /api/cards
//
// Request body:
//
//	{
//	  "items": [
//	    {"fiat_amount_cents": 10000, "quantity": 50},
//	    {"fiat_amount_cents":  5000, "quantity": 20}
//	  ],
//	  "fiat_currency": "EUR",
//	  "purchase_email": "buyer@mail.com",
//	  "user_id": "optional-uuid"
//	}
//
// Response 201:
//
//	{
//	  "cards": [{"card_id": "uuid", "code": "GIFT-XXXX-YYYY-ZZZZ"}, ...],
//	  "checkout_url": "https://checkout.stripe.com/...",
//	  "session_id": "cs_live_...",
//	  "expires_at": "2026-03-05T12:00:00Z"
//	}
func (h *handler) createCard(w http.ResponseWriter, r *http.Request) {
	var req card.CreateCardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON", nil)
		return
	}

	resp, err := h.cardService.CreateCard(r.Context(), req)
	if handleError(w, err) {
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

// redeemCard handles POST /api/cards/{code}/redeem
//
// Request body (Lightning):
//
//	{
//	  "method": "lightning",
//	  "amount_sats": 50000,
//	  "invoice": "lnbc500u1p..."
//	}
//
// Response 200:
//
//	{
//	  "transaction_id": "uuid",
//	  "method": "lightning",
//	  "amount_sats": 50000,
//	  "tx_hash": "abc123...",
//	  "payment_hash": "def456...",
//	  "remaining_balance_sats": 50000,
//	  "card_status": "active"
//	}
func (h *handler) redeemCard(w http.ResponseWriter, r *http.Request) {
	var req card.RedeemCardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON", nil)
		return
	}
	req.Code = r.PathValue("code")

	resp, err := h.cardService.RedeemCard(r.Context(), req)
	if handleError(w, err) {
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// getCard handles GET /api/cards/{code}
//
// Response 200:
//
//	{
//	  "card_id": "uuid",
//	  "code": "GIFT-XXXX-YYYY-ZZZZ",
//	  "btc_amount_sats": 149254,
//	  "fiat_amount_cents": 10000,
//	  "fiat_currency": "USD",
//	  "status": "active",
//	  "created_at": "2026-03-05T12:00:00Z",
//	  "funded_at": "2026-03-05T12:05:00Z"
//	}
func (h *handler) getCard(w http.ResponseWriter, r *http.Request) {
	resp, err := h.cardService.GetCardByCode(r.Context(), r.PathValue("code"))
	if handleError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// getCardsBySession handles GET /api/checkout/sessions/{session_id}
//
// Returns payment status and card codes for a Stripe checkout session.
// Card codes are only included once payment_status is "paid".
//
// Response 200:
//
//	{
//	  "payment_status": "paid",
//	  "cards": [{"card_id": "uuid", "code": "GIFT-XXXX-YYYY-ZZZZ"}]
//	}
func (h *handler) getCardsBySession(w http.ResponseWriter, r *http.Request) {
	resp, err := h.cardService.GetCardsBySessionID(r.Context(), r.PathValue("session_id"))
	if handleError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// getCardBalance handles GET /api/cards/{code}/balance
//
// Resolves card code → card ID, then fetches the balance.
//
// Response 200:
//
//	{
//	  "btc_amount_sats": 149254,
//	  "btc_amount": "0.00149254"
//	}
func (h *handler) getCardBalance(w http.ResponseWriter, r *http.Request) {
	// GetCardBalance takes a cardID, so resolve code → card first.
	c, err := h.cardService.GetCardByCode(r.Context(), r.PathValue("code"))
	if handleError(w, err) {
		return
	}

	sats, err := h.cardService.GetCardBalance(r.Context(), c.ID)
	if handleError(w, err) {
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"btc_amount_sats": sats,
		"btc_amount":      fmt.Sprintf("%.8f", float64(sats)/1e8),
	})
}

// validateCard handles GET /api/cards/{code}/validate
//
// Response 200:
//
//	{
//	  "valid": true,
//	  "status": "active"
//	}
func (h *handler) validateCard(w http.ResponseWriter, r *http.Request) {
	status, err := h.cardService.ValidateCardCode(r.Context(), r.PathValue("code"))
	if err != nil {
		if errors.Is(err, card.ErrCardNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{
				"valid":  false,
				"status": "",
			})
			return
		}
		handleError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"valid":  status == database.Active,
		"status": status,
	})
}

// ============================================================================
// Treasury handlers
// ============================================================================

// getTreasuryBalance handles GET /api/treasury/balance
//
// Response 200:
//
//	{
//	  "available_sats": 500000000,
//	  "available_btc": "5.00000000"
//	}
func (h *handler) getTreasuryBalance(w http.ResponseWriter, r *http.Request) {
	sats, err := h.cardService.GetTreasuryAvailableBalance(r.Context())
	if handleError(w, err) {
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"available_sats": sats,
		"available_btc":  fmt.Sprintf("%.8f", float64(sats)/1e8),
	})
}

// ============================================================================
// Health check
// ============================================================================

// healthCheck handles GET /health
//
// Response 200:
//
//	{"status": "ok"}
func (h *handler) healthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
