package otc

// OTCInstrument describes a tradeable OTC currency pair, as returned by
// private/otc/get-otc-instruments.
type OTCInstrument struct {
	InstrumentName string `json:"instrument_name"` // e.g. "BTC_USD"
	BaseCurrency   string `json:"base_currency"`
	QuoteCurrency  string `json:"quote_currency"`
	Type           string `json:"type"` // e.g. "SPOT"
	PriceTickSize  string `json:"price_tick_size"`
	QuoteDecimals  int    `json:"quote_decimals"` // API returns integer, docs say string
	QtyTickSize    string `json:"qty_tick_size"`
	QtyDecimals    int    `json:"qty_decimals"` // API returns integer, docs say string
}

// QuoteLegRequest is a single leg for private/otc/request-quote.
// Exactly one of Quantity or Notional must be set.
type QuoteLegRequest struct {
	InstrumentName string `json:"instrument_name"`
	Side           string `json:"side"`               // "BUY" | "SELL"
	Quantity       string `json:"quantity,omitempty"` // BTC amount; mutually exclusive with Notional
	Notional       string `json:"notional,omitempty"` // fiat amount; mutually exclusive with Quantity
}

// RequestQuoteParams is the params for private/otc/request-quote.
// Note: this endpoint is WebSocket-only; use a WebSocket client to send it.
type RequestQuoteParams struct {
	ClQuoteReqID          string            `json:"cl_quote_req_id"`
	FirmQuote             bool              `json:"firm_quote,omitempty"`
	SettlementArrangement string            `json:"settlement_arrangement,omitempty"` // "IMMEDIATE" | "T1"
	Duration              string            `json:"duration,omitempty"`               // ms: "5000".."600000"
	QuoteTTL              string            `json:"quote_ttl,omitempty"`              // "4000" | "5000"
	LegList               []QuoteLegRequest `json:"leg_list"`
}

// QuoteRequestAck is pushed on user.otc_qr.requests after a quote request is
// submitted. It carries the system-generated quote_req_id and status updates.
type QuoteRequestAck struct {
	QuoteReqID   string `json:"quote_req_id"`
	ClQuoteReqID string `json:"cl_quote_req_id"`
	Status       string `json:"status,omitempty"` // "ACTIVE" | "REJECTED" | "COMPLETED"
	Reason       string `json:"reason,omitempty"`
}

// QuoteLeg is a single leg in a quote delivered via user.otc_qr.quotes.
type QuoteLeg struct {
	InstrumentName string `json:"instrument_name"`
	Side           string `json:"side,omitempty"`
	Quantity       string `json:"quantity,omitempty"`
	Notional       string `json:"notional,omitempty"`
	Bid            string `json:"bid,omitempty"`
	Ask            string `json:"ask,omitempty"`
	Type           string `json:"type,omitempty"` // "PRICE"
}

// Quote is a price quote delivered via the user.otc_qr.quotes WebSocket channel.
// The taker must call RequestDeal with QuoteID before ExpiryTimeNs elapses.
type Quote struct {
	QuoteID        string     `json:"quote_id"`
	QuoteReqID     string     `json:"quote_req_id"`
	ClQuoteReqID   string     `json:"cl_quote_req_id"`
	Status         string     `json:"status"` // "ACTIVE"
	Reason         string     `json:"reason"`
	ResponseTimeNs string     `json:"response_time_ns"` // nanoseconds since Unix epoch
	ExpiryTimeNs   string     `json:"expiry_time_ns"`   // nanoseconds since Unix epoch
	LegList        []QuoteLeg `json:"leg_list"`
}

// DealLegRequest is a single leg for private/otc/request-deal.
// Price, Side, and one of Quantity/Notional must be copied exactly from the
// corresponding Quote leg received via user.otc_qr.quotes.
type DealLegRequest struct {
	InstrumentName string `json:"instrument_name"`
	Price          string `json:"price"`
	Side           string `json:"side"`               // "BUY" | "SELL"
	Quantity       string `json:"quantity,omitempty"` // mutually exclusive with Notional
	Notional       string `json:"notional,omitempty"` // mutually exclusive with Quantity
}

// RequestDealParams is the params for private/otc/request-deal.
// DealType must be "QUOTE_REQUEST".
// ClDealID is the idempotency key — safe to retry with the same value.
type RequestDealParams struct {
	DealType   string           `json:"deal_type"` // "QUOTE_REQUEST"
	ClDealID   string           `json:"cl_deal_id"`
	QuoteID    string           `json:"quote_id"`
	QuoteReqID string           `json:"quote_req_id"`
	LegList    []DealLegRequest `json:"leg_list"`
}

// DealStatus represents the lifecycle state of an OTC deal.
type DealStatus string

const (
	DealAccepted  DealStatus = "ACCEPTED"  // exchange received the deal
	DealConfirmed DealStatus = "CONFIRMED" // LP executed the deal
	DealSettled   DealStatus = "SETTLED"   // funds transferred
	DealRejected  DealStatus = "REJECTED"
)

// DealLeg is a single leg in a deal result.
type DealLeg struct {
	InstrumentName   string `json:"instrument_name"`
	Price            string `json:"price"`
	Side             string `json:"side"`
	Quantity         string `json:"quantity,omitempty"`
	Notional         string `json:"notional,omitempty"`
	ExecutedQty      string `json:"executed_quantity,omitempty"`
	ExecutedNotional string `json:"executed_notional,omitempty"`
	Type             string `json:"type,omitempty"` // "PRICE"
}

// Deal is the result of private/otc/request-deal.
type Deal struct {
	DealID       string     `json:"deal_id"`
	ClDealID     string     `json:"cl_deal_id"`
	QuoteID      string     `json:"quote_id"`
	QuoteReqID   string     `json:"quote_req_id"`
	DealType     string     `json:"deal_type"`
	DealStatus   DealStatus `json:"deal_status"`
	Reason       string     `json:"reason"`
	UpdateTimeNs string     `json:"update_time_ns"` // nanoseconds since Unix epoch
	LegList      []DealLeg  `json:"leg_list"`
}

// WithdrawalStatus mirrors Crypto.com's numeric withdrawal status codes
// as returned by private/get-withdrawal-history.
type WithdrawalStatus string

const (
	WithdrawalPending       WithdrawalStatus = "0" // Pending
	WithdrawalProcessing    WithdrawalStatus = "1" // Processing
	WithdrawalRejected      WithdrawalStatus = "2" // Rejected
	WithdrawalPaymentInProg WithdrawalStatus = "3" // Payment In-progress
	WithdrawalPaymentFailed WithdrawalStatus = "4" // Payment Failed
	WithdrawalCompleted     WithdrawalStatus = "5" // Completed
	WithdrawalCancelled     WithdrawalStatus = "6" // Cancelled
)

// WithdrawalRequest is the payload for private/create-withdrawal.
// ClientWdID is the idempotency key — safe to retry with the same value.
type WithdrawalRequest struct {
	Currency   string `json:"currency"`   // "BTC"
	Amount     string `json:"amount"`     // BTC amount as a decimal string
	Address    string `json:"address"`    // LND on-chain deposit address (bech32)
	NetworkID  string `json:"network_id"` // "BTC" for on-chain mainnet/testnet
	ClientWdID string `json:"client_wid"` // idempotency key (UUID recommended)
}

// Withdrawal holds the current state of a withdrawal request,
// as returned by private/get-withdrawal-history.
type Withdrawal struct {
	ID         string           `json:"id"`
	ClientWdID string           `json:"client_wid"`
	Currency   string           `json:"currency"`
	Amount     float64          `json:"amount"`   // API returns a number
	Address    string           `json:"address"`
	Status     WithdrawalStatus `json:"status"`   // numeric code string e.g. "5"
	TxID       string           `json:"txid"`     // empty string until broadcast
	Fee        float64          `json:"fee"`      // API returns a number
	NetworkID  *string          `json:"network_id"`
	CreateTime int64            `json:"create_time"` // Unix milliseconds
	UpdateTime int64            `json:"update_time"` // Unix milliseconds
}

// IsConfirmed returns true when the withdrawal has been broadcast and completed.
func (w *Withdrawal) IsConfirmed() bool {
	return w.Status == WithdrawalCompleted && w.TxID != ""
}
