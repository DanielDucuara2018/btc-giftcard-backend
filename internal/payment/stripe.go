package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
)

// Config holds the Stripe credentials and redirect URLs needed by the server.
// All URLs must be fully assembled before passing to NewStripeClient —
// use cfg.StripeSuccessURL() / cfg.StripeCancelURL() from config.ApiConfig.
type Config struct {
	SecretKey     string // sk_test_* (sandbox) or sk_live_* (production)
	WebhookSecret string // whsec_* — used to verify incoming webhook signatures
	SuccessURL    string // full redirect URL after successful payment
	CancelURL     string // full redirect URL when customer cancels
}

type stripeClient struct {
	cfg Config
}

func NewStripeClient(cfg Config) Provider {
	stripe.Key = cfg.SecretKey
	return &stripeClient{
		cfg: cfg,
	}
}

func (c *stripeClient) CreateCheckoutSession(ctx context.Context, req CreateCheckoutRequest) (*CheckoutSession, error) {
	expiresAt := time.Now().Add(24 * time.Hour)

	lineItems := make([]*stripe.CheckoutSessionLineItemParams, len(req.Items))
	for i, item := range req.Items {
		lineItems[i] = &stripe.CheckoutSessionLineItemParams{
			PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
				Currency:   stripe.String("eur"),
				UnitAmount: stripe.Int64(item.FaceValueCents),
				ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
					Name: stripe.String(item.Description),
				},
			},
			Quantity: stripe.Int64(item.Quantity),
		}
	}

	params := &stripe.CheckoutSessionParams{
		LineItems:  lineItems,
		Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL: stripe.String(c.cfg.SuccessURL),
		CancelURL:  stripe.String(c.cfg.CancelURL),
		ExpiresAt:  stripe.Int64(expiresAt.Unix()),
		Metadata: map[string]string{
			"purchase_email": req.PurchaseEmail,
		},
	}
	params.Context = ctx

	result, err := session.New(params)
	if err != nil {
		return nil, err
	}

	return &CheckoutSession{
		ID:        result.ID,
		URL:       result.URL,
		ExpiresAt: time.Unix(result.ExpiresAt, 0).UTC(),
	}, nil
}

func (c *stripeClient) ConstructEvent(rawBody []byte, sigHeader string) (*Event, error) {
	stripeEvent, err := webhook.ConstructEvent(rawBody, sigHeader, c.cfg.WebhookSecret)
	if err != nil {
		return nil, err
	}

	event := &Event{Type: string(stripeEvent.Type)}

	switch stripeEvent.Type {
	case EventCheckoutCompleted, EventCheckoutExpired:
		var s stripe.CheckoutSession
		if err := json.Unmarshal(stripeEvent.Data.Raw, &s); err != nil {
			return nil, fmt.Errorf("failed to unmarshal checkout session from event: %w", err)
		}
		event.CheckoutSession = &CheckoutSessionPayload{
			ID: s.ID,
		}
	}

	return event, nil
}
