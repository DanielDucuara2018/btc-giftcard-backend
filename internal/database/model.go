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

type Card struct {
	ID                 string     `json:"id" db:"id"`
	UserID             *string    `json:"user_id,omitempty" db:"user_id"`
	PurchaseEmail      string     `json:"purchase_email" db:"purchase_email"`
	OwnerEmail         string     `json:"owner_email" db:"owner_email"`
	Code               string     `json:"code" db:"code"`
	BTCAmountSats      int64      `json:"btc_amount_sats" db:"btc_amount_sats"`     // Satoshis (1 BTC = 100,000,000 sats)
	FiatAmountCents    int64      `json:"fiat_amount_cents" db:"fiat_amount_cents"` // Cents (e.g., $100.50 = 10050)
	FiatCurrency       string     `json:"fiat_currency" db:"fiat_currency"`
	PurchasePriceCents int64      `json:"purchase_price_cents" db:"purchase_price_cents"` // Total charged in cents
	Status             CardStatus `json:"status" db:"status"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	RedeemedAt         *time.Time `json:"redeemed_at,omitempty" db:"redeemed_at"`
	FundedAt           *time.Time `json:"funded_at,omitempty" db:"funded_at"`
}

// GetBTC returns BTC amount as float64 for display (e.g., 0.00152345)
func (c *Card) GetBTC() float64 {
	return float64(c.BTCAmountSats) / 100_000_000
}

// GetFiatAmount returns fiat amount as float64 for display (e.g., 100.50)
func (c *Card) GetFiatAmount() float64 {
	return float64(c.FiatAmountCents) / 100
}

// GetPurchasePrice returns purchase price as float64 for display (e.g., 103.00)
func (c *Card) GetPurchasePrice() float64 {
	return float64(c.PurchasePriceCents) / 100
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
