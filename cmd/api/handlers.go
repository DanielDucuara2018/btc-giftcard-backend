package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"btc-giftcard/internal/card"
	"btc-giftcard/internal/database"
	"btc-giftcard/pkg/logger"

	"go.uber.org/zap"
)

// ============================================================================
// Error mapping + response helpers
// ============================================================================

// errorStatusMap maps known service errors to HTTP status codes.
// Looked up via errors.Is (not direct key access) so wrapped errors
// like fmt.Errorf("...: %w", ErrCardNotFound) are matched correctly.
var errorStatusMap = map[error]int{
	card.ErrInvalidCurrency:   http.StatusBadRequest,
	card.ErrInvalidFiatAmount: http.StatusBadRequest,
	card.ErrInvalidPurchase:   http.StatusBadRequest,
	card.ErrMissingEmail:      http.StatusBadRequest,
	card.ErrCardNotFound:      http.StatusNotFound,
	card.ErrCardNotActive:     http.StatusConflict,
	card.ErrCardAlreadyUsed:   http.StatusConflict,
	card.ErrInsufficientFunds: http.StatusUnprocessableEntity,
	card.ErrInvalidMethod:     http.StatusBadRequest,
	card.ErrInvalidAddress:    http.StatusBadRequest,
	card.ErrLightningInvoice:  http.StatusBadRequest,
}

// APIError is the standard JSON error envelope returned by all endpoints.
type APIError struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Errors  map[string]string `json:"errors,omitempty"`
}

// Respond writes a JSON response with the given status code.
func (h *handler) Respond(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// RespondError writes a structured JSON error response.
func (h *handler) RespondError(w http.ResponseWriter, status int, msg string, fieldErrors map[string]string) {
	h.Respond(w, status, APIError{
		Code:    status,
		Message: msg,
		Errors:  fieldErrors,
	})
}

// handleError maps a service error to an HTTP error response.
// Returns true if an error was handled (response was written), false if err is nil.
func (h *handler) handleError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	for target, status := range errorStatusMap {
		if errors.Is(err, target) {
			h.RespondError(w, status, err.Error(), nil)
			return true
		}
	}
	logger.Error("unexpected error", zap.Error(err))
	h.RespondError(w, http.StatusInternalServerError, "internal server error", nil)
	return true
}

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
		h.RespondError(w, http.StatusBadRequest, "invalid JSON", nil)
		return
	}

	resp, err := h.cardService.CreateCard(r.Context(), req)
	if h.handleError(w, err) {
		return
	}

	h.Respond(w, http.StatusCreated, resp)
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
		h.RespondError(w, http.StatusBadRequest, "invalid JSON", nil)
		return
	}
	req.Code = r.PathValue("code")

	resp, err := h.cardService.RedeemCard(r.Context(), req)
	if h.handleError(w, err) {
		return
	}

	h.Respond(w, http.StatusOK, resp)
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
	if h.handleError(w, err) {
		return
	}
	h.Respond(w, http.StatusOK, resp)
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
	if h.handleError(w, err) {
		return
	}

	sats, err := h.cardService.GetCardBalance(r.Context(), c.ID)
	if h.handleError(w, err) {
		return
	}

	h.Respond(w, http.StatusOK, map[string]any{
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
			h.Respond(w, http.StatusOK, map[string]any{
				"valid":  false,
				"status": "",
			})
			return
		}
		h.handleError(w, err)
		return
	}

	h.Respond(w, http.StatusOK, map[string]any{
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
	if h.handleError(w, err) {
		return
	}

	h.Respond(w, http.StatusOK, map[string]any{
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
	h.Respond(w, http.StatusOK, map[string]string{"status": "ok"})
}
