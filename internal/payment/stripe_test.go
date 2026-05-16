package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWebhookSecret = "test_webhook_secret_for_unit_tests"

// signStripePayload returns a valid Stripe-Signature header for the given body.
// Format: t={unix},v1={hex(hmac-sha256(secret, "{unix}.{body}"))}
// This mirrors the algorithm used by the stripe-go SDK's webhook.ConstructEvent.
func signStripePayload(t *testing.T, body []byte, secret string) string {
	t.Helper()
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s", ts, body)
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", ts, sig)
}

// checkoutEventJSON builds a minimal Stripe event payload for checkout events.
// api_version must match the version expected by stripe-go v82 (2025-08-27.basil).
func checkoutEventJSON(eventType, sessionID string) []byte {
	payload := map[string]any{
		"id":          "evt_test_" + sessionID,
		"object":      "event",
		"api_version": "2025-08-27.basil",
		"type":        eventType,
		"data": map[string]any{
			"object": map[string]any{
				"id":     sessionID,
				"object": "checkout.session",
			},
		},
	}
	b, _ := json.Marshal(payload)
	return b
}

func TestConstructEvent_CompletedEvent(t *testing.T) {
	c := &stripeClient{cfg: Config{WebhookSecret: testWebhookSecret}}
	sessionID := "cs_test_completed_abc123"
	body := checkoutEventJSON(EventCheckoutCompleted, sessionID)
	header := signStripePayload(t, body, testWebhookSecret)

	event, err := c.ConstructEvent(body, header)
	require.NoError(t, err)
	assert.Equal(t, EventCheckoutCompleted, event.Type)
	require.NotNil(t, event.CheckoutSession)
	assert.Equal(t, sessionID, event.CheckoutSession.ID)
}

func TestConstructEvent_ExpiredEvent(t *testing.T) {
	c := &stripeClient{cfg: Config{WebhookSecret: testWebhookSecret}}
	sessionID := "cs_test_expired_xyz789"
	body := checkoutEventJSON(EventCheckoutExpired, sessionID)
	header := signStripePayload(t, body, testWebhookSecret)

	event, err := c.ConstructEvent(body, header)
	require.NoError(t, err)
	assert.Equal(t, EventCheckoutExpired, event.Type)
	require.NotNil(t, event.CheckoutSession)
	assert.Equal(t, sessionID, event.CheckoutSession.ID)
}

// TestConstructEvent_UnknownEventType verifies that non-checkout events are
// handled without error and with a nil CheckoutSession.
func TestConstructEvent_UnknownEventType(t *testing.T) {
	c := &stripeClient{cfg: Config{WebhookSecret: testWebhookSecret}}
	body, _ := json.Marshal(map[string]any{
		"id":          "evt_test_pi",
		"object":      "event",
		"api_version": "2025-08-27.basil",
		"type":        "payment_intent.created",
		"data": map[string]any{
			"object": map[string]any{
				"id":     "pi_test_abc",
				"object": "payment_intent",
			},
		},
	})
	header := signStripePayload(t, body, testWebhookSecret)

	event, err := c.ConstructEvent(body, header)
	require.NoError(t, err)
	assert.Equal(t, "payment_intent.created", event.Type)
	assert.Nil(t, event.CheckoutSession, "non-checkout events must have nil CheckoutSession")
}

// TestConstructEvent_InvalidSignature verifies that a tampered payload is rejected.
func TestConstructEvent_InvalidSignature(t *testing.T) {
	c := &stripeClient{cfg: Config{WebhookSecret: testWebhookSecret}}
	body := checkoutEventJSON(EventCheckoutCompleted, "cs_test_xyz")
	// Sign with a different secret so the HMAC won't match.
	badHeader := signStripePayload(t, body, "wrong_secret")

	_, err := c.ConstructEvent(body, badHeader)
	assert.Error(t, err)
}

// TestConstructEvent_TamperedPayload verifies that modifying the body after
// signing causes signature verification to fail.
func TestConstructEvent_TamperedPayload(t *testing.T) {
	c := &stripeClient{cfg: Config{WebhookSecret: testWebhookSecret}}
	original := checkoutEventJSON(EventCheckoutCompleted, "cs_test_tamper")
	header := signStripePayload(t, original, testWebhookSecret)

	// Replace the session ID in the body after signing.
	tampered := checkoutEventJSON(EventCheckoutCompleted, "cs_attacker_session")

	_, err := c.ConstructEvent(tampered, header)
	assert.Error(t, err)
}
