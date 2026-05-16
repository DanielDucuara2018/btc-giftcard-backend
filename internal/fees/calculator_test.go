package fees

import (
	"btc-giftcard/internal/database"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCfg returns a deterministic fee configuration for unit tests.
func testCfg() *Config {
	return &Config{
		ServiceFeePct:    2.0,
		StripeFeePct:     1.5,
		StripeFeeFlatEUR: 0.25,
		CryptoSpreadPct:  0.16,
		SEPAFeeEUR:       0.0,
	}
}

func TestPaymentMethod_IsValid(t *testing.T) {
	assert.True(t, database.CardBlue.IsValid())
	assert.True(t, database.BankTranfer.IsValid())
	assert.False(t, database.PaymentMethod("").IsValid())
	assert.False(t, database.PaymentMethod("unknown").IsValid())
	assert.False(t, database.PaymentMethod("crypto").IsValid())
}

// TestCalculate_Card verifies the full fee breakdown for a Stripe card payment.
// Using €100.00 (10000 cents) so each percentage is easy to check manually.
func TestCalculate_Card(t *testing.T) {
	b, err := Calculate(10000, database.CardBlue, testCfg())
	require.NoError(t, err)

	assert.Equal(t, int64(10000), b.FaceValueCents)
	assert.Equal(t, int64(200), b.ServiceFeeCents)      // 2.0% of 10000
	assert.Equal(t, int64(150), b.ProcessorFeeCents)    // 1.5% of 10000
	assert.Equal(t, int64(25), b.ProcessorFeeFlatCents) // €0.25
	assert.Equal(t, int64(16), b.CryptoSpreadCents)     // 0.16% of 10000
	assert.Equal(t, int64(0), b.SEPAFeeCents)
	assert.Equal(t, int64(391), b.TotalFeeCents) // 200+150+25+16
	assert.Equal(t, int64(9609), b.NetEURCents)  // 10000-391
	assert.Equal(t, database.CardBlue, b.Method)
}

// TestCalculate_BankTransfer verifies that no Stripe fees are applied for SEPA.
func TestCalculate_BankTransfer(t *testing.T) {
	b, err := Calculate(10000, database.BankTranfer, testCfg())
	require.NoError(t, err)

	assert.Equal(t, int64(10000), b.FaceValueCents)
	assert.Equal(t, int64(200), b.ServiceFeeCents)
	assert.Equal(t, int64(0), b.ProcessorFeeCents)
	assert.Equal(t, int64(0), b.ProcessorFeeFlatCents)
	assert.Equal(t, int64(16), b.CryptoSpreadCents)
	assert.Equal(t, int64(0), b.SEPAFeeCents)
	assert.Equal(t, int64(216), b.TotalFeeCents) // 200+16
	assert.Equal(t, int64(9784), b.NetEURCents)
	assert.Equal(t, database.BankTranfer, b.Method)
}

// TestCalculate_SEPAFee verifies that a non-zero SEPA fee is included in the total.
func TestCalculate_SEPAFee(t *testing.T) {
	cfg := testCfg()
	cfg.SEPAFeeEUR = 0.35 // €0.35 → 35 cents

	b, err := Calculate(10000, database.BankTranfer, cfg)
	require.NoError(t, err)

	assert.Equal(t, int64(35), b.SEPAFeeCents)
	assert.Equal(t, int64(251), b.TotalFeeCents) // 200+16+35
	assert.Equal(t, int64(9749), b.NetEURCents)
}

// TestCalculate_SmallAmount verifies rounding behaviour on a €1.00 card.
func TestCalculate_SmallAmount(t *testing.T) {
	b, err := Calculate(100, database.CardBlue, testCfg())
	require.NoError(t, err)

	assert.Equal(t, int64(100), b.FaceValueCents)
	assert.Equal(t, int64(2), b.ServiceFeeCents)        // round(2.0% of 100)  = 2
	assert.Equal(t, int64(2), b.ProcessorFeeCents)      // round(1.5% of 100) = 2
	assert.Equal(t, int64(25), b.ProcessorFeeFlatCents) // flat: always 25
	assert.Equal(t, int64(0), b.CryptoSpreadCents)      // round(0.16% of 100) = 0
	assert.Equal(t, int64(29), b.TotalFeeCents)         // 2+2+25+0
	assert.Equal(t, int64(71), b.NetEURCents)
}

func TestCalculate_ZeroAmount(t *testing.T) {
	_, err := Calculate(0, database.CardBlue, testCfg())
	assert.ErrorIs(t, err, ErrInvalidFiatAmount)
}

func TestCalculate_NegativeAmount(t *testing.T) {
	_, err := Calculate(-1, database.CardBlue, testCfg())
	assert.ErrorIs(t, err, ErrInvalidFiatAmount)
}

func TestCalculate_UnsupportedMethod(t *testing.T) {
	_, err := Calculate(10000, database.PaymentMethod("wire"), testCfg())
	assert.ErrorIs(t, err, ErrUnsupportedMethod)
}
