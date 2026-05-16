package payment

import "time"

// Event type constants — provider-agnostic names for webhook event types.
const (
	EventCheckoutCompleted = "checkout.session.completed"
	EventCheckoutExpired   = "checkout.session.expired"
)

// Event is a provider-agnostic webhook event. The handler switches on Type
// and reads only the fields relevant to that event type.
type Event struct {
	Type            string
	CheckoutSession *CheckoutSessionPayload // non-nil for checkout.session.* events
}

// CheckoutSessionPayload holds the session ID from a checkout event.
// The webhook handler uses it to look up all associated cards in the DB via
// GetByStripeSessionID — no card identifiers are stored in Stripe metadata.
type CheckoutSessionPayload struct {
	ID string // Stripe session ID → matches cards.payment_reference
}

// LineItem is one denomination + quantity in a checkout order.
type LineItem struct {
	FaceValueCents int64  // unit price in EUR cents, e.g. 10000 for €100
	Quantity       int64  // number of cards at this denomination
	Description    string // shown on Stripe's hosted checkout page
}

// CreateCheckoutRequest is the input to Provider.CreateCheckoutSession.
// A single session can cover multiple denominations (multi-line-item checkout).
type CreateCheckoutRequest struct {
	Items         []LineItem
	PurchaseEmail string // stored in Stripe session metadata for auditing
}

// CheckoutSession is returned by Provider.CreateCheckoutSession.
type CheckoutSession struct {
	ID        string    // provider session ID → stored as payment_reference
	URL       string    // hosted checkout URL → stored as stripe_checkout_url, sent to frontend
	ExpiresAt time.Time // aligned with cards.payment_expires_at
}
