package main

import (
	"btc-giftcard/internal/card"
	"fmt"
	"net/http"
)

// handler holds dependencies for all HTTP endpoint handlers.
type handler struct {
	cardService *card.Service
}

func newHandler(cardService *card.Service) *handler {
	return &handler{cardService: cardService}
}

// routes builds the HTTP router and returns it as an http.Handler.
//
// TODO: Choose and import a router (e.g., chi, gorilla/mux, or net/http.ServeMux).
//
// Routes to register:
//
//	POST   /api/cards                  → h.createCard       Create a new gift card
//	POST   /api/cards/{code}/redeem    → h.redeemCard       Redeem (spend) a card
//	GET    /api/cards/{code}           → h.getCard           Get card details by code
//	GET    /api/cards/{code}/balance   → h.getCardBalance    Get card balance in sats
//	GET    /api/cards/{code}/validate  → h.validateCard      Validate a card code exists and is active
//	GET    /api/treasury/balance       → h.getTreasuryBalance  Get available treasury balance
//	GET    /health                     → h.healthCheck       Liveness probe
//
// Middleware to apply (in order):
//   - Request logging (method, path, status, duration)
//   - Panic recovery (catch panics, return 500)
//   - CORS headers (configurable allowed origins)
//   - Request ID injection (X-Request-ID header)
//   - Rate limiting per IP (Redis-backed, configurable limits)
func (h *handler) registerCardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /cards", h.createCard)
	mux.HandleFunc("POST /cards/{code}/redeem", h.redeemCard)
}

func (h *handler) registerTreasuryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /treasury/balance", h.getTreasuryBalance)
}

func (h *handler) routes() http.Handler {
	// TODO: Implement router setup with all routes and middleware listed above.
	root := http.NewServeMux()

	apiV1 := http.NewServeMux()
	h.registerCardRoutes(apiV1)
	h.registerTreasuryRoutes(apiV1)

	root.Handle("/api", http.StripPrefix("/api", apiV1))

	root.HandleFunc("Get /", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	return root
}
