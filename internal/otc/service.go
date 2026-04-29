package otc

import "context"

// CryptocomService is the interface consumed by treasury.TreasuryManager for
// OTC trading and withdrawal operations.
// All methods that move funds accept an idempotency key (ClDealID / ClientWdID)
// and may safely be retried with the same key on transient errors.
type CryptocomService interface {
	// GetOTCInstruments returns the list of instruments currently available for
	// OTC trading (e.g. BTC_USD, BTC_EUR). Call this to validate a pair before
	// submitting a quote request via WebSocket.
	GetOTCInstruments(ctx context.Context) ([]OTCInstrument, error)

	// RequestQuote connects to the Crypto.com private WebSocket, authenticates,
	// subscribes to user.otc_qr.quotes, and blocks until a Quote with a matching
	// ClQuoteReqID arrives. The returned Quote expires within seconds — call
	// RequestDeal immediately.
	// ctx should carry a deadline of ~30 seconds.
	RequestQuote(ctx context.Context, params RequestQuoteParams) (*Quote, error)

	// RequestDeal executes an OTC deal against an active quote received via the
	// user.otc_qr.quotes WebSocket channel.
	// params.QuoteID and the exact leg prices must be copied verbatim from the
	// Quote — any deviation will result in a QUOTE_NOT_FOUND or INVALID_PRICE error.
	// params.ClDealID is the idempotency key; retrying with the same value is safe.
	RequestDeal(ctx context.Context, params RequestDealParams) (*Deal, error)

	// Withdraw initiates an on-chain BTC withdrawal from the Crypto.com account
	// to the LND node's deposit address.
	// req.ClientWdID is the idempotency key; retrying with the same value is safe.
	Withdraw(ctx context.Context, req WithdrawalRequest) (*Withdrawal, error)

	// GetWithdrawal returns the current state of a withdrawal by its exchange ID.
	// Poll until Withdrawal.Status == WithdrawalCompleted and TxID is set.
	GetWithdrawal(ctx context.Context, withdrawalID string) (*Withdrawal, error)
}
