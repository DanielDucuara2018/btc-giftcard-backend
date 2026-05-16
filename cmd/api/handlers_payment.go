package main

import (
	"btc-giftcard/pkg/logger"
	"io"
	"net/http"

	"go.uber.org/zap"
)

func (h *handler) cardPayment(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(r.Body)
	if handleError(w, err) {
		return
	}

	event, err := h.stripeClient.ConstructEvent(payload, r.Header.Get("Stripe-Signature"))
	if handleError(w, err) {
		return
	}

	if err := h.cardService.HandleCheckoutEvent(r.Context(), event); err != nil {
		// log the error but still return 200 to prevent Stripe from retrying
		// a permanently-broken event (e.g. DB down → transient; log + alert)
		logger.Error("stripe webhook processing failed", zap.Error(err))
	}

	w.WriteHeader(http.StatusOK)
}
