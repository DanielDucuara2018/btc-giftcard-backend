package database

import (
	"time"
)

// CardStatus represents the lifecycle state of a gift card.
type CardStatus string

// TransactionType represents the kind of transaction.
type TransactionType string

// TransactionStatus represents the state of a transaction.
type TransactionStatus string

const (
	Created  CardStatus = "created"
	Funding  CardStatus = "funding"
	Active   CardStatus = "active"
	Redeemed CardStatus = "redeemed"
	Expired  CardStatus = "expired"
)

const (
	Fund    TransactionType = "fund"
	Redeem  TransactionType = "redeem"
	Payment TransactionType = "payment"
)

const (
	Pending   TransactionStatus = "pending"
	Confirmed TransactionStatus = "confirmed"
	Failed    TransactionStatus = "failed"
)

// CardPaymentStatus represents the payment state for a gift card purchase.
type CardPaymentStatus string

const (
	PaymentPending CardPaymentStatus = "pending"
	PaymentPaid    CardPaymentStatus = "paid"
	PaymentFailed  CardPaymentStatus = "failed"
	PaymentExpired CardPaymentStatus = "expired"
)

type PaymentMethod string

const (
	CardBlue    PaymentMethod = "card"
	BankTranfer PaymentMethod = "bank_transfer"
)

// IsValid returns true if the currency is a supported fiat currency.
func (c PaymentMethod) IsValid() bool {
	switch c {
	case CardBlue, BankTranfer:
		return true
	default:
		return false
	}
}

// FiatCurrency represents a supported fiat currency for card purchases.
type FiatCurrency string

const (
	FiatUSD FiatCurrency = "USD"
	FiatEUR FiatCurrency = "EUR"
)

// IsValid returns true if the currency is a supported fiat currency.
func (c FiatCurrency) IsValid() bool {
	switch c {
	case FiatUSD, FiatEUR:
		return true
	default:
		return false
	}
}

type Card struct {
	ID              string  `json:"id" db:"id"`
	UserID          *string `json:"user_id,omitempty" db:"user_id"`
	PurchaseEmail   string  `json:"purchase_email" db:"purchase_email"`
	OwnerEmail      string  `json:"owner_email" db:"owner_email"`
	Code            string  `json:"code" db:"code"`
	BTCAmountSats   int64   `json:"btc_amount_sats" db:"btc_amount_sats"`     // Satoshis (1 BTC = 100,000,000 sats)
	FiatAmountCents int64   `json:"fiat_amount_cents" db:"fiat_amount_cents"` // Gross face value (what customer pays), in cents
	FiatCurrency    FiatCurrency  `json:"fiat_currency" db:"fiat_currency"`

	// Payment intake
	PaymentMethod     PaymentMethod     `json:"payment_method" db:"payment_method"`                 // "card" | "bank_transfer"
	PaymentReference  *string           `json:"payment_reference,omitempty" db:"payment_reference"` // Stripe session ID | SEPA ref
	PaymentStatus     CardPaymentStatus `json:"payment_status" db:"payment_status"`
	PaymentExpiresAt  *time.Time        `json:"payment_expires_at,omitempty" db:"payment_expires_at"`
	StripeCheckoutURL *string           `json:"stripe_checkout_url,omitempty" db:"stripe_checkout_url"`
	SEPAReference     *string           `json:"sepa_reference,omitempty" db:"sepa_reference"`

	// Fee snapshot (locked at creation, never updated)
	ServiceFeeCents       int64 `json:"service_fee_cents" db:"service_fee_cents"`
	ProcessorFeeCents     int64 `json:"processor_fee_cents" db:"processor_fee_cents"`
	ProcessorFeeFlatCents int64 `json:"processor_fee_flat_cents" db:"processor_fee_flat_cents"`
	CryptoSpreadCents     int64 `json:"crypto_spread_cents" db:"crypto_spread_cents"`
	SEPAFeeCents          int64 `json:"sepa_fee_cents" db:"sepa_fee_cents"`
	TotalFeeCents         int64 `json:"total_fee_cents" db:"total_fee_cents"`
	// StripeActualFeeCents is populated async after T+1 Stripe settlement (reconciliation only)
	StripeActualFeeCents int64 `json:"stripe_fee_actual_cents" db:"stripe_fee_actual_cents"`
	// BTCPriceEURCents is the BTC/EUR price locked at card creation (audit trail)
	BTCPriceEURCents int64 `json:"btc_price_eur_cents" db:"btc_price_eur_cents"`

	Status     CardStatus `json:"status" db:"status"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	RedeemedAt *time.Time `json:"redeemed_at,omitempty" db:"redeemed_at"`
	FundedAt   *time.Time `json:"funded_at,omitempty" db:"funded_at"`
}

// GetBTC returns BTC amount as float64 for display (e.g., 0.00152345)
func (c *Card) GetBTC() float64 {
	return float64(c.BTCAmountSats) / 100_000_000
}

// GetFiatAmount returns fiat amount as float64 for display (e.g., 100.50)
func (c *Card) GetFiatAmount() float64 {
	return float64(c.FiatAmountCents) / 100
}

type Transaction struct {
	ID               string            `json:"id" db:"id"`
	CardID           string            `json:"card_id" db:"card_id"`
	Type             TransactionType   `json:"type" db:"type"`
	RedemptionMethod *string           `json:"redemption_method,omitempty" db:"redemption_method"` // 'lightning'
	TxHash           *string           `json:"tx_hash,omitempty" db:"tx_hash"`                     // On-chain tx hash (NULL for Lightning)
	PaymentHash      *string           `json:"payment_hash,omitempty" db:"payment_hash"`           // Lightning payment hash (NULL for on-chain)
	PaymentPreimage  *string           `json:"payment_preimage,omitempty" db:"payment_preimage"`   // Lightning proof of payment (set on success)
	LightningInvoice *string           `json:"lightning_invoice,omitempty" db:"lightning_invoice"` // BOLT11 invoice (NULL for on-chain)
	FromAddress      *string           `json:"from_address,omitempty" db:"from_address"`           // Source Bitcoin address (on-chain)
	ToAddress        *string           `json:"to_address,omitempty" db:"to_address"`               // Destination Bitcoin address (on-chain)
	BTCAmountSats    int64             `json:"btc_amount_sats" db:"btc_amount_sats"`               // Satoshis
	Status           TransactionStatus `json:"status" db:"status"`
	Confirmations    int               `json:"confirmations" db:"confirmations"`
	CreatedAt        time.Time         `json:"created_at" db:"created_at"`
	BroadcastAt      *time.Time        `json:"broadcast_at,omitempty" db:"broadcast_at"` // When sent to blockchain
	ConfirmedAt      *time.Time        `json:"confirmed_at,omitempty" db:"confirmed_at"` // When confirmed
}

// GetBTC returns BTC amount as float64 for display (e.g., 0.00152345)
func (t *Transaction) GetBTC() float64 {
	return float64(t.BTCAmountSats) / 100_000_000
}
