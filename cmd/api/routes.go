package main

import (
	"net/http"

	"btc-giftcard/internal/lnd"
)

// handler holds dependencies for all HTTP endpoint handlers.
type handler struct {
	cardService  cardServicer
	lndClient    lnd.LightningClient
	stripeClient stripeClient
}

func newHandler(cardService cardServicer, lndClient lnd.LightningClient, stripeClient stripeClient) *handler {
	return &handler{cardService: cardService, lndClient: lndClient, stripeClient: stripeClient}
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

// registerNodeRoutes registers LND node management endpoints.
//
// Wallet:
//
//	GET  /node/wallet/balance   — on-chain wallet balance
//	POST /node/wallet/address   — generate a new deposit address
//
// Channels:
//
//	GET  /node/channels/balance — aggregate channel balance
//	GET  /node/channels         — list open channels
//	POST /node/channels         — open a new channel (requires connected peer)
//
// Peers:
//
//	GET  /node/peers            — list connected peers
//	POST /node/peers            — connect to a peer
//
// Info:
//
//	GET  /node/info             — node alias, pubkey, sync status
func (h *handler) registerNodeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /node/info", h.getNodeInfo)
	mux.HandleFunc("GET /node/wallet/balance", h.getWalletBalance)
	mux.HandleFunc("POST /node/wallet/address", h.newWalletAddress)
	mux.HandleFunc("GET /node/channels/balance", h.getChannelBalance)
	mux.HandleFunc("GET /node/channels", h.listChannels)
	mux.HandleFunc("POST /node/channels", h.openChannel)
	mux.HandleFunc("GET /node/peers", h.listPeers)
	mux.HandleFunc("POST /node/peers", h.connectPeer)
}

func (h *handler) registerPaymentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /webhook/stripe", h.cardPayment)
	// mux.HandleFunc("POST /webhook/qonto", h.bankTransferPayment)
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
	h.registerNodeRoutes(apiV1)
	h.registerPaymentRoutes(apiV1)

	root.Handle("/api/", http.StripPrefix("/api", apiV1))
	root.HandleFunc("GET /health", h.healthCheck)

	return requestIDMiddleware(loggingMiddleware(recoveryMiddleware(corsMiddleware(rateLimitMiddleware(root)))))
}
