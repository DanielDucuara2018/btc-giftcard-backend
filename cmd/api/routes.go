package main

import (
	"net/http"
)

// handler holds dependencies for all HTTP endpoint handlers.
type handler struct {
	cardService cardServicer
}

func newHandler(cardService cardServicer) *handler {
	return &handler{cardService: cardService}
}

// registerCardRoutes registers card CRUD and redemption endpoints.
func (h *handler) registerCardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /cards", h.createCard)
	mux.HandleFunc("POST /cards/{code}/redeem", h.redeemCard)
	mux.HandleFunc("GET /cards/{code}", h.getCard)
	mux.HandleFunc("GET /cards/{code}/balance", h.getCardBalance)
	mux.HandleFunc("GET /cards/{code}/validate", h.validateCard)
}

// registerTreasuryRoutes registers treasury balance endpoints.
func (h *handler) registerTreasuryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /treasury/balance", h.getTreasuryBalance)
}

// routes builds the HTTP router and wraps it with the middleware chain.
//
// Middleware order (outermost → innermost):
//
//  1. requestIDMiddleware — injects/echoes X-Request-ID; must be outermost so all
//     downstream middleware (logging, recovery) have the ID in context
//  2. loggingMiddleware   — logs after the request completes (captures real status + duration + request ID)
//  3. recoveryMiddleware  — catches panics, writes 500 before logging records it
//  4. corsMiddleware      — sets CORS headers, short-circuits OPTIONS preflights
//  5. rateLimitMiddleware — rejects IPs exceeding the per-window request limit
func (h *handler) routes() http.Handler {
	root := http.NewServeMux()

	apiV1 := http.NewServeMux()
	h.registerCardRoutes(apiV1)
	h.registerTreasuryRoutes(apiV1)

	root.Handle("/api/", http.StripPrefix("/api", apiV1))
	root.HandleFunc("GET /health", h.healthCheck)

	return requestIDMiddleware(loggingMiddleware(recoveryMiddleware(corsMiddleware(rateLimitMiddleware(root)))))
}
