package main

import (
	"btc-giftcard/internal/card"
	"errors"
	"net/http"
)

// ============================================================================
// Middleware
// ============================================================================

// loggingMiddleware logs every request: method, path, status code, duration.
//
// Implementation steps:
//  1. Record start time
//  2. Wrap http.ResponseWriter to capture the status code
//  3. Call next.ServeHTTP(wrappedWriter, r)
//  4. Log: method, path, status, duration using logger.Info
func loggingMiddleware(next http.Handler) http.Handler {
	// TODO: Implement — see steps above.
	panic("loggingMiddleware not implemented")
}

// recoveryMiddleware catches panics in handlers and returns 500.
//
// Implementation steps:
//  1. Defer a recover() call
//  2. If panic recovered, log the error and stack trace
//  3. Return 500 Internal Server Error JSON response
//  4. Call next.ServeHTTP(w, r)
func recoveryMiddleware(next http.Handler) http.Handler {
	// TODO: Implement — see steps above.
	panic("recoveryMiddleware not implemented")
}

// corsMiddleware sets CORS headers for cross-origin requests.
//
// Implementation steps:
//  1. Set Access-Control-Allow-Origin (configurable, default "*" for dev)
//  2. Set Access-Control-Allow-Methods: GET, POST, OPTIONS
//  3. Set Access-Control-Allow-Headers: Content-Type, Authorization
//  4. If OPTIONS preflight, return 204 immediately
//  5. Otherwise call next.ServeHTTP(w, r)
func corsMiddleware(next http.Handler) http.Handler {
	// TODO: Implement — see steps above.
	panic("corsMiddleware not implemented")
}

// ============================================================================
// Error response helpers
// ============================================================================

// errorResponse is the standard JSON error body returned by all endpoints.
//
//	{
//	  "error": "human-readable error message"
//	}
type errorResponse struct {
	Error string `json:"error"`
}

// mapServiceError translates card.Service sentinel errors to HTTP status codes.
//
// Mapping:
//
//	card.ErrCardNotFound        → 404
//	card.ErrCardNotActive       → 409
//	card.ErrCardAlreadyUsed     → 409
//	card.ErrInsufficientFunds   → 422
//	card.ErrInsufficientBalance → 503 (treasury issue)
//	card.ErrTreasuryLockBusy    → 503
//	card.ErrInvalidMethod       → 400
//	card.ErrInvalidAddress      → 400
//	card.ErrLightningInvoice    → 400
//	card.ErrInvalidCurrency     → 400
//	card.ErrInvalidFiatAmount   → 400
//	card.ErrInvalidPurchase     → 400
//	card.ErrMissingEmail        → 400
//	(unknown)                   → 500
//
// Implementation steps:
//  1. Use errors.Is() to check each sentinel error
//  2. Return the appropriate HTTP status code
//  3. For unknown errors, return 500 and log the full error (don't expose internals)
func mapServiceError(err error) int {
	// TODO: Implement — see mapping above.
	_ = errors.Is(err, card.ErrCardNotFound) // placeholder to keep import
	return http.StatusInternalServerError
}

// writeJSON writes a JSON response with the given status code.
//
// Implementation steps:
//  1. Set Content-Type: application/json
//  2. Write the status code
//  3. Marshal v to JSON and write to w
//  4. If marshal fails, log the error and write a generic 500
func writeJSON(w http.ResponseWriter, status int, v any) {
	// TODO: Implement — see steps above.
	panic("writeJSON not implemented")
}

// writeError writes a JSON error response using mapServiceError for the status code.
//
// Implementation steps:
//  1. Determine HTTP status from mapServiceError(err)
//  2. Call writeJSON(w, status, errorResponse{Error: err.Error()})
//  3. For 500 errors, use a generic message instead of exposing internal details
func writeError(w http.ResponseWriter, err error) {
	// TODO: Implement — see steps above.
	panic("writeError not implemented")
}
