package payment

import "context"

// Provider is the generic interface for card payment processors.
// No provider-specific types (e.g. stripe.Event) appear here — the handler
// imports only this package, never the underlying SDK.
type Provider interface {
	CreateCheckoutSession(ctx context.Context, req CreateCheckoutRequest) (*CheckoutSession, error)
	ConstructEvent(rawBody []byte, sigHeader string) (*Event, error)
}
