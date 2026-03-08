package main

import "net/http"

// ============================================================================
// Card handlers
// ============================================================================

// createCard handles POST /api/cards
//
// Request body (JSON):
//
//	{
//	  "fiat_amount_cents": 10000,         // Face value in cents ($100.00)
//	  "fiat_currency": "USD",             // "USD" or "EUR"
//	  "purchase_price_cents": 10500,      // Total charged including platform fee
//	  "purchase_email": "buyer@mail.com", // Email for card delivery + verification
//	  "user_id": "optional-uuid"          // Optional, links card to a user account
//	}
//
// Response (201 Created):
//
//	{
//	  "card_id": "uuid",
//	  "code": "GIFT-XXXX-YYYY-ZZZZ",
//	  "btc_amount_sats": 0,
//	  "status": "created",
//	  "created_at": "2026-03-05T12:00:00Z"
//	}
//
// Implementation steps:
//  1. Parse and validate JSON request body into card.CreateCardRequest
//  2. Call h.cardService.CreateCard(ctx, req)
//  3. Map card.CreateCardResponse to JSON response
//  4. Return 201 with JSON body
//
// Error mapping:
//   - card.ErrInvalidCurrency    → 400 Bad Request
//   - card.ErrInvalidFiatAmount  → 400 Bad Request
//   - card.ErrInvalidPurchase    → 400 Bad Request
//   - card.ErrMissingEmail       → 400 Bad Request
//   - any other error            → 500 Internal Server Error
func (h *handler) createCard(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement — see steps and error mapping above.
	panic("createCard not implemented")
}

// redeemCard handles POST /api/cards/{code}/redeem
//
// Request body (JSON):
//
//	Lightning:
//	{
//	  "method": "lightning",
//	  "amount_sats": 50000,
//	  "lightning_invoice": "lnbc500u1p..."
//	}
//
//	On-chain:
//	{
//	  "method": "onchain",
//	  "amount_sats": 100000,
//	  "address": "bc1q..."
//	}
//
// Response (200 OK):
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
//
// Implementation steps:
//  1. Extract {code} from URL path parameter
//  2. Parse and validate JSON request body into card.RedeemCardRequest
//  3. Set req.Code from the URL path parameter
//  4. Call h.cardService.RedeemCard(ctx, req)
//  5. Map card.RedeemCardResponse to JSON response
//  6. Return 200 with JSON body
//
// Error mapping:
//   - card.ErrCardNotFound       → 404 Not Found
//   - card.ErrCardNotActive      → 409 Conflict
//   - card.ErrCardAlreadyUsed    → 409 Conflict
//   - card.ErrInsufficientFunds  → 422 Unprocessable Entity
//   - card.ErrInvalidMethod      → 400 Bad Request
//   - card.ErrInvalidAddress     → 400 Bad Request
//   - card.ErrLightningInvoice   → 400 Bad Request
//   - any other error            → 500 Internal Server Error
func (h *handler) redeemCard(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement — see steps and error mapping above.
	panic("redeemCard not implemented")
}

// getCard handles GET /api/cards/{code}
//
// Response (200 OK):
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
//
// Implementation steps:
//  1. Extract {code} from URL path parameter
//  2. Call h.cardService.GetCardByCode(ctx, code)
//  3. Map *database.Card to JSON response (exclude sensitive fields like owner_email)
//  4. Return 200 with JSON body
//
// Error mapping:
//   - card.ErrCardNotFound → 404 Not Found
//   - any other error      → 500 Internal Server Error
func (h *handler) getCard(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement — see steps and error mapping above.
	panic("getCard not implemented")
}

// getCardBalance handles GET /api/cards/{code}/balance
//
// Response (200 OK):
//
//	{
//	  "btc_amount_sats": 149254,
//	  "btc_amount": "0.00149254"
//	}
//
// Implementation steps:
//  1. Extract {code} from URL path parameter
//  2. Call h.cardService.GetCardBalance(ctx, code)
//  3. Return balance as JSON
//
// Error mapping:
//   - card.ErrCardNotFound → 404 Not Found
//   - any other error      → 500 Internal Server Error
func (h *handler) getCardBalance(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement — see steps and error mapping above.
	panic("getCardBalance not implemented")
}

// validateCard handles GET /api/cards/{code}/validate
//
// Response (200 OK):
//
//	{
//	  "valid": true,
//	  "status": "active"
//	}
//
// Implementation steps:
//  1. Extract {code} from URL path parameter
//  2. Call h.cardService.ValidateCardCode(ctx, code)
//  3. Return validation result as JSON
//
// Error mapping:
//   - card.ErrCardNotFound → 404 Not Found (or {"valid": false})
//   - any other error      → 500 Internal Server Error
func (h *handler) validateCard(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement — see steps and error mapping above.
	panic("validateCard not implemented")
}

// ============================================================================
// Treasury handlers
// ============================================================================

// getTreasuryBalance handles GET /api/treasury/balance
//
// Response (200 OK):
//
//	{
//	  "available_sats": 500000000,
//	  "available_btc": "5.00000000"
//	}
//
// Implementation steps:
//  1. Call h.cardService.GetTreasuryAvailableBalance(ctx)
//  2. Return balance as JSON
//
// Error mapping:
//   - any error → 500 Internal Server Error
func (h *handler) getTreasuryBalance(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement — see steps above.
	panic("getTreasuryBalance not implemented")
}

// ============================================================================
// Health check
// ============================================================================

// healthCheck handles GET /health
//
// Response (200 OK):
//
//	{
//	  "status": "ok"
//	}
//
// Implementation steps:
//  1. Optionally ping database and Redis to verify connectivity
//  2. Return {"status": "ok"} if healthy, 503 if not
func (h *handler) healthCheck(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement — see steps above.
	panic("healthCheck not implemented")
}
