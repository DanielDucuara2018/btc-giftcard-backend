package main

import (
	"btc-giftcard/internal/card"
	"net/http"
)

// handler holds dependencies for all HTTP endpoint handlers.
type handler struct {
	cardService *card.Service
}

func newHandler(cardService *card.Service) *handler {
	return &handler{cardService: cardService}
}

// TODO Middleware to apply (in order):
//   - Request logging (method, path, status, duration)
//   - Panic recovery (catch panics, return 500)
//   - CORS headers (configurable allowed origins)
//   - Request ID injection (X-Request-ID header)
//   - Rate limiting per IP (Redis-backed, configurable limits)

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

// routes builds the HTTP router with all API routes under /api/ and
// the health check at the root.
func (h *handler) routes() http.Handler {
	root := http.NewServeMux()

	apiV1 := http.NewServeMux()
	h.registerCardRoutes(apiV1)
	h.registerTreasuryRoutes(apiV1)

	root.Handle("/api/", http.StripPrefix("/api", apiV1))
	root.HandleFunc("GET /health", h.healthCheck)

	return root
}
