package fees

import (
	"btc-giftcard/internal/database"
	"errors"
	"math"
)

// ============================================================================
// Errors
// ============================================================================

var (
	ErrUnsupportedMethod = errors.New("unsupported payment method")
	ErrInvalidFiatAmount = errors.New("fiat amount must be positive")
)

// Fees defines the fee stack embedded in every card price
type Config struct {
	ServiceFeePct    float64
	StripeFeePct     float64
	StripeFeeFlatEUR float64
	CryptoSpreadPct  float64
	SEPAFeeEUR       float64
	PaymentExpiryH   int
}

// ============================================================================
// Breakdown — fee calculation result
// ============================================================================

// Breakdown holds the full fee decomposition for a single card purchase.
// All amounts are in euro cents unless the field name says otherwise.
type Breakdown struct {
	// FaceValueCents is the amount the customer pays (i.e. CreateCardRequest.FiatAmountCents).
	FaceValueCents int64

	// ServiceFeeCents is the platform margin embedded in the price.
	ServiceFeeCents int64

	// ProcessorFeeCents is the payment processor fee (Stripe) or 0 for SEPA.
	ProcessorFeeCents int64

	// ProcessorFeeFlatCents is the flat portion of the processor fee (e.g. €0.25 → 25).
	ProcessorFeeFlatCents int64

	// CryptoSpreadCents is the estimated OTC spread cost passed to the customer.
	CryptoSpreadCents int64

	// SEPAFeeCents is the incoming SEPA transfer fee (currently 0 via Qonto).
	SEPAFeeCents int64

	// TotalFeeCents is the sum of all fees above.
	TotalFeeCents int64

	// NetEURCents is the amount of the face value that actually buys BTC.
	// NetEURCents = FaceValueCents - TotalFeeCents
	NetEURCents int64

	// Method is the payment rail used for this card.
	Method database.PaymentMethod
}

// ============================================================================
// Calculate
// ============================================================================

// Calculate derives the fee breakdown for a card purchase.
//
// faceValueCents is the amount the customer pays (CreateCardRequest.FiatAmountCents).
// method selects the payment rail, which determines which processor fee applies.
// cfg is the fee configuration loaded from config.toml / environment.
//
// Returns ErrInvalidFiatAmount if faceValueCents ≤ 0.
// Returns ErrUnsupportedMethod if method is not MethodCard or MethodBankTransfer.
func Calculate(faceValueCents int64, method database.PaymentMethod, fees *Config) (Breakdown, error) {
	if faceValueCents <= 0 {
		return Breakdown{}, ErrInvalidFiatAmount
	}

	if !method.IsValid() {
		return Breakdown{}, ErrUnsupportedMethod
	}

	b := Breakdown{
		FaceValueCents:    faceValueCents,
		ServiceFeeCents:   pctOf(fees.ServiceFeePct, faceValueCents),
		CryptoSpreadCents: pctOf(fees.CryptoSpreadPct, faceValueCents),
		SEPAFeeCents:      eurToCents(fees.SEPAFeeEUR),
		Method:            method,
	}

	switch method {
	case database.CardBlue:
		b.ProcessorFeeCents = pctOf(fees.StripeFeePct, faceValueCents)
		b.ProcessorFeeFlatCents = eurToCents(fees.StripeFeeFlatEUR)
	case database.BankTranfer:
		// SEPA incoming transfer is free via Qonto; SEPAFeeCents already covers any future fee
		b.ProcessorFeeCents = 0
		b.ProcessorFeeFlatCents = 0
	}

	b.TotalFeeCents = b.ServiceFeeCents + b.ProcessorFeeCents + b.ProcessorFeeFlatCents +
		b.CryptoSpreadCents + b.SEPAFeeCents
	b.NetEURCents = faceValueCents - b.TotalFeeCents

	return b, nil
}

// eurToCents converts a euro float (e.g. 0.25) to integer euro cents (25).
func eurToCents(eur float64) int64 {
	return int64(math.Round(eur * 100))
}

// pctOf applies a percentage (e.g. 1.5 for 1.5%) to an amount in cents,
// rounding to the nearest cent.
func pctOf(pct float64, amountCents int64) int64 {
	return int64(math.Round(pct / 100.0 * float64(amountCents)))
}
