package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"btc-giftcard/internal/card"
	"btc-giftcard/internal/database"
)

// ============================================================================
// Card handlers
// ============================================================================

// createCard handles POST /api/cards
//
// Request body:
//
//	{
//	  "fiat_amount_cents": 10000,
//	  "fiat_currency": "USD",
//	  "purchase_price_cents": 10500,
//	  "purchase_email": "buyer@mail.com",
//	  "user_id": "optional-uuid"
//	}
//
// Response 201:
//
//	{
//	  "card_id": "uuid",
//	  "code": "GIFT-XXXX-YYYY-ZZZZ",
//	  "btc_amount_sats": 0,
//	  "status": "created",
//	  "created_at": "2026-03-05T12:00:00Z"
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
//	  "lightning_invoice": "lnbc500u1p..."
//	}
//
// Request body (On-chain):
//
//	{
//	  "method": "onchain",
//	  "amount_sats": 100000,
//	  "address": "bc1q..."
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
